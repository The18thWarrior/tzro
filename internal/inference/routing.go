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

// InferenceMessage represents a single message in a multi-turn conversation.
type InferenceMessage struct {
	Role    string `json:"role"`    // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type StructuredInferenceRequest struct {
	Messages   []InferenceMessage
	JSONSchema string
	StreamMeta *StreamMeta
	ToolNames  []string
	TaskID     string
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

// MessagesToMaps converts InferenceMessages to []map[string]string for HTTP request bodies.
func MessagesToMaps(msgs []InferenceMessage) []map[string]string {
	result := make([]map[string]string, len(msgs))
	for i, m := range msgs {
		result[i] = map[string]string{"role": m.Role, "content": m.Content}
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
	forceCloud := m.forceCloudFallback[req.TaskID]
	m.fallbackMutex.RUnlock()

	// 2. Cloud-only mode
	if cfg.ModelMode == "cloud" {
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

		// If backend is starting, block and wait for it to become active/adopted
		if status == "starting" {
			fmt.Fprintln(os.Stderr, "[Inference Backend] Backend is currently starting. Waiting for it to become active...")
			startWait := time.Now()
			for time.Since(startWait) < 60*time.Second {
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
			}
		}

		isActive := (status == "active" || status == "adopted")

		if isActive {
			// PRE-FLIGHT: Thermal pressure gating
			nodeID := ""
			if req.StreamMeta != nil {
				nodeID = req.StreamMeta.NodeID
			}
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
			if cfg.ModelMode == "cooperative" {
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
		if req.StreamMeta != nil {
			return CallCloudModelStream(ctx, req.Messages, req.JSONSchema, *req.StreamMeta, m.getPublisher())
		}
		return CallCloudModel(ctx, req.Messages, req.JSONSchema)
	}

	return "", fmt.Errorf("local sidecar is inactive and cloud fallback is unavailable")
}

func (m *LocalModelManager) IsForceCloud(taskID string) bool {
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
