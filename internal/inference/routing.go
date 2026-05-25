package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tzro/internal/config"
	"tzro/internal/telemetry"
)

type StructuredInferenceRequest struct {
	SystemPrompt string
	UserPrompt   string
	JSONSchema   string
	StreamMeta   *StreamMeta
	ToolNames    []string
	TaskID       string
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
			return CallCloudModelStream(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema, *req.StreamMeta, m.getPublisher())
		}
		return CallCloudModel(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema)
	}

	// 3. Local or Cooperative mode
	if cfg.ModelMode == "local" || (cfg.ModelMode == "cooperative" && !forceCloud) {
		status, _, _, _, _ := m.GetStatusInfo()
		isSidecarActive := (status == "Active" || status == "Adopted")

		if isSidecarActive {
			var localRes *InferenceResult
			var err error

			if req.StreamMeta != nil {
				localRes, err = m.CallLocalModelStream(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema, *req.StreamMeta)
			} else {
				localRes, err = m.CallLocalModel(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema)
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
					return CallCloudModelStream(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema, *req.StreamMeta, m.getPublisher())
				}
				return CallCloudModel(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema)
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
				cloudRes, err = CallCloudModelStream(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema, *req.StreamMeta, m.getPublisher())
			} else {
				cloudRes, err = CallCloudModel(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema)
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
			return CallCloudModelStream(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema, *req.StreamMeta, m.getPublisher())
		}
		return CallCloudModel(ctx, req.SystemPrompt, req.UserPrompt, req.JSONSchema)
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
}

func (m *LocalModelManager) runHeuristics(req StructuredInferenceRequest) (string, bool) {
	if strings.Contains(req.JSONSchema, "confidence") {
		res := classifyHeuristic(req.UserPrompt)
		b, _ := json.Marshal(res)
		return string(b), true
	}
	if strings.Contains(req.JSONSchema, "complexity") {
		tier := heuristicClassify(req.UserPrompt, req.ToolNames)
		if tier != "" {
			return fmt.Sprintf(`{"complexity":"%s"}`, tier), true
		}
		fallbackTier := heuristicClassifyFallback(req.UserPrompt, req.ToolNames)
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
