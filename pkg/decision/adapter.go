package decision

import (
	"context"

	"tzro/pkg/executor"
)

// DeciderAdapter bridges a DecisionProvider to the executor.Decider interface,
// allowing the graph execution engine to evaluate System 1 decisions.
type DeciderAdapter struct {
	provider DecisionProvider
	squasher *StateSquasher
}

// NewDeciderAdapter creates an adapter with the specified provider and squasher.
func NewDeciderAdapter(provider DecisionProvider, squasher *StateSquasher) *DeciderAdapter {
	if squasher == nil {
		squasher = NewStateSquasher(2048)
	}
	return &DeciderAdapter{
		provider: provider,
		squasher: squasher,
	}
}

// Decide implements executor.Decider.
func (a *DeciderAdapter) Decide(ctx context.Context, in *executor.DecisionInput) (*executor.DecisionOutput, error) {
	state := in.State
	if state != nil {
		compacted, _, err := a.squasher.CompactState(state)
		if err != nil {
			return nil, err
		}
		state = compacted
	}

	resp, err := a.provider.Evaluate(ctx, &DecisionRequest{
		QuestionType: QuestionType(in.QuestionType),
		Prompt:       in.Prompt,
		Options:      in.Options,
		State:        state,
	})
	if err != nil {
		return nil, err
	}

	return &executor.DecisionOutput{
		Answer:     resp.Answer,
		Confidence: resp.Confidence,
		Scores:     resp.Scores,
	}, nil
}
