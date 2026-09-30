package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func evidenceConfig(t *testing.T, events string) Config {
	t.Helper()
	root := t.TempDir()
	pi := filepath.Join(root, "pi")
	script := `#!/bin/sh
case "$*" in
  *--version*) echo fixture-1 ;;
  *'--mode rpc'*)
    read line
    echo '{"type":"response","command":"get_commands","success":true,"data":{"commands":[]}}'
    ;;
  *)
    printf 'package fixture\nfunc Value() int { return 1 }\n' > value.go
    cat <<'EVENTS'
` + events + "\nEVENTS\n;;\nesac\n"
	if err := os.WriteFile(pi, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	installer, _ := filepath.Abs("../../../install.sh")
	return Config{TzroBinary: binary, Installer: installer, PiBinary: pi,
		Model: "fixture", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fixture-private-key",
		Profiles: []Profile{Baseline}, WorkDir: filepath.Join(root, "profiles"), Run: true,
		Timeout: 30 * time.Second, MaxCost: 1, Prices: &Prices{Input: 1, Output: 2, CacheRead: .1},
		Tasks: []Task{{ID: "fix", Prompt: "Fix Value.", Files: map[string]string{
			"go.mod": "module fixture\n\ngo 1.22\n", "value.go": "package fixture\nfunc Value() int {return 0}\n",
			"value_test.go": "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T){if Value()!=1 {t.Fatal(Value())}}\n",
		}}}}
}

func TestClientVersionPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		ready        bool
	}{
		{"stdout", "echo fixture-1", true},
		{"stderr", "echo fixture-1 >&2", true},
		{"empty", ":", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := evidenceConfig(t, "")
			cfg.Run = false
			body, err := os.ReadFile(cfg.PiBinary)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.PiBinary, []byte(strings.Replace(string(body), "echo fixture-1", tc.output, 1)), 0700); err != nil {
				t.Fatal(err)
			}
			report, err := Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if report.Ready != tc.ready || (tc.ready && report.ClientVersion != "fixture-1") {
				t.Fatalf("invalid version preflight: ready=%v version=%q", report.Ready, report.ClientVersion)
			}
			if report.Results[0].Usage.Requests != 0 {
				t.Fatal("version preflight sent model requests")
			}
		})
	}
}

func TestRunRetainsNativeToolEvidence(t *testing.T) {
	events := `{"type":"message_end","message":{"role":"assistant","stopReason":"toolUse","content":[],"usage":{"input":10,"output":2,"cacheRead":4,"cacheWrite":0}}}
{"type":"tool_execution_start","toolCallId":"call-1","toolName":"read","args":{"path":"/home/.pi/agent/skills/tzro/SKILL.md"}}
{"type":"tool_execution_end","toolCallId":"call-1","toolName":"read","isError":true,"result":{"content":[{"type":"text","text":"fixture-private-key: missing file"}]}}
{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"fixed"}],"usage":{"input":20,"output":3,"cacheRead":8,"cacheWrite":0}}}
{"type":"agent_end"}`
	cfg := evidenceConfig(t, events)
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(report.Results[0])
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	if strings.Contains(string(data), cfg.APIKey) {
		t.Fatal("credential leaked in report")
	}
	trace, ok := result["trace_path"].(string)
	if !ok {
		t.Fatal("native trace missing from report")
	}
	recorded, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorded), cfg.APIKey) || !strings.Contains(string(recorded), "missing file") || !strings.Contains(string(recorded), "tool_execution_end") {
		t.Fatalf("trace lost diagnostic evidence or leaked credentials: %s", recorded)
	}
	turns, _ := result["turns"].([]any)
	tools, _ := result["tools"].([]any)
	if len(turns) != 2 || len(tools) != 1 {
		t.Fatalf("missing turns or tools: %s", data)
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" || tool["is_error"] != true || result["skill_read"] == true {
		t.Fatalf("failed skill read misreported: %s", data)
	}
	if !report.Results[0].TaskSuccess || report.Results[0].Usage.Input != 30 {
		t.Fatalf("recording changed grading or usage: %s", data)
	}
}

func TestRunPreservesConnectionFailure(t *testing.T) {
	cfg := evidenceConfig(t, `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Connection error","usage":{"input":10,"output":2,"cacheRead":4,"cacheWrite":0}}}
{"type":"agent_end"}`)
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := report.Results[0]
	if r.Status != "failed" || r.Error != "client error: Connection error" || len(r.Turns) != 1 || r.Usage.Input != 10 {
		t.Fatalf("first attempt was discarded: %+v", r)
	}
}

func TestRunRepeatsWithHiddenGrading(t *testing.T) {
	cfg := evidenceConfig(t, `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","usage":{"input":10,"output":2,"cacheRead":4,"cacheWrite":0}}}
{"type":"agent_end"}`)
	cfg.Repeats = 3
	cfg.Tasks[0].GradeFiles = map[string]string{"private_test.go": "package fixture\nimport \"testing\"\nfunc TestHidden(t *testing.T){if Value()!=2 {t.Fatal(\"hidden expectation\")}}\n"}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 3 || report.Metadata["source_snapshot_sha256"] == "" {
		t.Fatalf("missing repeat/provenance: %+v", report)
	}
	for i, r := range report.Results {
		if r.Repeat != i+1 || r.TaskSuccess || r.GradingSHA256 == "" {
			t.Fatalf("hidden grading or repeat missing: %+v", r)
		}
		if _, err := os.Stat(filepath.Join(r.Workspace, "private_test.go")); !os.IsNotExist(err) {
			t.Fatal("hidden checks leaked into the agent workspace")
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, "progress.json")); err != nil {
		t.Fatal("durable progress report missing:", err)
	}
}
