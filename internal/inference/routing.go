package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/telemetry"
)

// ContentPart represents a single content element in a multimodal message.
// Used with the OpenAI-compatible vision API format.
type ContentPart struct {
	Type     string    `json:"type"`                // "text" | "image_url"
	Text     string    `json:"text,omitempty"`      // For type="text"
	ImageURL *ImageURL `json:"image_url,omitempty"` // For type="image_url"
}

// ImageURL holds the URL (or data URI) for an image content part.
type ImageURL struct {
	URL string `json:"url"` // "data:image/png;base64,..." or "https://..."
}

// InferenceMessage represents a single message in a multi-turn conversation.
// For text-only messages, use Content. For multimodal messages (text + images),
// use Parts instead — when Parts is non-empty it takes precedence over Content.
type InferenceMessage struct {
	Role    string        `json:"role"`            // "system" | "user" | "assistant"
	Content string        `json:"content"`         // Text-only content (backward compatible)
	Parts   []ContentPart `json:"parts,omitempty"` // Multimodal content parts (overrides Content when set)
}

// NewMultimodalMessage creates an InferenceMessage with mixed text and image content parts.
func NewMultimodalMessage(role string, parts []ContentPart) InferenceMessage {
	return InferenceMessage{Role: role, Parts: parts}
}

// HasMultimodalContent returns true if this message contains content parts
// (typically text + image_url), indicating it should be serialized as an array
// rather than a plain string.
func (m InferenceMessage) HasMultimodalContent() bool {
	return len(m.Parts) > 0
}

type StructuredInferenceRequest struct {
	Messages    []InferenceMessage
	JSONSchema  string
	StreamMeta  *StreamMeta
	ToolNames   []string
	TaskID      string
	IsLowStakes bool // If true, disable thermal escalation and sticky cloud fallback (ADR-0040)
}

// NewSimpleRequest creates a 2-message request for classification, chat, and other
// simple callers that don't need segmented multi-turn prompts.
func NewSimpleRequest(system, user, schema string) StructuredInferenceRequest {
	return StructuredInferenceRequest{
		Messages: []InferenceMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		JSONSchema: schema,
	}
}

// MessagesToMaps converts InferenceMessages to the OpenAI-compatible message format.
// For text-only messages, content is a string. For multimodal messages, content is
// an array of content parts (text + image_url).
func MessagesToMaps(msgs []InferenceMessage) []map[string]interface{} {
	result := make([]map[string]interface{}, len(msgs))
	for i, m := range msgs {
		if m.HasMultimodalContent() {
			result[i] = map[string]interface{}{"role": m.Role, "content": m.Parts}
		} else {
			result[i] = map[string]interface{}{"role": m.Role, "content": m.Content}
		}
	}
	return result
}

