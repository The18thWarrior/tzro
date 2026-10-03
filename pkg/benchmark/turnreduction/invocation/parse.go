package invocation

import (
	"encoding/json"
	"fmt"
	"io"
)

type Summary struct {
	Problem              string    `json:"problem,omitempty"`
	Complete             bool      `json:"complete"`
	Started              int       `json:"model_invocations_started"`
	Completed            int       `json:"model_invocations_completed"`
	ModelIntervalSeconds float64   `json:"model_interval_s"`
	Intervals            []float64 `json:"invocation_intervals_s,omitempty"`
	Method               string    `json:"method"`
}

// Parse measures native high-level model invocations, not provider HTTP retries.
// Intervals include hook dispatch and client work around the model call.
func Parse(input io.Reader, conversation string) (Summary, error) {
	result := Summary{Method: "matched native PreInvocation/PostInvocation with terminal Stop"}
	decoder := json.NewDecoder(input)
	var pending *Event
	stopped := false
	var lastTimestamp int64
	for {
		var event Event
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, fmt.Errorf("invalid invocation evidence: %w", err)
		}
		if event.Conversation != conversation || conversation == "" || event.Invalid != "" || event.Schema != "tzro.native-invocation.v1" || event.HasError {
			return result, fmt.Errorf("invalid invocation identity or payload")
		}
		if stopped || event.TimestampNS <= 0 || event.TimestampNS < lastTimestamp {
			return result, fmt.Errorf("invocation events out of order or after Stop")
		}
		lastTimestamp = event.TimestampNS
		if event.Kind == "PreInvocation" || event.Kind == "PostInvocation" {
			if event.Invocation == nil || *event.Invocation != result.Completed || event.InitialSteps == nil || *event.InitialSteps < 0 {
				return result, fmt.Errorf("missing, duplicate, or nonconsecutive invocation number")
			}
		}
		switch event.Kind {
		case "PreInvocation":
			if pending != nil {
				return result, fmt.Errorf("invocation started before previous completion")
			}
			result.Started++
			pending = &event
		case "PostInvocation":
			if pending == nil {
				return result, fmt.Errorf("unmatched model completion")
			}
			seconds := float64(event.TimestampNS-pending.TimestampNS) / 1e9
			result.Intervals = append(result.Intervals, seconds)
			result.ModelIntervalSeconds += seconds
			result.Completed++
			pending = nil
		case "Stop":
			if pending != nil || event.Execution == nil || *event.Execution < 0 || event.Termination == "" || event.FullyIdle == nil || !*event.FullyIdle {
				return result, fmt.Errorf("incomplete terminal Stop")
			}
			stopped = true
		default:
			return result, fmt.Errorf("unsupported invocation event")
		}
	}
	result.Complete = stopped && result.Started > 0 && result.Started == result.Completed && pending == nil
	if !result.Complete {
		return result, fmt.Errorf("incomplete native invocation sequence")
	}
	return result, nil
}
