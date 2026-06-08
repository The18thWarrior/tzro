package mock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"tzro/internal/compiler"
)

type MockToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type MockExpectedCall struct {
	ToolName string                 `json:"tool_name"`
	Args     map[string]interface{} `json:"args"`
}

type MockBenchmarkTurn struct {
	UserMessage      string                 `json:"user_message"`
	ExpectedCalls    []MockExpectedCall     `json:"expected_calls,omitempty"`
	ExpectedToolCall string                 `json:"expected_tool_call,omitempty"`
	ExpectedArgs     map[string]interface{} `json:"expected_args,omitempty"`
	MockResponse     string                 `json:"mock_response"`
}

type MockExpectedGraphNode struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Action       string   `json:"action"`
	Instructions string   `json:"instructions"`
	AllowedTools []string `json:"allowedTools"`
	Status       string   `json:"status"`
}

type MockExpectedGraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type MockExpectedGraph struct {
	TaskID string                  `json:"taskId"`
	Nodes  []MockExpectedGraphNode `json:"nodes"`
	Edges  []MockExpectedGraphEdge `json:"edges"`
}

type MockTestCase struct {
	ID            string                 `json:"id"`
	Dataset       string                 `json:"dataset"`
	SystemPrompt  string                 `json:"system_prompt"`
	Tools         []MockToolDefinition   `json:"tools"`
	Turns         []MockBenchmarkTurn    `json:"turns"`
	ExpectedGraph MockExpectedGraph      `json:"expected_graph,omitempty"`
	InitialConfig map[string]interface{} `json:"initial_config,omitempty"`
}

type MockRoundTripper struct {
	TargetURL     string
	RealTransport http.RoundTripper
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	targetReq, err := http.NewRequest(req.Method, m.TargetURL, req.Body)
	if err != nil {
		return nil, err
	}
	targetReq.Header = req.Header
	return m.RealTransport.RoundTrip(targetReq)
}

// Runner orchestrates the completions HTTP interceptor and mock server.
type Runner struct {
	MockServer *httptest.Server
	Transport  *MockRoundTripper
}

