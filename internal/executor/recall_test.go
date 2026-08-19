package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/symbols"
)

// mockRecallEngine supports two-pass extraction (ADR-0064).
// Pass 1 (jsonSchema == ""): returns reasoning with ACTION tags or SYNTHESIZE_READY.
// Pass 2 (jsonSchema contains "tool_call"/"synthesize"): extracts structured JSON from lastResponse.
type mockRecallEngine struct {
	Calls        []string
	lastResponse string
}

func (m *mockRecallEngine) Infer(ctx context.Context, systemPrompt, lastResult, jsonSchema string, _ ModelTarget) (string, error) {
	m.Calls = append(m.Calls, systemPrompt+"\nLAST_RESULT: "+lastResult)

	// Two-pass GBNF detection
	if jsonSchema != "" && strings.Contains(jsonSchema, `"tool_call"`) && strings.Contains(jsonSchema, `"synthesize"`) {
		return m.extractAction(), nil
	}

	if strings.Contains(systemPrompt, "Synthesis Engine") {
		if strings.Contains(systemPrompt, "Found API key in config.go") {
			return "Final synthesis with fact — the API key was found in config.go and is correctly configured for the target environment.", nil
		}
		return "Final synthesis MISSING fact — no relevant API key information was found in the upstream exploration steps.", nil
	} else if strings.Contains(systemPrompt, "Recall Node") {
		if strings.Contains(lastResult, "Baseline context loaded") {
			m.lastResponse = `<ACTION>{"tool": "fetch_details", "arguments": {"node_id": "probe_1", "step_index": 1}}</ACTION>`
			return m.lastResponse, nil
		}
		if strings.Contains(lastResult, "Details for probe_1 Step 1") {
			m.lastResponse = `<ACTION>{"tool": "update_refined_context", "arguments": {"fact": "Found API key in config.go"}}</ACTION>`
			return m.lastResponse, nil
		}
		m.lastResponse = `<SYNTHESIZE_READY>`
		return m.lastResponse, nil
	}

	return "Unexpected prompt", nil
}

func (m *mockRecallEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, _ ModelTarget) (string, error) {
	var sys, usr string
	for _, msg := range messages {
		if msg.Role == "system" {
			sys = msg.Content
		} else if msg.Role == "user" {
			usr = msg.Content
		}
	}
	return m.Infer(ctx, sys, usr, jsonSchema, TargetAuto)
}

