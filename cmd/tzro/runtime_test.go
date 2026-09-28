package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/executor"
)

func TestExecuteUsesOptInRuntimes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)
	worker := func(name, response string) string {
		path := filepath.Join(home, name)
		script := "#!/bin/sh\nprintf '%s\\n' '{\"status\":\"ready\"}'\nwhile IFS= read -r line; do printf '%s\\n' '" + response + "'; done\n"
		if err := os.WriteFile(path, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("TZRO_EXPERIMENTAL_RUNTIMES", "1")
	t.Setenv("TZRO_DECISION_PROVIDER", "local")
	t.Setenv("TZRO_DECISION_BIN", worker("jev-score", "{\"answer\":\"yes\",\"confidence\":0.99}"))
	t.Setenv("TZRO_EXTRACTOR_BIN", worker("extract", "{\"spans\":[{\"label\":\"file_path\",\"text\":\"main.go\",\"start\":0,\"end\":7,\"confidence\":0.99}]}"))
	t.Setenv("TZRO_EXTRACTOR_ARGS", "[]")
	graph := executor.Graph{Version: "3.0", TaskID: "runtime-smoke", Nodes: []executor.Node{
		{ID: "decision", Type: executor.NodeTypeDecision, Question: &executor.Question{Type: "choice", Prompt: "Is this a Go file?", Options: []string{"yes", "no"}}, Input: map[string]interface{}{"file": "main.go"}},
		{ID: "extract", Type: executor.NodeTypeExtract, Labels: []string{"file_path"}, Input: map[string]interface{}{"text": "main.go"}},
	}}
	data, _ := json.Marshal(graph)
	path := filepath.Join(home, "graph.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := newExecuteCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{path})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	var result executor.ExecutionResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid graph result: %s %v", out.String(), err)
	}
	for _, id := range []string{"decision", "extract"} {
		if result.Outputs[id].Status != "completed" {
			t.Fatalf("%s runtime unused: %+v", id, result)
		}
	}
}
