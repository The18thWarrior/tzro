package invocation

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestParse_CountsModelInvocationsIndependentlyOfTrajectorySteps(t *testing.T) {
	events := sequence()
	var data bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&data).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := Parse(&data, "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Complete || summary.Started != 2 || summary.Completed != 2 || summary.ModelIntervalSeconds != 3 {
		t.Fatalf("expected two responses despite multiple trajectory steps: %+v", summary)
	}
}

func sequence() []Event {
	zero, one, steps, idle := 0, 1, 12, true
	return []Event{
		{Schema: "tzro.native-invocation.v1", Kind: "PreInvocation", Conversation: "conversation-1", Invocation: &zero, InitialSteps: &one, TimestampNS: 1e9},
		{Schema: "tzro.native-invocation.v1", Kind: "PostInvocation", Conversation: "conversation-1", Invocation: &zero, InitialSteps: &one, TimestampNS: 2e9},
		{Schema: "tzro.native-invocation.v1", Kind: "PreInvocation", Conversation: "conversation-1", Invocation: &one, InitialSteps: &steps, TimestampNS: 3e9},
		{Schema: "tzro.native-invocation.v1", Kind: "PostInvocation", Conversation: "conversation-1", Invocation: &one, InitialSteps: &steps, TimestampNS: 5e9},
		{Schema: "tzro.native-invocation.v1", Kind: "Stop", Conversation: "conversation-1", Execution: &one, FullyIdle: &idle, Termination: "model_stop", TimestampNS: 6e9},
	}
}

func TestParse_RejectsUnreliableEvidence(t *testing.T) {
	wrong := 4
	cases := map[string]func([]Event) []Event{
		"missing stop":       func(e []Event) []Event { return e[:4] },
		"missing completion": func(e []Event) []Event { return append(e[:3], e[4]) },
		"duplicate invocation": func(e []Event) []Event {
			e[2].Invocation = e[0].Invocation
			e[3].Invocation = e[0].Invocation
			return e
		},
		"wrong completion":     func(e []Event) []Event { e[1].Invocation = &wrong; return e },
		"gap":                  func(e []Event) []Event { e[2].Invocation = &wrong; e[3].Invocation = &wrong; return e },
		"missing counter":      func(e []Event) []Event { e[0].Invocation = nil; return e },
		"foreign conversation": func(e []Event) []Event { e[1].Conversation = "other"; return e },
		"clock reversal":       func(e []Event) []Event { e[1].TimestampNS = 1; return e },
		"duplicate stop":       func(e []Event) []Event { return append(e, e[4]) },
		"error termination":    func(e []Event) []Event { e[4].HasError = true; return e },
		"unknown schema":       func(e []Event) []Event { e[0].Schema = "unknown"; return e },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var input bytes.Buffer
			for _, event := range change(sequence()) {
				_ = json.NewEncoder(&input).Encode(event)
			}
			got, err := Parse(&input, "conversation-1")
			if err == nil || got.Complete {
				t.Fatalf("unreliable evidence accepted: %+v %v", got, err)
			}
		})
	}
}
