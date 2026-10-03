package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/executor"
	"tzro/pkg/store"
)

func TestExecuteSelectedResults(t *testing.T) {
	for _, tc := range []struct{ name, command, status, expected string }{
		{"success", "printf requested-result", "completed", "requested-result"},
		{"failure", "printf partial-result; printf broken >&2; exit 3", "failed", "broken"},
		{"overflow", "printf 'start%12000send' x", "completed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			db := filepath.Join(root, "store.db")
			t.Setenv("TZRO_DB_PATH", db)
			t.Setenv("TZRO_EXPERIMENTAL_RUNTIMES", "0")
			g := executor.Graph{Version: "3.0", TaskID: "selected", Nodes: []executor.Node{
				{ID: "first", Type: executor.NodeTypeTool, Tool: "bash", Args: map[string]any{"command": "printf intermediate-output"}},
				{ID: "last", Type: executor.NodeTypeTool, Tool: "bash", DependsOn: []string{"first"}, Args: map[string]any{"command": tc.command}},
			}, Returns: []string{"/nodes/last/output/stdout"}}
			data, _ := json.Marshal(g)
			var out bytes.Buffer
			cmd := newExecuteCmd()
			cmd.SetIn(bytes.NewReader(data))
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"-", "--result", "selected"})
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["status"] != tc.status || len(out.Bytes()) > 8192 || strings.Contains(out.String(), "intermediate-output") {
				t.Fatalf("incorrect selected result: %s", out.String())
			}
			if tc.expected != "" && !strings.Contains(out.String(), tc.expected) {
				t.Fatalf("requested result or error lost: %s", out.String())
			}
			if tc.name == "success" && strings.Count(out.String(), tc.expected) != 1 {
				t.Fatalf("selected result duplicated: %s", out.String())
			}
			s, err := store.OpenStore(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			id, _ := result["artifact_id"].(string)
			artifact, err := s.GetArtifact(id, root)
			if err != nil {
				t.Fatalf("full graph evidence unavailable: %v", err)
			}
			if !strings.Contains(artifact.Body, "intermediate-output") {
				t.Fatal("intermediate output discarded")
			}
		})
	}
}

type lowConfidenceDecision struct{}

func TestGraphReadOverflowRetainsExactEvidence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	db := filepath.Join(root, "store.db")
	t.Setenv("TZRO_DB_PATH", db)
	t.Setenv("TZRO_EXPERIMENTAL_RUNTIMES", "0")
	body := strings.Repeat("exact source text\r\n", 1000) + "final line"
	if err := os.WriteFile("notes.txt", []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	g := executor.Graph{Version: "3.0", TaskID: "read-overflow", Nodes: []executor.Node{
		{ID: "read", Type: executor.NodeTypeTool, Tool: "read", Args: map[string]any{"file": "notes.txt"}},
	}, Returns: []string{"/nodes/read/output"}}
	data, _ := json.Marshal(g)
	var out bytes.Buffer
	cmd := newExecuteCmd()
	cmd.SetIn(bytes.NewReader(data))
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-", "--result", "selected"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var selected map[string]any
	if err := json.Unmarshal(out.Bytes(), &selected); err != nil {
		t.Fatal(err)
	}
	if selected["status"] != "completed" || selected["omitted"] == nil || out.Len() > 8000 {
		t.Fatalf("bad selected result: %s", out.String())
	}
	s, err := store.OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _ := selected["artifact_id"].(string)
	artifact, err := s.GetArtifact(id, root)
	if err != nil {
		t.Fatal(err)
	}
	var full executor.ExecutionResult
	if err := json.Unmarshal([]byte(artifact.Body), &full); err != nil {
		t.Fatal(err)
	}
	if full.Outputs["read"].Data["body"] != body || full.Returns["nodes/read/output"] == nil {
		t.Fatalf("exact source or requested return lost: %+v", full)
	}
}

func (lowConfidenceDecision) Decide(context.Context, *executor.DecisionInput) (*executor.DecisionOutput, error) {
	return &executor.DecisionOutput{Answer: "yes", Confidence: 0.2}, nil
}

func TestSelectedGraphYieldAndMissingWorker(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_worker", true: "low_confidence"}[configured], func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			s, err := store.OpenStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			g := &executor.Graph{Version: "3.0", TaskID: "decision", Nodes: []executor.Node{
				{ID: "decide", Type: executor.NodeTypeDecision, Question: &executor.Question{Type: "choice", Prompt: "Continue?", Options: []string{"yes", "no"}}, Accept: &executor.AcceptCriteria{MinConfidence: 0.9}},
				{ID: "later", Type: executor.NodeTypeTool, Tool: "bash", DependsOn: []string{"decide"}, Args: map[string]any{"command": "touch must-not-run"}},
			}}
			e := executor.NewEngine()
			if configured {
				e = executor.NewEngine(executor.WithDecider(lowConfidenceDecision{}))
			}
			result, err := e.Execute(context.Background(), g)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(selectedGraphResult(g, result, s, root))
			want := "no decider configured"
			if configured {
				want = "acceptance_criteria_unmet"
			}
			if !strings.Contains(string(data), want) || !strings.Contains(string(data), "blocked") {
				t.Fatalf("lost error/yield evidence: %s", data)
			}
			if _, err := os.Stat("must-not-run"); !os.IsNotExist(err) {
				t.Fatal("ran after failed/yielded dependency")
			}
		})
	}
}

func TestSelectedGraphStorageFailurePreservesEvidence(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	result := &executor.ExecutionResult{TaskID: "fallback", Status: "completed", Outputs: map[string]executor.NodeOutput{"intermediate": {NodeID: "intermediate", Status: "completed", Stdout: "retain-me"}}}
	data, _ := json.Marshal(selectedGraphResult(&executor.Graph{}, result, s, t.TempDir()))
	if !strings.Contains(string(data), "retain-me") || strings.Contains(string(data), "artifact_id") {
		t.Fatalf("unrecoverable omission: %s", data)
	}
}
