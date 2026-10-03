package decision

import "context"

// QuestionType represents the category of decision requested from the model.
type QuestionType string

const (
	QuestionTypeChoice QuestionType = "choice"
	QuestionTypeNoul   QuestionType = "noul"
	QuestionTypeScore  QuestionType = "score"
)

// DecisionRequest is sent to the decision provider (local or remote).
type DecisionRequest struct {
	QuestionType QuestionType           `json:"question_type"` // "choice", "noul", "score"
	Prompt       string                 `json:"prompt"`
	Options      []string               `json:"options,omitempty"`
	State        map[string]interface{} `json:"state"`
	Category     string                 `json:"category,omitempty"`
}

// DecisionResponse is the calibrated result from the decision engine.
type DecisionResponse struct {
	Answer     string             `json:"answer"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
	LatencyMs  int64              `json:"latency_ms"`
	Error      string             `json:"error,omitempty"`
}

// DecisionProvider abstracts local daemon vs remote HTTP decision engines.
type DecisionProvider interface {
	Evaluate(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error)
	Close() error
}
