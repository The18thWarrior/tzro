package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"tzro/pkg/executor"
)

func configureFull(cfg Config, p *prepared) error {
	f := cfg.Full
	for name, path := range map[string]string{"JEV executable": f.DecisionBin, "JEV model": f.DecisionModel, "GLiNER executable": f.ExtractorBin, "GLiNER model": f.ExtractorModel} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("Full requires an absolute path for %s", name)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("Full %s: %w", name, err)
		}
	}
	if f.DecisionVersion == "" || f.ExtractorVersion == "" {
		return fmt.Errorf("Full requires decision and extractor version identifiers")
	}
	args, _ := json.Marshal(f.ExtractorArgs)
	p.env = append(p.env, "TZRO_EXPERIMENTAL_RUNTIMES=1", "TZRO_DECISION_PROVIDER=local",
		"TZRO_DECISION_BIN="+f.DecisionBin, "TZRO_DECISION_MODEL="+f.DecisionModel,
		"TZRO_EXTRACTOR_BIN="+f.ExtractorBin, "TZRO_EXTRACTOR_ARGS="+string(args),
		"GLINER_MODEL_DIR="+f.ExtractorModel, "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1")
	return nil
}

func checkRuntimes(ctx context.Context, p *prepared) error {
	graph := executor.Graph{Version: "3.0", TaskID: "installation-readiness", Nodes: []executor.Node{
		{ID: "decision", Type: executor.NodeTypeDecision, Question: &executor.Question{Type: "choice", Prompt: "Which word is a programming language?", Options: []string{"Go", "banana"}}, Input: map[string]interface{}{"text": "Go is a programming language."}},
		{ID: "extract", Type: executor.NodeTypeExtract, Labels: []string{"file_path"}, Input: map[string]interface{}{"text": "main.go"}},
	}}
	data, _ := json.Marshal(graph)
	cmd := command(ctx, p.env, p.result.Workspace, p.binary, "execute", "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Full runtime readiness command failed: %w", err)
	}
	var result executor.ExecutionResult
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("invalid runtime readiness result: %w", err)
	}
	for _, node := range []string{"decision", "extract"} {
		if result.Outputs[node].Status != "completed" {
			return fmt.Errorf("Full %s runtime is not ready: %s", node, result.Outputs[node].Stderr)
		}
	}
	if result.Outputs["decision"].Data["answer"] != "Go" {
		return fmt.Errorf("decision readiness did not answer the choice probe correctly")
	}
	spans, _ := result.Outputs["extract"].Data["spans"].([]any)
	if len(spans) == 0 {
		return fmt.Errorf("extractor readiness returned no spans")
	}
	if result.Outputs["extract"].Data["file_path"] != "main.go" {
		return fmt.Errorf("extractor readiness did not recover the supplied path")
	}
	p.result.RuntimeReady = true
	return nil
}