// StartMockServer starts the completions HTTP interceptor loop.
func (r *Runner) StartMockServer(tcJSON []byte, mode string) {
	var tc MockTestCase
	if err := json.Unmarshal(tcJSON, &tc); err != nil {
		// Fallback empty testcase
		tc = MockTestCase{}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var compReq struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(bodyBytes, &compReq)

		var systemPrompt, userPrompt string
		var allContent strings.Builder // ADR-0021: segmented prompts spread content across multiple messages
		for _, m := range compReq.Messages {
			allContent.WriteString(m.Content)
			allContent.WriteString(" ")
			if m.Role == "system" {
				systemPrompt = m.Content
			} else if m.Role == "user" {
				userPrompt = m.Content
			}
		}
		allMessages := allContent.String()

		// Protocol Envelope Handlers
		writeSSE := func(content string) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			chunk.Choices = []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			}{
				{
					Delta: struct {
						Content string `json:"content"`
					}{
						Content: content,
					},
				},
			}
			b, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))

			var usageChunk struct {
				Choices []interface{} `json:"choices"`
				Usage   struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			usageChunk.Choices = []interface{}{}
			usageChunk.Usage.PromptTokens = 120
			usageChunk.Usage.CompletionTokens = 45
			ub, _ := json.Marshal(usageChunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(ub))

			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		}

		writeJSON := func(content string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`{
				"choices": [{
					"message": {
						"content": %q
					}
				}],
				"usage": {
					"prompt_tokens": 120,
					"completion_tokens": 45
				}
			}`, content)))
		}

		// 1. Is this a DAG Planning Request?
		if strings.Contains(systemPrompt, "Strategic Planner") {
			var nodes []compiler.GraphNode
			var edges []compiler.GraphEdge

			if len(tc.ExpectedGraph.Nodes) > 0 {
				for _, n := range tc.ExpectedGraph.Nodes {
					nodes = append(nodes, compiler.GraphNode{
						ID:           n.ID,
						Type:         n.Type,
						Action:       n.Action,
						Instructions: n.Instructions,
						AllowedTools: n.AllowedTools,
						Status:       n.Status,
					})
				}
				for _, e := range tc.ExpectedGraph.Edges {
					edges = append(edges, compiler.GraphEdge{
						SourceID: e.SourceID,
						TargetID: e.TargetID,
					})
				}
			} else if mode == "consolidated" {
				// Consolidated DAG plans the entire sequence of turns with intermediate variable bindings
				for i, turn := range tc.Turns {
					if turn.ExpectedToolCall == "" {
						continue
					}
					nodeID := fmt.Sprintf("node_%d", i+1)
					instr := turn.UserMessage
					// Perform variable linking substitution for turns 2+ to simulate planned pipeline optimization
					if i == 1 {
						if tc.Dataset == "bfcl" {
							instr = "Great! Please book that flight {{nodes.node_1.output.flight_id}} for passenger John Doe."
						} else if tc.Dataset == "complexfuncbench" {
							instr = "Great, book the hotel with ID {{nodes.node_1.output.hotel_id}}."
						}
					} else if i == 2 {
						if tc.Dataset == "bfcl" {
							instr = "Now check the ticket status for {{nodes.node_2.output.ticket_id}}."
						} else if tc.Dataset == "complexfuncbench" {
							instr = "Now book a taxi to The Savoy at 6 PM." // Static or dynamic
						}
					}

					nodes = append(nodes, compiler.GraphNode{
						ID:           nodeID,
						Type:         "action",
						Action:       turn.ExpectedToolCall,
						Instructions: instr,
						AllowedTools: []string{turn.ExpectedToolCall},
						Status:       "pending",
					})

					if len(nodes) > 1 {
						edges = append(edges, compiler.GraphEdge{
							SourceID: nodes[len(nodes)-2].ID,
							TargetID: nodeID,
						})
					}
				}
			} else {
				// Interactive mode maps ONLY the current turn's active sub-task (max 10 nodes, here modeled as 1 primary step)
				// We identify which turn this prompt represents using the deterministic turn index suffix in the systemPrompt TaskID
				activeTurn := MockBenchmarkTurn{}
				if len(tc.Turns) > 0 {
					activeTurn = tc.Turns[0]
				}
				foundTurn := false
				for idx, turn := range tc.Turns {
					expectedSuffix := fmt.Sprintf("%s_t%d", tc.ID, idx)
					if strings.Contains(allMessages, expectedSuffix) {
						activeTurn = turn
						foundTurn = true
						break
					}
				}
				if !foundTurn {
					// Fallback to full-string message matching if suffix is missing
					for _, turn := range tc.Turns {
						if strings.Contains(userPrompt, turn.UserMessage) {
							activeTurn = turn
							foundTurn = true
							break
						}
					}
				}

				if activeTurn.ExpectedToolCall != "" {
					nodes = append(nodes, compiler.GraphNode{
						ID:           "interactive_node",
						Type:         "action",
						Action:       activeTurn.ExpectedToolCall,
						Instructions: userPrompt,
						AllowedTools: []string{activeTurn.ExpectedToolCall},
						Status:       "pending",
					})
				}
			}

			graph := compiler.ExecutionGraph{
				TaskID:    fmt.Sprintf("task_%s", tc.ID),
				Nodes:     nodes,
				Edges:     edges,
				MaxCycles: 5,
				CreatedAt: time.Now().Unix(),
			}
			graphBytes, _ := json.Marshal(graph)
			if compReq.Stream {
				writeSSE(string(graphBytes))
			} else {
				writeJSON(string(graphBytes))
			}
			return
		}

		// 2. Is this a Local Tactician Node Executor parameter mapping request?
		// ADR-0021: With segmented prompts, "Local Tactician" is in the static system prompt,
		// but tool names appear in the last user message (instruction segment with schema).
		// We use userPrompt (the last user message) for tool matching to avoid false matches
		// against tool names that appear in accumulated context from prior nodes.
		if strings.Contains(allMessages, "Local Tactician") {
			// Determine expected tool call parameters
			var expectedArgs map[string]interface{}
			found := false

			// Match based on tool Action name in the instruction segment (last user message)
			for _, turn := range tc.Turns {
				if len(turn.ExpectedCalls) > 0 {
					for _, ec := range turn.ExpectedCalls {
						if strings.Contains(userPrompt, ec.ToolName) {
							expectedArgs = ec.Args
							found = true
							break
						}
					}
					if found {
						break
					}
				} else if turn.ExpectedToolCall != "" && strings.Contains(userPrompt, turn.ExpectedToolCall) {
					expectedArgs = turn.ExpectedArgs
					found = true
					break
				}
			}

			if !found && len(tc.Turns) > 0 {
				if len(tc.Turns[0].ExpectedCalls) > 0 {
					expectedArgs = tc.Turns[0].ExpectedCalls[0].Args
				} else {
					expectedArgs = tc.Turns[0].ExpectedArgs
				}
			}

			// Format expected_args inside tool_arguments GBNF compliance wrapper
			// In expected_calls standard, arguments inside ExpectedCalls.Args are wrapped in arrays representing options (e.g. `["USD", "usd"]`).
			// Let's unwrap them to single values (preferring the first item in the array or the value itself) to return standard JSON to the parser.
			unwrappedArgs := make(map[string]interface{})
			for k, v := range expectedArgs {
				if arr, ok := v.([]interface{}); ok && len(arr) > 0 {
					unwrappedArgs[k] = arr[0]
				} else {
					unwrappedArgs[k] = v
				}
			}

			respBody := map[string]interface{}{
				"tool_arguments": unwrappedArgs,
			}
			respBytes, _ := json.Marshal(respBody)

			if compReq.Stream {
				writeSSE(string(respBytes))
			} else {
				writeJSON(string(respBytes))
			}
			return
		}

		// Fallback empty response
		if compReq.Stream {
			writeSSE("{}")
		} else {
			writeJSON("{}")
		}
	})

	r.MockServer = httptest.NewServer(handler)
	r.Transport = &MockRoundTripper{
		TargetURL:     r.MockServer.URL,
		RealTransport: http.DefaultTransport,
	}
	http.DefaultTransport = r.Transport
}

// StopMockServer teardown and restores native transports.
func (r *Runner) StopMockServer() {
	if r.MockServer != nil {
		r.MockServer.Close()
	}
	if r.Transport != nil {
		http.DefaultTransport = r.Transport.RealTransport
	}
}
