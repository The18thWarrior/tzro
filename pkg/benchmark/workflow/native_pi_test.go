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
			Messages []struct{ Role string }
			Model    string
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
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == "tool" {
				hasTool = true
			}
		}
		delta := map[string]any{"role": "assistant", "content": "Fixed the implementation."}
		finish := "stop"
		if !hasTool {
			// Force tool use only in this wire fixture, to verify native hook loading.
			command := "printf 'package fixture\\nfunc Value() int { return 1 }\\n' > value.go\nif [ \"${TZRO_EXPERIMENTAL_RUNTIMES:-}\" = 1 ]; then tzro execute graph.json; fi\nprintf 'fixture tool output\\n'"
			arguments, _ := json.Marshal(map[string]string{"command": command})
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_fixture", "type": "function", "function": map[string]string{"name": "bash", "arguments": string(arguments)}}}}
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
	if realBin, realModel := os.Getenv("TZRO_TEST_JEV_BIN"), os.Getenv("TZRO_TEST_JEV_MODEL"); realBin != "" && realModel != "" {
		decisionBin, decisionModel, decisionVersion = realBin, realModel, "local real scorer integration test"
	}
	installer, _ := filepath.Abs("../../../install.sh")
	cfg := Config{TzroBinary: binary, Installer: installer, PiBinary: pi, Model: "fixture-native", BaseURL: server.URL + "/v1", APIKey: "fixture-only", WorkDir: filepath.Join(root, "profiles"), SetupTimeout: 60 * time.Second, Timeout: 60 * time.Second, Run: true, MaxCost: 1, Prices: &Prices{Input: 1, Output: 1, CacheRead: 1, CacheWrite: 1},
		Full: FullConfig{DecisionBin: decisionBin, DecisionModel: decisionModel, DecisionVersion: decisionVersion, ExtractorBin: worker("extract", "{\"spans\":[{\"label\":\"file_path\",\"text\":\"main.go\",\"start\":0,\"end\":7,\"confidence\":0.99}]}"), ExtractorModel: modelDir, ExtractorVersion: "fixture"},
		Tasks: []Task{{ID: "fix", Prompt: "Fix Value to return 1. Keep all tests unchanged.", Files: map[string]string{
			"go.mod": "module fixture\n\ngo 1.22\n", "value.go": "package fixture\nfunc Value() int { return 0 }\n",
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
	for _, r := range report.Results {
		if r.Status != "completed" || !r.TaskSuccess || !r.Usage.Complete || r.Usage.Requests != 2 || r.ToolCalls != 1 || r.ToolErrors != 0 {
			t.Errorf("incomplete native execution: %+v", r)
		}
		if r.SkillLoaded != (r.Profile != Baseline) {
			t.Errorf("skill loading: %+v", r)
		}
		if r.Profile != Baseline && r.Hooks != "invocation observed" {
			t.Errorf("native hook not invoked: %+v", r)
		}
		if r.Profile == Full {
			if r.ProxyRequests != 2 || !r.RuntimeReady {
				t.Errorf("Full routing/readiness: %+v", r)
			}
			invoked := false
			for _, event := range r.Activity {
				if event["runtime"] == "decision" && event["status"] == "completed" {
					invoked = true
				}
			}
			if !invoked {
				t.Errorf("Full task did not record its decision invocation: %+v", r)
			}
		}
		if r.PromptSHA256 != report.Results[0].PromptSHA256 || r.FixtureSHA256 != report.Results[0].FixtureSHA256 {
			t.Error("task input mismatch")
		}
	}
	if requests.Load() != 6 {
		t.Errorf("provider request count: %d", requests.Load())
	}
}
