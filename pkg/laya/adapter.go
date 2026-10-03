package laya

import (
	"context"

	"tzro/pkg/executor"
)

// LayaDeciderAdapter bridges the laya.DaemonClient to the executor.Decider interface,
// allowing the graph execution engine to use the Laya daemon for System 1 decisions.
type LayaDeciderAdapter struct {
	client    *DaemonClient
	assembler *StateAssembler
}

// NewLayaDeciderAdapter wraps a DaemonClient to satisfy executor.Decider.
// State is compacted via StateAssembler to fit within the daemon's 450-token context window.
func NewLayaDeciderAdapter(client *DaemonClient) *LayaDeciderAdapter {
	return &LayaDeciderAdapter{
		client:    client,
		assembler: NewStateAssembler(0), // default 450 tokens
	}
}

// Decide delegates to the DaemonClient and converts between
// laya and executor decision types. State is compacted before dispatch.
func (l *LayaDeciderAdapter) Decide(ctx context.Context, req *executor.DecisionInput) (*executor.DecisionOutput, error) {
	// Compact state to fit within ModernBERT's context window
	state := req.State
	if state != nil {
		compacted, _, err := l.assembler.CompactState(state)
		if err != nil {
			return nil, err
		}
		state = compacted
	}

	resp, err := l.client.Evaluate(ctx, &DecisionRequest{
		QuestionType: req.QuestionType,
		Prompt:       req.Prompt,
		Options:      req.Options,
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