func (m *mockRecallEngine) extractAction() string {
	if strings.Contains(m.lastResponse, "<SYNTHESIZE_READY>") {
		return `{"action":"synthesize","tool":"","arguments":{}}`
	}
	actionRe := regexp.MustCompile(`(?s)<ACTION>(.*?)</ACTION>`)
	matches := actionRe.FindStringSubmatch(m.lastResponse)
	if len(matches) > 1 {
		var parsed struct {
			Tool      string                 `json:"tool"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if json.Unmarshal([]byte(matches[1]), &parsed) == nil {
			argsJSON, _ := json.Marshal(parsed.Arguments)
			return fmt.Sprintf(`{"action":"tool_call","tool":"%s","arguments":%s}`, parsed.Tool, string(argsJSON))
		}
	}
	return `{"action":"synthesize","tool":"","arguments":{}}`
}

func TestRunRecall_RefinedContext(t *testing.T) {
	ctx := context.Background()
	mock := &mockRecallEngine{}

	// Setup temporary DB
	tempDB := "test_recall.db"
	_ = os.Remove(tempDB)
	defer os.Remove(tempDB)

	memory.DB.SetDBPathForTesting(tempDB)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}

	// Insert a dummy thought step — ProbeID must use the composite
	// format "taskID_nodeID" matching production probe storage.
	_ = memory.DB.AddThoughtStep(memory.ThoughtStep{
		TaskID:     "t1",
		ProbeID:    "t1_probe_1",
		StepIndex:  1,
		ToolName:   "read_file",
		ToolArgs:   `{"path": "config.go"}`,
		ToolOutput: "API_KEY=12345",
	})

	// We need an ExecutionEngine but only for its publisher.
	eng := &ExecutionEngine{}

	result, err := eng.RunRecall(ctx, "t1", "recall_1", []string{"probe_1"}, "Find the API key", mock)
	if err != nil {
		t.Fatalf("RunRecall failed: %v", err)
	}

	if !strings.Contains(result.Synthesis, "Final synthesis with fact") {
		t.Errorf("Expected synthesis with fact, got: %s", result.Synthesis)
	}
}

func TestRecallNode_DocGenSectionedSynthesisIntegration(t *testing.T) {
	ctx := context.Background()

	// Setup temporary DB
	tempDB := "test_recall_docgen.db"
	_ = os.Remove(tempDB)
	defer os.Remove(tempDB)

	memory.DB.SetDBPathForTesting(tempDB)
	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}

	taskID := "comparison_cooperative_inference_module_docs"
	probeID := taskID + "_explore_project"

	// Insert symbols in DB
	_ = memory.DB.InsertSymbols(probeID, taskID, []symbols.Symbol{
		{Name: "InferenceBackend", Kind: symbols.SymbolInterface, Signature: "type InferenceBackend interface"},
		{Name: "LocalModelManager", Kind: symbols.SymbolType, Signature: "type LocalModelManager struct"},
		{Name: "CallRouter", Kind: symbols.SymbolFunc, Signature: "func CallRouter()"},
		{Name: "ThermalState", Kind: symbols.SymbolType, Signature: "type ThermalState int"},
	})

	_ = memory.DB.AddThoughtStep(memory.ThoughtStep{
		TaskID:     taskID,
		ProbeID:    probeID,
		StepIndex:  1,
		ToolName:   "read_file",
		ToolArgs:   `{"path": "backend.go"}`,
		ToolOutput: "type InferenceBackend interface {}\ntype LocalModelManager struct {}\nfunc CallRouter() {}\ntype ThermalState int",
	})

	mock := &mockSectionInferenceEngine{
		responses: map[string]string{
			"schema": `{"title": "Internal Inference Documentation", "sections": [
				{"heading": "## 1. Overview & Architecture", "objective": "Architecture overview", "is_terminal": false},
				{"heading": "## 2. Core Backends & Routing", "objective": "InferenceBackend, LocalModelManager, CallRouter", "is_terminal": false},
				{"heading": "## 3. Support & Thermal State", "objective": "ThermalState and metrics", "is_terminal": true}
			]}`,
			"## 1. Overview & Architecture": "## 1. Overview & Architecture\n\nThe internal/inference package orchestrates local and cloud inference.",
			"## 2. Core Backends & Routing": "## 2. Core Backends & Routing\n\n`InferenceBackend` defines the model interface. `LocalModelManager` coordinates local sidecars. `CallRouter` dispatches navigation calls.",
			"## 3. Support & Thermal State": "## 3. Support & Thermal State\n\n`ThermalState` monitors device thermals to regulate concurrency.",
		},
	}

	eng := &ExecutionEngine{}
	goal := "Generate module-level documentation for internal/inference/ covering ALL public types: InferenceBackend, LocalModelManager, CallRouter, ThermalState."

	res, err := eng.RunRecall(ctx, taskID, "recall_1", []string{"explore_project"}, goal, mock)
	if err != nil {
		t.Fatalf("RunRecall failed: %v", err)
	}

	if !strings.Contains(res.Synthesis, "# Internal Inference Documentation") {
		t.Errorf("expected title in synthesis, got:\n%s", res.Synthesis)
	}
	if !strings.Contains(res.Synthesis, "## 1. Overview & Architecture") {
		t.Errorf("expected section 1 in synthesis")
	}
	if !strings.Contains(res.Synthesis, "## 2. Core Backends & Routing") {
		t.Errorf("expected section 2 in synthesis")
	}
	if !strings.Contains(res.Synthesis, "## 3. Support & Thermal State") {
		t.Errorf("expected section 3 in synthesis")
	}
}
