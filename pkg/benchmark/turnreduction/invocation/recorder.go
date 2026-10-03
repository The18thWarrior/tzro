// Package invocation records native model boundaries without changing the trajectory.
package invocation

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Event contains only allowlisted native hook metadata. Hook text is never retained.
type Event struct {
	Schema       string `json:"schema"`
	Kind         string `json:"event"`
	TimestampNS  int64  `json:"timestamp_ns"`
	Conversation string `json:"conversation_id"`
	Model        string `json:"model,omitempty"`
	Invocation   *int   `json:"invocation_num,omitempty"`
	InitialSteps *int   `json:"initial_num_steps,omitempty"`
	Execution    *int   `json:"execution_num,omitempty"`
	Termination  string `json:"termination_reason,omitempty"`
	FullyIdle    *bool  `json:"fully_idle,omitempty"`
	HasError     bool   `json:"has_error,omitempty"`
	Invalid      string `json:"invalid,omitempty"`
}

// Record always returns an empty hook response. Recording failures are returned
// to the launcher and cannot become an instruction, permission, or injected step.
func Record(kind, path string, input io.Reader, output io.Writer) (recordErr error) {
	defer func() {
		if _, err := io.WriteString(output, "{}\n"); recordErr == nil {
			recordErr = err
		}
	}()
	event := Event{Schema: "tzro.native-invocation.v1", Kind: kind, TimestampNS: time.Now().UnixNano()}
	var payload struct {
		Conversation string `json:"conversationId"`
		Model        string `json:"modelName"`
		Invocation   *int   `json:"invocationNum"`
		InitialSteps *int   `json:"initialNumSteps"`
		Execution    *int   `json:"executionNum"`
		Termination  string `json:"terminationReason"`
		FullyIdle    *bool  `json:"fullyIdle"`
		Error        string `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(input, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		event.Invalid = "invalid hook JSON"
	} else if decoder.Decode(new(any)) != io.EOF {
		event.Invalid = "unexpected trailing hook data"
	}
	event.Conversation, event.Model = payload.Conversation, payload.Model
	event.Invocation, event.InitialSteps = payload.Invocation, payload.InitialSteps
	event.Execution, event.Termination, event.FullyIdle = payload.Execution, payload.Termination, payload.FullyIdle
	event.HasError = payload.Error != ""
	if event.Conversation == "" {
		event.Invalid = "missing conversation identity"
	}
	switch kind {
	case "PreInvocation", "PostInvocation":
		if event.Invocation == nil || *event.Invocation < 0 || event.InitialSteps == nil || *event.InitialSteps < 0 {
			event.Invalid = "missing or invalid invocation metadata"
		}
	case "Stop":
		if event.Execution == nil || event.FullyIdle == nil || event.Termination == "" {
			event.Invalid = "missing termination metadata"
		}
	default:
		event.Invalid = "unsupported hook event"
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lock(file); err != nil {
		return err
	}
	defer unlock(file)
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if event.Invalid != "" {
		return fmt.Errorf("%s", event.Invalid)
	}
	return nil
}
