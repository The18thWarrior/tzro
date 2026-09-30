package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A protocol smoke test with the installed client, never a quality benchmark.
// All cloud replies and extraction responses are fixtures. JEV can be real.
func TestNativePiProfiles(t *testing.T) {
	if os.Getenv("TZRO_NATIVE_PI_SMOKE") != "1" {
		t.Skip("set TZRO_NATIVE_PI_SMOKE=1 with Pi installed")
	}
	pi, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "tzro-source")
	build := exec.Command("go", "build", "-o", binary, "./cmd/tzro")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "wrong route", 404)
			return
		}
		var req struct {
			Messages []struct {
				Role    string
				Content json.RawMessage
			}
			Model string
			Tools []struct {
				Function struct{ Name, Description string }
			}
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Model != "fixture-native" {
			http.Error(w, "wrong model", 400)
			return
		}
		requests.Add(1)
		toolMessages := 0
		installed := false
		for _, tool := range req.Tools {
			installed = installed || tool.Function.Name == "tzro"
		}
		for _, m := range req.Messages {
			if m.Role == "tool" {
				toolMessages++
				if toolMessages == 2 && installed && !strings.Contains(string(m.Content), "Tzro structural view") {
					t.Error("installed read hook did not deliver a recoverable structural view to the model")
				}
			}
		}
		delta := map[string]any{"role": "assistant", "content": "Fixed the implementation."}
		finish := "stop"
		if toolMessages == 0 {
			name := "bash"
			args := map[string]any{"command": "cat value.go"}
			for _, tool := range req.Tools {
				if tool.Function.Name == "tzro" {
					name = "tzro"
					args = map[string]any{"command": "probe", "args": []string{"Value"}}
				}
			}
			arguments, _ := json.Marshal(args)
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_discover", "type": "function", "function": map[string]string{"name": name, "arguments": string(arguments)}}}}
			finish = "tool_calls"
		} else if toolMessages == 1 {
			arguments, _ := json.Marshal(map[string]string{"path": "value.go"})
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_read", "type": "function", "function": map[string]string{"name": "read", "arguments": string(arguments)}}}}
			finish = "tool_calls"
		} else if toolMessages == 2 {
			// Force tool use only in this wire fixture, never in paid task prompts.
			command := "printf 'package fixture\\nfunc Value() int { return 1 }\\n' > value.go"
			name := "bash"
			var args any = map[string]string{"command": command}
			if installed {
				name = "tzro_execute_graph"
				nodes := []any{
					map[string]any{"id": "write", "type": "tool", "tool": "bash", "args": map[string]string{"command": command}},
					map[string]any{"id": "verify", "type": "tool", "tool": "read", "depends_on": []string{"write"}, "args": map[string]string{"file": "value.go"}},
					map[string]any{"id": "module", "type": "tool", "tool": "read", "args": map[string]string{"file": "go.mod"}},
				}
				for _, tool := range req.Tools {
					if tool.Function.Name == name && strings.Contains(tool.Function.Description, "Local decision/extraction workers are enabled") {
						nodes = append(nodes,
							map[string]any{"id": "extract", "type": "extract", "depends_on": []string{"verify"}, "input": map[string]string{"text": "Inspect value.go"}, "labels": []string{"file_path"}},
							map[string]any{"id": "inspect", "type": "tool", "tool": "skeleton", "depends_on": []string{"extract"}, "args": map[string]any{"file": map[string]string{"$ref": "/nodes/extract/output/file_path"}}},
							map[string]any{"id": "decision", "type": "decision", "depends_on": []string{"inspect"}, "input": map[string]any{"text": map[string]string{"$ref": "/nodes/inspect/output/skeleton"}}, "question": map[string]any{"type": "choice", "prompt": "Which word is a programming language?", "options": []string{"Go", "banana"}}},
						)
					}
				}
				args = map[string]any{"graph": map[string]any{"version": "3.0", "task_id": "fixture", "nodes": nodes, "returns": []string{"/nodes/verify/output", "/nodes/module/output/body"}}}
			}
			arguments, _ := json.Marshal(args)
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_fixture", "type": "function", "function": map[string]string{"name": name, "arguments": string(arguments)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]any{"id": "fixture", "object": "chat.completion.chunk", "created": 1, "model": req.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
		encoded, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		chunk["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}
		chunk["usage"] = map[string]any{"prompt_tokens": 14, "completion_tokens": 2, "total_tokens": 16, "prompt_tokens_details": map[string]int{"cached_tokens": 4}}
		encoded, _ = json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer server.Close()
	write := func(name, body string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	worker := func(name, response string) string {
		return write(name, "#!/bin/sh\necho '{\"status\":\"ready\"}'\nwhile IFS= read -r line; do echo '"+response+"'; done\n")
	}
	modelDir := filepath.Join(root, "extractor-model")
	if err := os.Mkdir(modelDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "weights"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	decisionBin := worker("decision", "{\"answer\":\"Go\",\"confidence\":0.95}")
	decisionModel := write("fixture.gguf", "fixture")
	decisionVersion := "fixture"
	// The fixture must answer both the readiness input and the graph input.
	extractorBin := write("extract", `#!/bin/sh
echo '{"status":"ready"}'
while IFS= read -r line; do
  case "$line" in
    *value.go*) echo '{"spans":[{"label":"file_path","text":"value.go","start":8,"end":16,"confidence":0.99}]}' ;;
    *) echo '{"spans":[{"label":"file_path","text":"main.go","start":0,"end":7,"confidence":0.99}]}' ;;
  esac
done
`)
	if realBin, realModel := os.Getenv("TZRO_TEST_JEV_BIN"), os.Getenv("TZRO_TEST_JEV_MODEL"); realBin != "" && realModel != "" {
		decisionBin, decisionModel, decisionVersion = realBin, realModel, "local real scorer integration test"
	}
	installer, _ := filepath.Abs("../../../install.sh")
	cfg := Config{TzroBinary: binary, Installer: installer, PiBinary: pi, Model: "fixture-native", BaseURL: server.URL + "/v1", APIKey: "fixture-only", WorkDir: filepath.Join(root, "profiles"), SetupTimeout: 60 * time.Second, Timeout: 60 * time.Second, Run: true, MaxCost: 1, Prices: &Prices{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1},
		Full: FullConfig{DecisionBin: decisionBin, DecisionModel: decisionModel, DecisionVersion: decisionVersion, ExtractorBin: extractorBin, ExtractorModel: modelDir, ExtractorVersion: "fixture"},
		Tasks: []Task{{ID: "fix", Prompt: "Fix Value to return 1. Keep all tests unchanged.", Files: map[string]string{
			"go.mod": "module fixture\n\ngo 1.22\n", "value.go": "package fixture\nfunc Value() int {\n" + strings.Repeat(" // Existing implementation detail retained in the local body store.\n", 220) + "return 0\n}\n",
			"value_test.go": "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=1 { t.Fatal(\"wrong value\") } }\n",
			"graph.json":    `{"version":"3.0","task_id":"fixture","nodes":[{"id":"decision","type":"decision","question":{"type":"choice","prompt":"Which word is a programming language?","options":["Go","banana"]},"input":{"text":"Go is a programming language."}}]}`,
		}}}}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready {
		t.Fatalf("preflight failed: %+v", report.Results)
	}
	if report.ClientVersion == "" {
		t.Fatal("native client version missing")
	}
	for _, r := range report.Results {
		if r.Status != "completed" || !r.TaskSuccess || !r.Usage.Complete || r.Usage.Requests != 4 || r.ToolCalls != 3 || r.ToolErrors != 0 {
			t.Errorf("incomplete native execution: %+v", r)
		}
		if r.SkillLoaded != (r.Profile != Baseline) {
			t.Errorf("skill loading: %+v", r)
		}
		if r.Profile != Baseline && (len(r.Tools) != 3 || r.Tools[0].Name != "tzro" || r.Tools[0].IsError) {
			t.Errorf("installed native tool did not execute: %+v", r.Tools)
		}
		if r.Profile != Baseline && (len(r.Tools) != 3 || r.Tools[2].Name != "tzro_execute_graph" || r.Tools[2].IsError || !strings.Contains(string(r.Tools[2].Result), "func Value() int { return 1 }") || !strings.Contains(string(r.Tools[2].Result), "module fixture")) {
			t.Errorf("installed graph did not return the dependent result: %+v", r.Tools)
		}
		if r.Profile != Baseline && r.Hooks != "invocation observed" {
			t.Errorf("native hook not invoked: %+v", r)
		}
		if r.Profile == Full {
			if r.ProxyRequests != 4 || !r.RuntimeReady {
				t.Errorf("Full routing/readiness: %+v", r)
			}
			invoked := map[string]bool{}
			for _, event := range r.Activity {
				if event["status"] == "completed" {
					kind, _ := event["runtime"].(string)
					invoked[kind] = true
				}
			}
			if !invoked["decision"] || !invoked["extractor"] || !invoked["graph"] {
				t.Errorf("Full task did not record its graph and worker invocations: %v", invoked)
			}
		}
		if r.PromptSHA256 != report.Results[0].PromptSHA256 || r.FixtureSHA256 != report.Results[0].FixtureSHA256 {
			t.Error("task input mismatch")
		}
	}
	if requests.Load() != 12 {
		t.Errorf("provider request count: %d", requests.Load())
	}
}
