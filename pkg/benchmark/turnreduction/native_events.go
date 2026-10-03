package turnreduction

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"tzro/pkg/benchmark/turnreduction/invocation"
)

// NativeEvents only derives measurements supported by the native event schema.
// User turns and streaming step updates are not Cloud Decision Rounds.
type NativeEvents struct {
	AgentResponseSteps  int                 `json:"agent_response_steps,omitempty"`
	ConversationID      string              `json:"conversation_id,omitempty"`
	Invocations         *invocation.Summary `json:"native_invocations,omitempty"`
	Model               string              `json:"model"`
	Status              string              `json:"status"`
	PermissionMode      string              `json:"permission_mode"`
	Tools               []string            `json:"available_tools"`
	ToolCalls           map[string]int      `json:"tool_calls"`
	Usage               map[string]int64    `json:"usage"`
	CloudDecisionRounds *int                `json:"cloud_decision_rounds"`
	ProviderRequests    *int                `json:"provider_requests"`
	ChargedUSD          *float64            `json:"charged_usd"`
}

// ParseNativeEvents deduplicates completed tool steps and uses terminal usage.
func ParseNativeEvents(r io.Reader) (NativeEvents, error) {
	result := NativeEvents{ToolCalls: map[string]int{}}
	seen := map[string]bool{}
	initialized, finished := false, false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var event struct {
			Event          string `json:"event"`
			ConversationID string `json:"conversation_id"`
			Init           struct {
				Model      string   `json:"model"`
				Permission string   `json:"permission_mode"`
				Tools      []string `json:"tools"`
			} `json:"init"`
			Step struct {
				Conversation string `json:"conversation_id"`
				Index        int    `json:"step_index"`
				State        string `json:"state"`
				Type         string `json:"step_type"`
				Name         string `json:"tool_name"`
			} `json:"step_update"`
			Result struct {
				ConversationID string           `json:"conversation_id"`
				Status         string           `json:"status"`
				Usage          map[string]int64 `json:"usage"`
			} `json:"result"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return result, fmt.Errorf("invalid native event: %w", err)
		}
		if finished {
			return result, fmt.Errorf("native event after terminal result")
		}
		if !initialized && event.Event != "init" {
			return result, fmt.Errorf("native event before init")
		}
		switch event.Event {
		case "init":
			if initialized {
				return result, fmt.Errorf("duplicate native init")
			}
			initialized = true
			result.ConversationID = event.ConversationID
			result.Model = event.Init.Model
			result.PermissionMode = event.Init.Permission
			result.Tools = event.Init.Tools
		case "step_update":
			if event.Step.Type == "agent_response" && event.Step.State == "DONE" {
				if result.ConversationID == "" || event.Step.Conversation != result.ConversationID {
					return result, fmt.Errorf("native response lacks matching conversation identity")
				}
				key := fmt.Sprintf("response/%s/%d", event.Step.Conversation, event.Step.Index)
				if !seen[key] {
					result.AgentResponseSteps++
					seen[key] = true
				}
			}
			if event.Step.Type == "tool" && event.Step.State == "DONE" {
				key := fmt.Sprintf("%s/%d", event.Step.Conversation, event.Step.Index)
				if !seen[key] {
					result.ToolCalls[event.Step.Name]++
					seen[key] = true
				}
			}
		case "result":
			if result.ConversationID != "" && event.Result.ConversationID != result.ConversationID {
				return result, fmt.Errorf("native terminal conversation does not match init")
			}
			if finished {
				return result, fmt.Errorf("duplicate native result")
			}
			finished = true
			result.Status = event.Result.Status
			result.Usage = event.Result.Usage
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if !initialized || !finished {
		return result, fmt.Errorf("native stream missing init or terminal result")
	}
	return result, nil
}