// GetSystemPrompt extracts the first system message content from a Messages slice.
// Returns empty string if no system message is found.
func GetSystemPrompt(msgs []InferenceMessage) string {
	for _, m := range msgs {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

// GetUserPrompt extracts the last user message content from a Messages slice.
// Returns empty string if no user message is found.
func GetUserPrompt(msgs []InferenceMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

type HeuristicIntentResult struct {
	Type       string                 `json:"type"`
	Confidence float64                `json:"confidence"`
	Summary    string                 `json:"summary"`
	Params     map[string]interface{} `json:"params"`
}

// ExecuteStructured encapsulates cooperative routing rules, local heuristics, and HTTP cloud client fallbacks.
func (m *LocalModelManager) ExecuteStructured(ctx context.Context, req StructuredInferenceRequest) (string, error) {

	m.initMaps()
	cfg := config.Get()

	// 1. Determine if we should force cloud for this task
	m.fallbackMutex.RLock()
	forceCloud := m.forceCloudFallback[req.TaskID] && cfg.PrivacyLevel != "strict-local"
	m.fallbackMutex.RUnlock()

	// 2. Cloud-only mode
	if cfg.ModelMode == "cloud" {
		if cfg.PrivacyLevel == "strict-local" {
			return "", fmt.Errorf("cloud execution disabled under strict-local privacy level")
		}
		if req.StreamMeta != nil {
			return CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
		}
		return CallCloudModel(ctx, req.Messages, req.JSONSchema)
	}

	// 3. Local or Cooperative mode
	if cfg.ModelMode == "local" || (cfg.ModelMode == "cooperative" && !forceCloud) {
		status := "stopped"
		if ActiveBackend != nil {
			status = strings.ToLower(ActiveBackend.Status())
		} else {
			mStatus, _, _, _, _ := m.GetStatusInfo()
			status = strings.ToLower(mStatus)
		}

		// If backend is stopped, attempt to start it synchronously
		if status == "stopped" {
			if ActiveBackend != nil {
				fmt.Fprintln(os.Stderr, "[Inference Backend] Backend is stopped. Attempting auto-start...")
				if err := ActiveBackend.Start(ctx); err == nil {
					status = strings.ToLower(ActiveBackend.Status())
				} else {
					fmt.Fprintf(os.Stderr, "[Inference Backend Error] Failed to auto-start: %v\n", err)
				}
			} else {
				fmt.Fprintln(os.Stderr, "[Llama Sidecar] Sidecar is stopped. Attempting auto-start...")
				if err := m.Start(ctx); err == nil {
					mStatus, _, _, _, _ := m.GetStatusInfo()
					status = strings.ToLower(mStatus)
				} else {
					fmt.Fprintf(os.Stderr, "[Llama Sidecar Error] Failed to auto-start: %v\n", err)
				}
			}
		}

		// If backend is in any non-active state (starting, unavailable, or other
		// transient states), block and wait for it to become active/adopted.
		// This closes a gap where transient states silently fell through to the
		// terminal "sidecar is inactive" error without any retry.
		if status != "active" && status != "adopted" && status != "stopped" {
			fmt.Fprintf(os.Stderr, "[Inference Backend] Backend is in transient state %q. Waiting up to 30s for it to become active...\n", status)
			nodeID := ""
			if req.StreamMeta != nil {
				nodeID = req.StreamMeta.NodeID
			}
			telemetry.Default.PublishEvent("inference_wait", req.TaskID, nodeID, fmt.Sprintf("Backend in transient state %q. Waiting for recovery.", status))
			startWait := time.Now()
			for time.Since(startWait) < 30*time.Second {
				time.Sleep(500 * time.Millisecond)
				if ActiveBackend != nil {
					status = strings.ToLower(ActiveBackend.Status())
				} else {
					mStatus, _, _, _, _ := m.GetStatusInfo()
					status = strings.ToLower(mStatus)
				}
				if status == "active" || status == "adopted" {
					fmt.Fprintf(os.Stderr, "[Inference Backend] Backend became active after %v. Proceeding with local execution.\n", time.Since(startWait))
					break
				}
				if status == "stopped" {
					// Transitioned to stopped — break out and let it fall through
					// to heuristics/cloud rather than waiting the full timeout.
					fmt.Fprintln(os.Stderr, "[Inference Backend] Backend transitioned to stopped during wait. Abandoning local execution.")
					break
				}
			}
		}

		// Pre-flight health probe: If status is still not active after all
		// recovery attempts (auto-start, transient wait), probe the actual
		// HTTP /health endpoint. This catches the case where m.Status was
		// pessimistically set to "Stopped" by a transient inference error
		// (e.g., timeout, context cancellation, slot contention) while the
		// sidecar process is still running and healthy.
		if status != "active" && status != "adopted" {
			health := m.ProbeHealth(ctx)
			switch health {
			case SidecarHealthReady:
				// Sidecar is alive and has a free slot — re-adopt.
				m.mutex.Lock()
				m.Status = "Adopted"
				m.mutex.Unlock()
				status = "adopted"

				nodeID := ""
				if req.StreamMeta != nil {
					nodeID = req.StreamMeta.NodeID
				}
				fmt.Fprintf(os.Stderr, "[Inference Routing] Pre-flight probe: sidecar alive and ready on port %d. Re-adopted from stale %q status.\n", m.ActivePort, "Stopped")
				telemetry.Default.PublishEvent("sidecar_readopted", req.TaskID, nodeID, "Pre-flight health probe detected live sidecar with stale Stopped status. Re-adopted.")

			case SidecarHealthBusy:
				// Sidecar is alive but all slots are occupied by another inference.
				// Wait for the current request to finish rather than failing outright.
				nodeID := ""
				if req.StreamMeta != nil {
					nodeID = req.StreamMeta.NodeID
				}
				fmt.Fprintf(os.Stderr, "[Inference Routing] Pre-flight probe: sidecar alive but busy (all slots occupied). Waiting for slot availability...\n")
				telemetry.Default.PublishEvent("sidecar_slot_wait", req.TaskID, nodeID, "Sidecar alive but busy. Waiting for inference slot.")

				startWait := time.Now()
				slotWaitTimeout := 30 * time.Second
				for time.Since(startWait) < slotWaitTimeout {
					time.Sleep(1 * time.Second)
					probeResult := m.ProbeHealth(ctx)
					if probeResult == SidecarHealthReady {
						m.mutex.Lock()
						m.Status = "Adopted"
						m.mutex.Unlock()
						status = "adopted"
						fmt.Fprintf(os.Stderr, "[Inference Routing] Slot became available after %v. Re-adopted.\n", time.Since(startWait).Round(time.Millisecond))
						telemetry.Default.PublishEvent("sidecar_readopted", req.TaskID, nodeID, fmt.Sprintf("Slot wait resolved after %v. Re-adopted.", time.Since(startWait).Round(time.Millisecond)))
						break
					}
					if probeResult == SidecarHealthDead {
						fmt.Fprintf(os.Stderr, "[Inference Routing] Sidecar died during slot wait after %v. Abandoning.\n", time.Since(startWait).Round(time.Millisecond))
						break
					}
					// Still busy — continue waiting
				}
				if status != "adopted" {
					fmt.Fprintf(os.Stderr, "[Inference Routing] Slot wait timed out after %v. Falling through.\n", slotWaitTimeout)
				}

			case SidecarHealthDead:
				// Process is truly dead — leave status as-is, fall through to heuristics/cloud.
			}
		}

		isActive := (status == "active" || status == "adopted")

		if isActive {
			// PRE-FLIGHT: Thermal pressure gating
			nodeID := ""
			if req.StreamMeta != nil {
				nodeID = req.StreamMeta.NodeID
			}

			// ADR-0040: Low-stakes requests (like validators) bypass thermal escalation
			// to avoid burning cloud tokens on simple structured extraction.
			// Remote backends skip thermal checks entirely — local thermal state is
			// irrelevant when inference runs on a different machine.
			isRemoteBackend := cfg.InferenceBackend.Type == "openai-compatible"
			if !req.IsLowStakes && !isRemoteBackend {
				proceed, escalateToCloud := CheckThermalPressure(req.TaskID, nodeID, m)
				if !proceed {
					if escalateToCloud && cfg.ModelMode == "cooperative" {
						// Thermal cloud escalation — route to cloud for this call
						if req.StreamMeta != nil {
							return CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
						}
						return CallCloudModel(ctx, req.Messages, req.JSONSchema)
					}
					// In local-only mode with thermal pressure, we have no alternative.
					// Fall through to attempt local inference anyway (speed floor is the backup).
				}
			}

			var localRes *InferenceResult
			var err error

			if ActiveBackend != nil {
				if req.StreamMeta != nil {
					localRes, err = ActiveBackend.CallModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta)
				} else {
					localRes, err = ActiveBackend.CallModel(ctx, req.Messages, req.JSONSchema)
				}
			} else {
				if req.StreamMeta != nil {
					localRes, err = m.CallLocalModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta)
				} else {
					localRes, err = m.CallLocalModel(ctx, req.Messages, req.JSONSchema)
				}
			}

			if err == nil {
				// Speed floor check using accurate metrics
				if localRes.DurationSeconds > 0 && req.TaskID != "" {
					speedFloor := cfg.SpeedFloor
					if speedFloor <= 0 {
						speedFloor = 5.0
					}

					m.fallbackMutex.Lock()
					if localRes.TokensPerSecond < speedFloor {
						m.consecutiveSpeedFail[req.TaskID]++
						if m.consecutiveSpeedFail[req.TaskID] >= 3 {
							m.forceCloudFallback[req.TaskID] = true
							nodeID := ""
							if req.StreamMeta != nil {
								nodeID = req.StreamMeta.NodeID
							}
							telemetry.Default.PublishEvent("speed_floor_fallback", req.TaskID, nodeID, fmt.Sprintf("Generation speed %.1f t/s dropped below floor %.1f t/s. Activating cloud fallback.", localRes.TokensPerSecond, speedFloor))
						}
					} else {
						m.consecutiveSpeedFail[req.TaskID] = 0
					}
					m.fallbackMutex.Unlock()
				}
				return localRes.Content, nil
			}

			// Escalation to cloud in cooperative mode
			if cfg.ModelMode == "cooperative" && !req.IsLowStakes {
				if cfg.PrivacyLevel == "strict-local" {
					return "", fmt.Errorf("local execution failed: %w (cloud fallback disabled under strict-local privacy level)", err)
				}
				m.fallbackMutex.Lock()
				m.forceCloudFallback[req.TaskID] = true
				m.fallbackMutex.Unlock()

				nodeID := ""
				if req.StreamMeta != nil {
					nodeID = req.StreamMeta.NodeID
				}
				telemetry.Default.PublishEvent("cloud_escalation", req.TaskID, nodeID, fmt.Sprintf("Local model execution failed: %v. Escalating to cloud fallback.", err))
				if req.StreamMeta != nil {
					return CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
				}
				return CallCloudModel(ctx, req.Messages, req.JSONSchema)
			}
			return "", fmt.Errorf("local execution failed: %w", err)
		}
	}

	// 4. Sidecar inactive fallback in cooperative/local mode
	heuristicRes, isHeuristic := m.runHeuristics(req)
	cloudKey := config.GetCloudAPIKey()
	if isHeuristic {
		if m.isInconclusiveHeuristic(req, heuristicRes) && cloudKey != "" {
			var cloudRes string
			var err error
			if req.StreamMeta != nil {
				cloudRes, err = CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
			} else {
				cloudRes, err = CallCloudModel(ctx, req.Messages, req.JSONSchema)
			}
			if err == nil {
				return cloudRes, nil
			}
		}
		return heuristicRes, nil
	}

	// No heuristic, attempt cloud if configured
	if cloudKey != "" {
		if cfg.PrivacyLevel == "strict-local" {
			return "", fmt.Errorf("cloud execution disabled under strict-local privacy level")
		}
		if req.StreamMeta != nil {
			return CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
		}
		return CallCloudModel(ctx, req.Messages, req.JSONSchema)
	}

	return "", fmt.Errorf("local sidecar is inactive and cloud fallback is unavailable")
}

func (m *LocalModelManager) IsForceCloud(taskID string) bool {
	if config.Get().PrivacyLevel == "strict-local" {
		return false
	}
	m.fallbackMutex.RLock()
	defer m.fallbackMutex.RUnlock()
	if m.forceCloudFallback == nil {
		return false
	}
	return m.forceCloudFallback[taskID]
}

func (m *LocalModelManager) initMaps() {
	m.fallbackMutex.Lock()
	defer m.fallbackMutex.Unlock()
	if m.forceCloudFallback == nil {
		m.forceCloudFallback = make(map[string]bool)
	}
	if m.consecutiveSpeedFail == nil {
		m.consecutiveSpeedFail = make(map[string]int)
	}
	if m.thermalCloudEscalationTime == nil {
		m.thermalCloudEscalationTime = make(map[string]time.Time)
	}
}

func (m *LocalModelManager) runHeuristics(req StructuredInferenceRequest) (string, bool) {
	userPrompt := GetUserPrompt(req.Messages)
	if strings.Contains(req.JSONSchema, "confidence") {
		res := classifyHeuristic(userPrompt)
		b, _ := json.Marshal(res)
		return string(b), true
	}
	if strings.Contains(req.JSONSchema, "complexity") {
		tier := heuristicClassify(userPrompt, req.ToolNames)
		if tier != "" {
			return fmt.Sprintf(`{"complexity":"%s"}`, tier), true
		}
		fallbackTier := heuristicClassifyFallback(userPrompt, req.ToolNames)
		return fmt.Sprintf(`{"complexity":"%s"}`, fallbackTier), true
	}
	return "", false
}

func (m *LocalModelManager) isInconclusiveHeuristic(req StructuredInferenceRequest, heuristicVal string) bool {
	if strings.Contains(req.JSONSchema, "confidence") {
		var res HeuristicIntentResult
		if json.Unmarshal([]byte(heuristicVal), &res) == nil {
			return res.Type == "chat"
		}
	}
	return false
}

func classifyHeuristic(prompt string) HeuristicIntentResult {
	lower := strings.ToLower(prompt)

	if strings.Contains(lower, "every") || strings.Contains(lower, "cron") || strings.Contains(lower, "scheduled") {
		return HeuristicIntentResult{
			Type:       "heartbeat",
			Confidence: 0.95,
			Summary:    "Create scheduled heartbeat task",
			Params: map[string]interface{}{
				"name":           "Scheduled Heartbeat Action",
				"cronExpression": "*/5 * * * *",
				"prompt":         prompt,
				"taskType":       "prompt_tool",
			},
		}
	}

	if strings.Contains(lower, "research") || strings.Contains(lower, "analyze") || strings.Contains(lower, "find information") {
		return HeuristicIntentResult{
			Type:       "research",
			Confidence: 0.90,
			Summary:    "Execute multi-source deep research search",
			Params: map[string]interface{}{
				"query": prompt,
				"depth": "standard",
			},
		}
	}

	if strings.Contains(lower, "bulk") || strings.Contains(lower, "migrate") || strings.Contains(lower, "sync") || strings.Contains(lower, "for each") {
		return HeuristicIntentResult{
			Type:       "workflow",
			Confidence: 0.92,
			Summary:    "Spawn compiled Multi-Agent workflow orchestration",
			Params: map[string]interface{}{
				"name":      "System Migration Flow",
				"goal":      prompt,
				"objective": "Orchestrate dynamic parallel DAG steps.",
			},
		}
	}

	if strings.Contains(lower, "mission") || strings.Contains(lower, "campaign") {
		return HeuristicIntentResult{
			Type:       "mission",
			Confidence: 0.85,
			Summary:    "Initiate persistent multi-week coordination goal",
			Params: map[string]interface{}{
				"name": "Enterprise Automation Campaign",
				"goal": prompt,
			},
		}
	}

	return HeuristicIntentResult{
		Type:       "chat",
		Confidence: 0.99,
		Summary:    "Standard conversational messaging lookup",
		Params: map[string]interface{}{
			"title":        "Conversational Query",
			"firstMessage": prompt,
		},
	}
}

func heuristicClassify(requestText string, toolNames []string) string {
	lower := strings.ToLower(strings.TrimSpace(requestText))
	words := strings.Fields(lower)

	if len(words) <= 2 {
		return "T0"
	}

	t1Patterns := []string{
		"delete all", "update all", "bulk ", "for each", "migrate ",
		"find all", "export all", "import all", "and then", "after that",
	}
	for _, p := range t1Patterns {
		if strings.Contains(lower, p) {
			return ""
		}
	}

	referencesTool := false
	for _, tn := range toolNames {
		normalized := strings.ReplaceAll(strings.ToLower(tn), "_", " ")
		if strings.Contains(lower, normalized) {
			referencesTool = true
			break
		}
	}

	t0Prefixes := []string{
		"tell me", "what is", "explain", "describe", "hello", "write ", "create a ",
	}
	for _, prefix := range t0Prefixes {
		if strings.HasPrefix(lower, prefix) && !referencesTool {
			return "T0"
		}
	}

	return ""
}

func heuristicClassifyFallback(requestText string, toolNames []string) string {
	lower := strings.ToLower(strings.TrimSpace(requestText))
	words := strings.Fields(lower)

	if len(words) <= 2 {
		return "T0"
	}

	t1Patterns := []string{
		"delete all", "update all", "bulk", "for each", "migrate", "sync",
		"find all", "export all", "import all", "and then", "after that",
	}
	for _, p := range t1Patterns {
		if strings.Contains(lower, p) {
			return "T1"
		}
	}

	if strings.Contains(lower, "delete all") || strings.Contains(lower, "force override") || strings.Contains(lower, "purge") {
		return "T2"
	}

	for _, name := range toolNames {
		normalized := strings.ReplaceAll(strings.ToLower(name), "_", " ")
		if strings.Contains(lower, normalized) {
			return "T1"
		}
	}

	return "T0"
}
