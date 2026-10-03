package turnreduction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNativeEvents_DeduplicatesToolsAndUsesTerminalUsage(t *testing.T) {
	stream := `{"event":"init","init":{"model":"gemini-3.8-flash-low"}}
{"event":"step_update","step_update":{"conversation_id":"x","step_index":3,"state":"ACTIVE","step_type":"tool","tool_name":"tzro_edit_and_verify","usage":{"total_tokens":999}}}
{"event":"step_update","step_update":{"conversation_id":"x","step_index":3,"state":"DONE","step_type":"tool","tool_name":"tzro_edit_and_verify","usage":{"total_tokens":999}}}
{"event":"step_update","step_update":{"conversation_id":"x","step_index":3,"state":"DONE","step_type":"tool","tool_name":"tzro_edit_and_verify"}}
{"event":"result","result":{"status":"SUCCESS","num_turns":1,"usage":{"total_tokens":15}}}
`
	got, err := ParseNativeEvents(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolCalls["tzro_edit_and_verify"] != 1 || got.Usage["total_tokens"] != 15 {
		t.Fatalf("incorrect native accounting: %+v", got)
	}
	if got.CloudDecisionRounds != nil || got.ProviderRequests != nil || got.ChargedUSD != nil {
		t.Fatal("unobserved metrics must remain unknown")
	}
}

func TestParseNativeEvents_RejectsIncompleteOrContradictoryStreams(t *testing.T) {
	init := `{"event":"init","init":{"model":"gemini-3.8-flash-low"}}` + "\n"
	result := `{"event":"result","result":{"status":"SUCCESS"}}` + "\n"
	tool := `{"event":"step_update","step_update":{"state":"DONE","step_type":"tool","tool_name":"run_command","step_index":2}}` + "\n"
	for name, stream := range map[string]string{"missing terminal": init, "missing init": result, "duplicate terminal": init + result + result, "late tool": init + result + tool, "early tool": tool + init + result, "late init": result + init} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseNativeEvents(strings.NewReader(stream)); err == nil {
				t.Fatal("invalid native stream accepted")
			}
		})
	}
}

func TestObserveInvocations_RequiresMatchingNativeResponses(t *testing.T) {
	dir := t.TempDir()
	trace := `{"schema":"tzro.native-invocation.v1","event":"PreInvocation","conversation_id":"main","invocation_num":0,"initial_num_steps":1,"timestamp_ns":1000000000}
{"schema":"tzro.native-invocation.v1","event":"PostInvocation","conversation_id":"main","invocation_num":0,"initial_num_steps":1,"timestamp_ns":2000000000}
{"schema":"tzro.native-invocation.v1","event":"Stop","conversation_id":"main","execution_num":1,"fully_idle":true,"termination_reason":"model_stop","timestamp_ns":3000000000}
`
	if err := os.WriteFile(filepath.Join(dir, "invocations.ndjson"), []byte(trace), 0600); err != nil {
		t.Fatal(err)
	}
	events := NativeEvents{ConversationID: "main", Status: "SUCCESS", AgentResponseSteps: 2}
	if err := observeInvocations(dir, &events, 10); err == nil || events.CloudDecisionRounds != nil {
		t.Fatalf("contradictory completions accepted: %+v %v", events, err)
	}
}
