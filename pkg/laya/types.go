package laya

// DecisionRequest is sent to the laya daemon over JSON-RPC.
type DecisionRequest struct {
	QuestionType string                 `json:"question_type"` // "noul", "choice", "score"
	Prompt       string                 `json:"prompt"`
	Options      []string               `json:"options,omitempty"`
	State        map[string]interface{} `json:"state"`
}

// DecisionResponse is the daemon's answer.
type DecisionResponse struct {
	Answer     string             `json:"answer"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
	LatencyMs  int64              `json:"latency_ms"`
}
