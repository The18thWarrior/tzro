package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"tzro/internal/inference"
	"tzro/internal/symbols"
)

type mockSectionInferenceEngine struct {
	responses map[string]string
}

func (m *mockSectionInferenceEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	if m.responses != nil {
		if jsonSchema != "" && m.responses["schema"] != "" {
			return m.responses["schema"], nil
		}
		// Prioritize exact Section Heading match in userPrompt
		for k, v := range m.responses {
			if strings.Contains(userPrompt, "Section Heading: "+k) || strings.Contains(userPrompt, "Section Heading: ## "+k) {
				return v, nil
			}
		}
		for k, v := range m.responses {
			if strings.Contains(userPrompt, k) {
				return v, nil
			}
		}
		for k, v := range m.responses {
			if strings.Contains(systemPrompt, k) {
				return v, nil
			}
		}
	}

	combined := systemPrompt + "\n" + userPrompt
	if strings.Contains(combined, "Comparison Matrix") {
		return `## 2. Comparative Analysis Matrix & Benchmarks

| System | Architecture | Language SDKs | Production Users |
| :--- | :--- | :--- | :--- |
| Temporal | Event Sourcing / Replay | Go, TypeScript, Python, Java | Netflix, Stripe, Descript |
| Restate | Durable Async Functions | TypeScript, Java, Rust | Emerging |
| Inngest | Event-Driven Functions | TypeScript, Python, Go | SoundCloud |`, nil
	}

	if strings.Contains(combined, "Core Architectural Patterns") {
		return `## 1. Core Architectural Patterns & Mechanics

Temporal relies on event sourcing and deterministic replay through history events. Restate models execution as durable execution log RPCs. Inngest uses step-based event-driven execution.`, nil
	}

	if strings.Contains(combined, "Cost Arbitrage") {
		return `## 3. Cost Arbitrage, Pricing & Economics

Self-hosting Temporal requires Cassandra/PostgreSQL clusters ($200-$500/mo) vs Temporal Cloud starting with free credits and $0.000025 per action.`, nil
	}

	return `## 4. Decision Framework & Recommendations

Choose Temporal for mission-critical complex financial workflows. Choose Restate for lightweight low-latency RPCs. Choose Inngest for event-driven serverless background jobs.`, nil
}

func (m *mockSectionInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	var sys, user string
	for _, msg := range messages {
		if msg.Role == "system" {
			sys = msg.Content
		} else if msg.Role == "user" {
			user += "\n" + msg.Content
		}
	}
	return m.Infer(ctx, sys, user, jsonSchema, target)
}

func TestShouldRunSectionedSynthesis(t *testing.T) {
	// Case 1: DocGen task with multiple files and symbols
	docgenGoal := "Generate module-level documentation for internal/inference/ covering ALL public types across 4 layers."
	refinedCtx := strings.Repeat("backend.go defines InferenceBackend interface. routing.go routes requests.\n", 20)
	if !ShouldRunSectionedSynthesis(docgenGoal, "docgen", refinedCtx, 12, 11, false) {
		t.Errorf("expected docgen task with 11 files to trigger sectioned synthesis")
	}

	// Case 2: Research task
	researchGoal := "Conduct comprehensive research comparing Temporal, Restate, and Inngest."
	if !ShouldRunSectionedSynthesis(researchGoal, "research", refinedCtx, 0, 3, false) {
		t.Errorf("expected research task to trigger sectioned synthesis")
	}

	// Case 3: Codegen sink should NEVER trigger sectioned synthesis
	if ShouldRunSectionedSynthesis(docgenGoal, "docgen", refinedCtx, 12, 11, true) {
		t.Errorf("expected codegen sink to NEVER trigger sectioned synthesis")
	}

	// Case 4: Trivial short query
	trivialGoal := "What is the name of the main function?"
	shortCtx := "func main() {}"
	if ShouldRunSectionedSynthesis(trivialGoal, "general", shortCtx, 1, 1, false) {
		t.Errorf("expected trivial single-function query NOT to trigger sectioned synthesis")
	}
}

func TestGenerateDocGenOutline(t *testing.T) {
	goal := "Generate module-level documentation for internal/inference/ covering ALL public types across 4 layers: Core, Local, Routing, Support."
	refinedCtx := `File: internal/inference/backend.go
type InferenceBackend interface { CallModel(ctx, messages, jsonSchema) }

File: internal/inference/local_model.go
type LocalModelManager struct {}

File: internal/inference/routing.go
func CallRouter()
func CallWorker()

File: internal/inference/thermal.go
type ThermalState int
type TokenTracker struct {}`

	// Test 1: Successful model outline generation
	mockEngine := &mockSectionInferenceEngine{
		responses: map[string]string{
			"schema": `{"title": "Inference Module Documentation", "sections": [
				{"heading": "## 1. Overview & Architecture", "objective": "Summarize inference module architecture and core interfaces", "is_terminal": false},
				{"heading": "## 2. Core & Local Layers", "objective": "Detail InferenceBackend and LocalModelManager", "is_terminal": false},
				{"heading": "## 3. Routing & Dual-Sidecar Mechanics", "objective": "Detail CallRouter and CallWorker routing paths", "is_terminal": false},
				{"heading": "## 4. Support Subsystem & Telemetry", "objective": "Document ThermalState, TokenTracker, and metrics", "is_terminal": false},
				{"heading": "## 5. Public API Index & Usage Patterns", "objective": "Provide comprehensive symbol reference table and code examples", "is_terminal": true}
			]}`,
		},
	}

	outline, err := GenerateDocGenOutline(context.Background(), mockEngine, goal, refinedCtx, nil)
	if err != nil {
		t.Fatalf("GenerateDocGenOutline failed: %v", err)
	}
	if len(outline.Sections) != 5 {
		t.Fatalf("expected 5 sections, got %d", len(outline.Sections))
	}
	if !outline.Sections[len(outline.Sections)-1].IsTerminal {
		t.Errorf("expected last section to be marked is_terminal")
	}

	// Test 2: Safety floor triggered when model under-decomposes (1 lazy section)
	lazyEngine := &mockSectionInferenceEngine{
		responses: map[string]string{
			"schema": `{"title": "Docs", "sections": [
				{"heading": "All Docs", "objective": "Write everything in one shot", "is_terminal": true}
			]}`,
		},
	}

	safetyOutline, err := GenerateDocGenOutline(context.Background(), lazyEngine, goal, refinedCtx, nil)
	if err != nil {
		t.Fatalf("GenerateDocGenOutline safety floor failed: %v", err)
	}
	if len(safetyOutline.Sections) < 3 {
		t.Fatalf("expected safety floor to produce at least 3 sections on multi-file context, got %d", len(safetyOutline.Sections))
	}
	if !safetyOutline.Sections[len(safetyOutline.Sections)-1].IsTerminal {
		t.Errorf("expected safety floor last section to be marked is_terminal")
	}
}

func TestExecuteDocGenSectionedSynthesis(t *testing.T) {
	goal := "Generate module-level documentation for internal/inference/ covering ALL public types across 4 layers."
	refinedCtx := `File: internal/inference/backend.go
type InferenceBackend interface { CallModel(ctx, messages, jsonSchema) }

File: internal/inference/local_model.go
type LocalModelManager struct {}`

	outline := &SynthesisOutline{
		Title: "Internal Inference Architecture & Public API Documentation",
		Sections: []SectionSpec{
			{
				Heading:    "## 1. Module Overview & Architecture",
				Objective:  "Explain the inference engine architecture and backend interfaces.",
				FormatHint: "prose",
				IsTerminal: false,
			},
			{
				Heading:    "## 2. Core Backends & Local Execution",
				Objective:  "Detail InferenceBackend and LocalModelManager implementations.",
				FormatHint: "prose",
				IsTerminal: false,
			},
			{
				Heading:    "## 3. Public Types & Usage Patterns",
				Objective:  "Provide complete symbol reference and code usage patterns.",
				FormatHint: "bulleted_deep_dive",
				IsTerminal: true,
			},
		},
	}

	mockEngine := &mockSectionInferenceEngine{
		responses: map[string]string{
			"## 1. Module Overview & Architecture": "## 1. Module Overview & Architecture\n\nThe internal/inference package coordinates local and remote model execution with strict type boundaries.",
			"## 2. Core Backends & Local Execution": "## 2. Core Backends & Local Execution\n\nInferenceBackend defines the primary interface. LocalModelManager manages on-device llama-server processes.",
			"## 3. Public Types & Usage Patterns":  "## 3. Public Types & Usage Patterns\n\n- `InferenceBackend`: Primary interface\n- `LocalModelManager`: Process coordinator",
		},
	}

	doc, err := ExecuteDocGenSectionedSynthesis(context.Background(), goal, refinedCtx, outline, nil, mockEngine)
	if err != nil {
		t.Fatalf("ExecuteDocGenSectionedSynthesis failed: %v", err)
	}

	if !strings.Contains(doc, "# Internal Inference Architecture & Public API Documentation") {
		t.Errorf("expected document title in output, got:\n%s", doc)
	}
	if !strings.Contains(doc, "## 1. Module Overview & Architecture") {
		t.Errorf("expected section 1 heading in output")
	}
	if !strings.Contains(doc, "## 2. Core Backends & Local Execution") {
		t.Errorf("expected section 2 heading in output")
	}
	if !strings.Contains(doc, "## 3. Public Types & Usage Patterns") {
		t.Errorf("expected section 3 heading in output")
	}
}

func TestDecomposeResearchGoalIntoSections(t *testing.T) {
	goal := "Conduct comprehensive research on durable execution engines in 2024-2025 focusing on Temporal, Restate, and Inngest. Compare architecture, SDKs, and pricing."

	if !IsMultiSectionResearchGoal(goal) {
		t.Fatalf("expected goal to be recognized as multi-section research goal")
	}

	sections := DecomposeResearchGoalIntoSections(goal)
	if len(sections) < 3 {
		t.Fatalf("expected at least 3 sections, got %d", len(sections))
	}

	hasTableSection := false
	for _, sec := range sections {
		if sec.IsTable {
			hasTableSection = true
		}
	}
	if !hasTableSection {
		t.Fatalf("expected at least one dedicated table section")
	}
}

func TestExecuteSectionedSynthesis(t *testing.T) {
	goal := "Conduct comprehensive research on durable execution engines in 2024-2025 focusing on Temporal, Restate, and Inngest."
	refinedContext := "Temporal is built on event sourcing. Restate is built on durable execution logs. Inngest is step-based."

	sections := DecomposeResearchGoalIntoSections(goal)
	engine := &mockSectionInferenceEngine{}

	synth, err := ExecuteSectionedSynthesis(context.Background(), goal, refinedContext, sections, engine)
	if err != nil {
		t.Fatalf("ExecuteSectionedSynthesis failed: %v", err)
	}

	if !strings.Contains(synth, "# Comparative Architectural Analysis of Durable Execution Engines") {
		t.Errorf("expected main document title, got:\n%s", synth)
	}

	if !strings.Contains(synth, "| System | Architecture | Language SDKs | Production Users |") {
		t.Errorf("expected comparison matrix table, got:\n%s", synth)
	}

	if !strings.Contains(synth, "## 3. Cost Arbitrage, Pricing & Economics") {
		t.Errorf("expected cost arbitrage section, got:\n%s", synth)
	}
}

func TestRankEvidenceForSource_RespectsConfigurableK(t *testing.T) {
	card := EvidenceCard{
		URL:   "https://ollama.com",
		Title: "Ollama Local AI",
		KeyFacts: []string{
			"Ollama allows running GGUF models on CPU and GPU with simple CLI.",
			"Throughput reaches 85 tokens per second on Apple M-series silicon.",
			"Memory footprint is bounded by 4-bit and 8-bit quantized weights.",
			"Supports OpenAI compatible REST API endpoints on port 11434.",
			"Configurable context windows up to 128k tokens with flash attention.",
			"Integrates with Open-WebUI, LiteLLM, and LangChain frameworks.",
			"Released version 0.5.4 with enhanced multi-modal support.",
			"Zero cloud telemetry and private local inference execution.",
			"Additional fact 9 that exceeds default limit.",
			"Additional fact 10 that exceeds default limit.",
		},
	}

	goal := "Evaluate local LLM inference engines focusing on Ollama throughput and API compatibility."

	// Test with K = 3
	table := RankEvidenceForSource(context.Background(), goal, card, 1, 3)
	if table.SourceIndex != 1 {
		t.Errorf("expected SourceIndex 1, got %d", table.SourceIndex)
	}
	if table.Title != "Ollama Local AI" {
		t.Errorf("expected Title 'Ollama Local AI', got %q", table.Title)
	}
	if len(table.RawSnippets) != 3 {
		t.Errorf("expected exactly 3 snippets with K=3, got %d", len(table.RawSnippets))
	}
	if len(table.KeyMetrics) == 0 {
		t.Errorf("expected key metrics to be extracted (e.g. 85 tokens per second, port 11434)")
	}

	// Test default K = 8 when k=0
	tableDefault := RankEvidenceForSource(context.Background(), goal, card, 2, 0)
	if len(tableDefault.RawSnippets) != 8 {
		t.Errorf("expected default 8 snippets with K=0, got %d", len(tableDefault.RawSnippets))
	}
}

type mockOutlineEngine struct {
	jsonResponse string
}

func (m *mockOutlineEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	if m.jsonResponse != "" {
		return m.jsonResponse, nil
	}
	return `{
		"title": "Comparative Evaluation of Local Inference Engines",
		"sections": [
			{
				"heading": "## 1. Engine Architectures",
				"objective": "Break down llama.cpp and Ollama mechanics",
				"target_source_ids": [1, 2],
				"format_hint": "prose",
				"is_terminal": false
			},
			{
				"heading": "## 2. Benchmark Comparison Matrix",
				"objective": "Compare tokens/sec and memory footprints",
				"target_source_ids": [1, 2, 3],
				"format_hint": "table",
				"is_terminal": false
			},
			{
				"heading": "## 3. Production Recommendations",
				"objective": "Provide deployment guidelines",
				"target_source_ids": [],
				"format_hint": "bulleted_deep_dive",
				"is_terminal": true
			}
		]
	}`, nil
}

func (m *mockOutlineEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	return "", nil
}

func TestGenerateSynthesisOutline_DynamicSectionsAndTerminalInflation(t *testing.T) {
	evidence := []EvidenceTable{
		{SourceIndex: 1, Title: "Ollama Docs", URL: "https://ollama.com", KeyMetrics: []string{"85 tok/s"}},
		{SourceIndex: 2, Title: "vLLM Benchmarks", URL: "https://vllm.ai", KeyMetrics: []string{"320 tok/s"}},
		{SourceIndex: 3, Title: "SGLang Repo", URL: "https://sglang.ai", KeyMetrics: []string{"RadixAttention"}},
	}

	goal := "Compare Ollama, vLLM, and SGLang performance and architectures."

	// Test 1: Successful GBNF outline generation
	engine := &mockOutlineEngine{}
	outline, err := GenerateSynthesisOutline(context.Background(), engine, goal, evidence)
	if err != nil {
		t.Fatalf("expected successful outline generation, got: %v", err)
	}

	if len(outline.Sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(outline.Sections))
	}
	if !outline.Sections[2].IsTerminal {
		t.Errorf("expected final section to be marked terminal")
	}

	// Test 2: Fallback when LLM outputs invalid JSON
	badEngine := &mockOutlineEngine{jsonResponse: "I am unable to generate JSON"}
	fallbackOutline, err := GenerateSynthesisOutline(context.Background(), badEngine, goal, evidence)
	if err != nil {
		t.Fatalf("expected graceful fallback on bad JSON, got error: %v", err)
	}
	if len(fallbackOutline.Sections) < 3 {
		t.Errorf("expected fallback to create at least 3 sections, got %d", len(fallbackOutline.Sections))
	}
	if !fallbackOutline.Sections[len(fallbackOutline.Sections)-1].IsTerminal {
		t.Errorf("expected terminal section to be true in fallback")
	}
}

type mockPipelineInferenceEngine struct {
	sectionCalls int
}

func (m *mockPipelineInferenceEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	// Outline generation
	if strings.Contains(systemPrompt, "research architect") {
		return `{
			"title": "Comprehensive Evaluation of Local Inference Engines",
			"sections": [
				{
					"heading": "## 1. Engine Architectures",
					"objective": "Detail llama.cpp and Ollama mechanics",
					"target_source_ids": [1],
					"format_hint": "prose",
					"is_terminal": false
				},
				{
					"heading": "## 2. Comparative Matrix",
					"objective": "Compare tokens/sec",
					"target_source_ids": [1, 2],
					"format_hint": "table",
					"is_terminal": false
				},
				{
					"heading": "## 3. Deployment Conclusions",
					"objective": "Synthesize recommendations across all engines",
					"target_source_ids": [],
					"format_hint": "bulleted_deep_dive",
					"is_terminal": true
				}
			]
		}`, nil
	}

	m.sectionCalls++
	if strings.Contains(systemPrompt, "Current Section: ## 1.") || strings.Contains(userPrompt, "Target Section: Section 1") || strings.Contains(userPrompt, "Synthesize Section 1") {
		return "## 1. Engine Architectures\n\nOllama provides a simplified CLI interface on port 11434 [1]. Llama.cpp provides raw C++ performance bindings [1].", nil
	}
	if strings.Contains(systemPrompt, "Current Section: ## 2.") || strings.Contains(userPrompt, "Target Section: Section 2") || strings.Contains(userPrompt, "Synthesize Section 2") {
		return "## 2. Comparative Matrix\n\n| Engine | Throughput | API |\n| :--- | :--- | :--- |\n| Ollama | 85 tok/s [1] | REST 11434 [1] |\n| vLLM | 320 tok/s [2] | OpenAI API [2] |", nil
	}

	// Terminal section
	return "## 3. Deployment Conclusions\n\nBased on all evaluated engines [1], [2], Ollama is optimal for developer workstations while vLLM is ideal for cluster serving.", nil
}

func (m *mockPipelineInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	var sys, user string
	for _, msg := range messages {
		if msg.Role == "system" {
			sys = msg.Content
		} else if msg.Role == "user" {
			user += "\n" + msg.Content
		}
	}
	return m.Infer(ctx, sys, user, jsonSchema, target)
}

func TestRunSectionedSynthesisPipeline_AssemblesAllSections(t *testing.T) {
	cards := []EvidenceCard{
		{
			URL:      "https://ollama.com",
			Title:    "Ollama Docs",
			KeyFacts: []string{"Ollama runs on port 11434 with 85 tok/s throughput on M-series chips."},
		},
		{
			URL:      "https://vllm.ai",
			Title:    "vLLM Benchmarks",
			KeyFacts: []string{"vLLM delivers 320 tok/s throughput for continuous batching on H100."},
		},
	}

	goal := "Compare Ollama and vLLM performance and architectures."
	engine := &mockPipelineInferenceEngine{}

	doc, err := RunSectionedSynthesisPipeline(context.Background(), engine, goal, cards)
	if err != nil {
		t.Fatalf("RunSectionedSynthesisPipeline failed: %v", err)
	}

	if engine.sectionCalls != 3 {
		t.Errorf("expected 3 section assembly calls, got %d", engine.sectionCalls)
	}

	if !strings.Contains(doc, "# Comprehensive Evaluation of Local Inference Engines") {
		t.Errorf("expected document title, got:\n%s", doc)
	}

	if !strings.Contains(doc, "## 1. Engine Architectures") {
		t.Errorf("expected section 1 heading, got:\n%s", doc)
	}

	if !strings.Contains(doc, "| Engine | Throughput | API |") {
		t.Errorf("expected section 2 comparison table, got:\n%s", doc)
	}

	if !strings.Contains(doc, "## 3. Deployment Conclusions") {
		t.Errorf("expected section 3 heading, got:\n%s", doc)
	}

	if !strings.Contains(doc, "## Verified Sources & Citations") {
		t.Errorf("expected bibliography table appended, got:\n%s", doc)
	}
}

func TestRemapAndGroundCitations_RemapsOutOfBoundsAndBindsMetrics(t *testing.T) {
	evidence := []EvidenceTable{
		{
			SourceIndex: 1,
			Title:       "Ollama Local Inference",
			URL:         "https://ollama.com",
			KeyMetrics:  []string{"85 tok/s", "port 11434"},
			RawSnippets: []string{"Ollama runs lightweight models on Apple silicon with 85 tok/s throughput on port 11434."},
		},
		{
			SourceIndex: 2,
			Title:       "vLLM High-Throughput Serving",
			URL:         "https://vllm.ai",
			KeyMetrics:  []string{"320 tok/s", "PagedAttention"},
			RawSnippets: []string{"vLLM delivers PagedAttention with 320 tok/s throughput on NVIDIA H100 GPUs."},
		},
	}

	draft := `# Local AI Inference Report

## 1. Engine Architectures
Ollama provides local developer inference on port 11434 [1].
vLLM utilizes PagedAttention for high-throughput batching [9].

## 2. Quantitative Benchmarks
Ollama delivers throughput reaching 85 tok/s on local hardware.
vLLM delivers throughput reaching 320 tok/s on data center clusters [2].`

	// Remap out-of-bounds [9] to [2] and auto-bind 85 tok/s to [1]
	processed := RemapAndGroundCitations(context.Background(), draft, evidence, 0.45, 0.50)

	// [9] should be remapped to [2] because of PagedAttention matching vLLM
	if strings.Contains(processed, "[9]") {
		t.Errorf("expected out-of-bounds tag [9] to be remapped or removed, got:\n%s", processed)
	}
	if !strings.Contains(processed, "PagedAttention for high-throughput batching [2]") {
		t.Errorf("expected [9] to be remapped to [2], got:\n%s", processed)
	}

	// 85 tok/s should be auto-bound to [1]
	if !strings.Contains(processed, "85 tok/s on local hardware [1]") {
		t.Errorf("expected 85 tok/s to be auto-bound with [1], got:\n%s", processed)
	}

	// Bibliography should be appended
	if !strings.Contains(processed, "## Verified Sources & Citations") {
		t.Errorf("expected bibliography table appended, got:\n%s", processed)
	}
	if !strings.Contains(processed, "| [1] | Ollama Local Inference | https://ollama.com |") {
		t.Errorf("expected source [1] row in bibliography, got:\n%s", processed)
	}
	if !strings.Contains(processed, "| [2] | vLLM High-Throughput Serving | https://vllm.ai |") {
		t.Errorf("expected source [2] row in bibliography, got:\n%s", processed)
	}
}

func TestPartitionDocGenContext(t *testing.T) {
	refinedCtx := `### File: internal/inference/backend.go
type InferenceBackend interface {
    CallModel(ctx context.Context, messages []InferenceMessage, jsonSchema string) (string, error)
}

### File: internal/inference/routing.go
func CallRouter(ctx context.Context, req Request) (*Response, error) {
    // routing logic
}

### File: internal/inference/thermal.go
type ThermalState int
type TokenTracker struct {
    LocalTokens int64
}`

	syms := []symbols.Symbol{
		{Name: "InferenceBackend", Kind: "interface", File: "internal/inference/backend.go", Signature: "type InferenceBackend interface"},
		{Name: "CallRouter", Kind: "func", File: "internal/inference/routing.go", Signature: "func CallRouter(ctx context.Context, req Request) (*Response, error)"},
		{Name: "ThermalState", Kind: "type", File: "internal/inference/thermal.go", Signature: "type ThermalState int"},
		{Name: "TokenTracker", Kind: "type", File: "internal/inference/thermal.go", Signature: "type TokenTracker struct"},
	}

	// Case 1: Query specifically for routing
	specRouting := SectionSpec{
		Heading:    "## 2. Routing Mechanics",
		Objective:  "Document CallRouter dispatch flows and dual-sidecar routing",
		FormatHint: "prose",
		IsTerminal: false,
	}
	ctxRouting, symsRouting := PartitionDocGenContext(refinedCtx, syms, specRouting, nil, 40000)

	if !strings.Contains(ctxRouting, "internal/inference/routing.go") {
		t.Errorf("expected routing context to contain routing.go, got:\n%s", ctxRouting)
	}
	if !strings.Contains(symsRouting, "CallRouter") {
		t.Errorf("expected routing symbols to contain CallRouter, got:\n%s", symsRouting)
	}

	// Case 2: Query for support/telemetry
	specSupport := SectionSpec{
		Heading:    "## 3. Support Subsystems & Thermal",
		Objective:  "Document ThermalState and TokenTracker telemetry",
		FormatHint: "prose",
		IsTerminal: false,
	}
	ctxSupport, symsSupport := PartitionDocGenContext(refinedCtx, syms, specSupport, nil, 40000)

	if !strings.Contains(ctxSupport, "internal/inference/thermal.go") {
		t.Errorf("expected support context to contain thermal.go, got:\n%s", ctxSupport)
	}
	if !strings.Contains(symsSupport, "ThermalState") || !strings.Contains(symsSupport, "TokenTracker") {
		t.Errorf("expected support symbols to contain ThermalState and TokenTracker, got:\n%s", symsSupport)
	}
}

func TestBuildDocGenSafetyFloorOutline_FunctionIndex(t *testing.T) {
	goal := "Generate an exhaustive function index for internal/cache/ listing EVERY exported function, every exported method on types, and every exported type."
	refinedCtx := "cache.go defines CacheEnvelope. metrics.go tracks metrics. query.go executes SQL."

	outline := BuildDocGenSafetyFloorOutline(goal, refinedCtx, nil)
	if outline == nil || len(outline.Sections) < 3 {
		t.Fatalf("expected function index safety floor to produce at least 3 sections, got %v", outline)
	}

	// Ensure headings reflect function index, NOT architectural overview
	hasTypesSec := false
	hasFuncsSec := false
	for _, s := range outline.Sections {
		if strings.Contains(s.Heading, "Types") || strings.Contains(s.Heading, "Interfaces") {
			hasTypesSec = true
		}
		if strings.Contains(s.Heading, "Functions") {
			hasFuncsSec = true
		}
	}
	if !hasTypesSec || !hasFuncsSec {
		t.Errorf("expected function index outline to contain Types and Functions sections, got: %v", outline.Sections)
	}
}

func TestExecuteDocGenSectionedSynthesis_InsideOutSandwichOrder(t *testing.T) {
	goal := "Generate module documentation for internal/inference/"
	refinedCtx := `### File: internal/inference/backend.go
type InferenceBackend interface {}

### File: internal/inference/routing.go
func CallRouter() {}`

	outline := &SynthesisOutline{
		Title: "Inference Engine Architecture",
		Sections: []SectionSpec{
			{Heading: "## 1. Overview & Architecture", Objective: "Overview of the module", IsInitial: true},
			{Heading: "## 2. Core Backends", Objective: "Detail backend interfaces", IsInitial: false, IsTerminal: false},
			{Heading: "## 3. Routing Layer", Objective: "Detail CallRouter", IsInitial: false, IsTerminal: false},
			{Heading: "## 4. Symbol Index", Objective: "Index all symbols", IsTerminal: true},
		},
	}

	var callOrder []string
	trackingEngine := &callTrackingEngine{
		responses: map[string]string{
			"## 1. Overview & Architecture": "## 1. Overview & Architecture\nSynthesized intro based on drafted body.",
			"## 2. Core Backends":           "## 2. Core Backends\nBackend implementation details.",
			"## 3. Routing Layer":           "## 3. Routing Layer\nRouting mechanics details.",
			"## 4. Symbol Index":            "## 4. Symbol Index\nSymbol reference table.",
		},
		onInfer: func(systemPrompt, userPrompt string) {
			for _, heading := range []string{"## 1. Overview & Architecture", "## 2. Core Backends", "## 3. Routing Layer", "## 4. Symbol Index"} {
				if strings.Contains(userPrompt, "Synthesize Section: "+heading) || strings.Contains(userPrompt, heading) {
					callOrder = append(callOrder, heading)
					break
				}
			}
		},
	}

	doc, err := ExecuteDocGenSectionedSynthesis(context.Background(), goal, refinedCtx, outline, nil, trackingEngine)
	if err != nil {
		t.Fatalf("ExecuteDocGenSectionedSynthesis failed: %v", err)
	}

	// Verify Inside-Out execution order: Body sections (2 & 3) executed BEFORE Overview (1) and Symbol Index (4)
	if len(callOrder) < 4 {
		t.Fatalf("expected 4 section calls, got %d: %v", len(callOrder), callOrder)
	}

	// Check that Section 2 and 3 were called before Section 1
	sec1Idx := -1
	sec2Idx := -1
	sec3Idx := -1
	sec4Idx := -1
	for i, h := range callOrder {
		switch {
		case strings.Contains(h, "## 1."):
			sec1Idx = i
		case strings.Contains(h, "## 2."):
			sec2Idx = i
		case strings.Contains(h, "## 3."):
			sec3Idx = i
		case strings.Contains(h, "## 4."):
			sec4Idx = i
		}
	}

	if sec2Idx > sec1Idx || sec3Idx > sec1Idx {
		t.Errorf("expected body sections (2 and 3) to execute BEFORE overview section (1). Order: %v", callOrder)
	}
	if sec4Idx < sec2Idx || sec4Idx < sec3Idx {
		t.Errorf("expected terminal section (4) to execute AFTER body sections (2 and 3). Order: %v", callOrder)
	}

	// Final document should still be assembled in canonical order 1 -> 2 -> 3 -> 4
	if !strings.Contains(doc, "## 1. Overview & Architecture") ||
		!strings.Contains(doc, "## 2. Core Backends") ||
		!strings.Contains(doc, "## 3. Routing Layer") ||
		!strings.Contains(doc, "## 4. Symbol Index") {
		t.Errorf("expected all 4 sections in final assembled doc, got:\n%s", doc)
	}
}

type callTrackingEngine struct {
	responses map[string]string
	onInfer   func(systemPrompt, userPrompt string)
}

func (c *callTrackingEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	if c.onInfer != nil {
		c.onInfer(systemPrompt, userPrompt)
	}
	// Check userPrompt first to avoid matching headings embedded in body context
	for k, v := range c.responses {
		if strings.Contains(userPrompt, "Synthesize Section: "+k) || strings.Contains(userPrompt, k) {
			return v, nil
		}
	}
	for k, v := range c.responses {
		if strings.Contains(systemPrompt, k) {
			return v, nil
		}
	}
	return "Section content", nil
}

func (c *callTrackingEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	return "", nil
}

type prefixTrackingInferenceEngine struct {
	capturedCalls [][]inference.InferenceMessage
}

func (p *prefixTrackingInferenceEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	return "## Mock Section Output\nContent", nil
}

func (p *prefixTrackingInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	p.capturedCalls = append(p.capturedCalls, messages)
	return "## Mock Section Output\nContent", nil
}

func TestSynthesizeSection_StaticPrefixSlotting(t *testing.T) {
	mockEngine := &prefixTrackingInferenceEngine{}

	goal := "Evaluate GPU context prefill performance"
	evidence := []EvidenceTable{
		{SourceIndex: 1, Title: "SplitZip Paper", URL: "https://example.com/splitzip"},
	}
	spec1 := SectionSpec{Heading: "## 1. Physical Codecs", Objective: "Explain SplitZip"}
	spec2 := SectionSpec{Heading: "## 2. Symbolic Codecs", Objective: "Explain Meta-Tokens"}
	outline := &SynthesisOutline{Title: "Prefix Study", Sections: []SectionSpec{spec1, spec2}}

	// Synthesize Section 1
	_, err := AssembleSection(context.Background(), mockEngine, goal, outline, 0, spec1, evidence, nil)
	if err != nil {
		t.Fatalf("AssembleSection 1 failed: %v", err)
	}

	// Synthesize Section 2
	_, err = AssembleSection(context.Background(), mockEngine, goal, outline, 1, spec2, evidence, []string{"Lead 1"})
	if err != nil {
		t.Fatalf("AssembleSection 2 failed: %v", err)
	}

	if len(mockEngine.capturedCalls) != 2 {
		t.Fatalf("Expected 2 calls to InferMessages, got %d", len(mockEngine.capturedCalls))
	}

	call1 := mockEngine.capturedCalls[0]
	call2 := mockEngine.capturedCalls[1]

	if len(call1) != 4 || len(call2) != 4 {
		t.Fatalf("Expected 4 messages per call, got call1=%d, call2=%d", len(call1), len(call2))
	}

	// Verify Turns 1-3 are byte-for-byte identical between section 1 and section 2 (KV cache prefix reuse)
	if call1[0].Content != call2[0].Content {
		t.Errorf("Turn 1 (system) mismatch between sections!\nCall1: %s\nCall2: %s", call1[0].Content, call2[0].Content)
	}
	if call1[1].Content != call2[1].Content {
		t.Errorf("Turn 2 (goal) mismatch between sections!\nCall1: %s\nCall2: %s", call1[1].Content, call2[1].Content)
	}
	if call1[2].Content != call2[2].Content {
		t.Errorf("Turn 3 (ack) mismatch between sections!\nCall1: %s\nCall2: %s", call1[2].Content, call2[2].Content)
	}

	// Verify Turn 4 contains the dynamic section heading and content
	if !strings.Contains(call1[3].Content, spec1.Heading) {
		t.Errorf("Turn 4 for Section 1 missing heading %q", spec1.Heading)
	}
	if !strings.Contains(call2[3].Content, spec2.Heading) {
		t.Errorf("Turn 4 for Section 2 missing heading %q", spec2.Heading)
	}
}

func TestBuildDocGenSafetyFloorOutline_DynamicSplit_ADRSummary(t *testing.T) {
	goal := "Read all ADR files in docs/adr/ and produce a consolidated decision log with status, date, and key implications for each decision. Focus on technical accuracy. Every ADR must be included — there are 37+ ADR files."
	var ctxBuilder strings.Builder
	for i := 1; i <= 37; i++ {
		ctxBuilder.WriteString(fmt.Sprintf("### docs/adr/%04d-adr-topic-%d.md\nStatus: Accepted\nDate: 2026-08-01\nContext: Issue %d\nDecision: Decision %d\nImplications: Key item %d\n\n", i, i, i, i, i))
	}
	refinedCtx := ctxBuilder.String()

	outline := BuildDocGenSafetyFloorOutline(goal, refinedCtx, nil)
	if outline == nil {
		t.Fatalf("expected outline, got nil")
	}

	// 37 ADRs batched at 8 per batch = 5 body batches + 1 initial (overview) + 1 terminal (cross-cutting) = 7 sections
	if len(outline.Sections) < 5 {
		t.Fatalf("expected at least 5 partitioned sections for 37 ADRs, got %d sections: %v", len(outline.Sections), outline.Sections)
	}

	if !outline.Sections[0].IsInitial {
		t.Errorf("expected section 0 to be initial overview")
	}
	if !outline.Sections[len(outline.Sections)-1].IsTerminal {
		t.Errorf("expected last section to be terminal cross-cutting synthesis")
	}

	// Ensure body sections mention the partitioned ADR range in heading or objective
	hasBatchHeading := false
	for _, sec := range outline.Sections[1 : len(outline.Sections)-1] {
		if strings.Contains(sec.Heading, "Consolidated Decision Records") {
			hasBatchHeading = true
			if !strings.Contains(sec.Heading, "to") && !strings.Contains(sec.Objective, "Specifically cover") {
				t.Errorf("expected body section to list target ADRs, got heading=%s, obj=%s", sec.Heading, sec.Objective)
			}
		}
	}
	if !hasBatchHeading {
		t.Errorf("expected body sections with Consolidated Decision Records, got %v", outline.Sections)
	}
}

func TestBuildDocGenSafetyFloorOutline_DynamicSplit_ModuleReference(t *testing.T) {
	goal := "Explore all source files in internal/inference/ directory and generate module documentation covering all public types across all layers."
	files := []string{
		"internal/inference/backend.go",
		"internal/inference/backend_llama.go",
		"internal/inference/backend_remote.go",
		"internal/inference/local_model.go",
		"internal/inference/model_catalog.go",
		"internal/inference/routing.go",
		"internal/inference/routing_dual.go",
		"internal/inference/prefill.go",
		"internal/inference/metrics.go",
		"internal/inference/thermal.go",
		"internal/inference/token_tracker.go",
	}
	var ctxBuilder strings.Builder
	for _, f := range files {
		ctxBuilder.WriteString(fmt.Sprintf("### File: %s\npackage inference\ntype TypeFor_%s struct {}\n\n", f, filepathBase(f)))
	}
	refinedCtx := ctxBuilder.String()

	outline := BuildDocGenSafetyFloorOutline(goal, refinedCtx, nil)
	if outline == nil {
		t.Fatalf("expected outline, got nil")
	}

	// 11 files batched at 3 per batch = 4 body batches + 1 initial + 1 terminal = 6 sections
	if len(outline.Sections) < 5 {
		t.Fatalf("expected at least 5 partitioned sections for 11 source files, got %d sections: %v", len(outline.Sections), outline.Sections)
	}

	if !outline.Sections[0].IsInitial {
		t.Errorf("expected section 0 to be initial overview")
	}
	if !outline.Sections[len(outline.Sections)-1].IsTerminal {
		t.Errorf("expected last section to be terminal reference")
	}
}

func TestBuildDocGenSafetyFloorOutline_DynamicSplit_16kChars(t *testing.T) {
	goal := "Document internal architecture decisions and technical specifications."
	// Single block with >16k characters
	refinedCtx := strings.Repeat("Detailed architecture design decisions and specifications across subsystems.\n", 300)
	if len(refinedCtx) <= 16000 {
		t.Fatalf("test precondition failed: len(refinedCtx) = %d <= 16000", len(refinedCtx))
	}

	outline := BuildDocGenSafetyFloorOutline(goal, refinedCtx, nil)
	if outline == nil || len(outline.Sections) < 3 {
		t.Fatalf("expected valid outline with >= 3 sections for >16k chars context, got: %v", outline)
	}
}

func TestBuildDocGenSafetyFloorOutline_NoSplit_SmallContext(t *testing.T) {
	goal := "Synthesize module architecture and component interactions for the cache subsystem."
	refinedCtx := "### File: internal/cache/cache.go\ntype CacheStore interface {}\n### File: internal/cache/metrics.go\nfunc GetMetrics()\n"

	outline := BuildDocGenSafetyFloorOutline(goal, refinedCtx, nil)
	if outline == nil {
		t.Fatalf("expected outline, got nil")
	}

	// 2 files and < 16k chars -> static outline (3 sections for module_reference)
	if len(outline.Sections) != 3 {
		t.Fatalf("expected standard 3-section outline for small context (2 files), got %d sections: %v", len(outline.Sections), outline.Sections)
	}
}

func TestExecuteDirectChunkSummarization(t *testing.T) {
	goal := "Read all ADR files in docs/adr/ and produce a consolidated decision log."
	sec := SectionSpec{
		Heading:   "## 2. Consolidated Decision Records (0001 to 0003)",
		Objective: "Extract structured decision records for ADR 0001, 0002, 0003.",
	}
	secCtx := `### docs/adr/0001-durable-go-dag-executor.md
Title: ADR 0001 Durable DAG
Status: Accepted
Date: 2026-07-29
Context: Durable DAG needed.
Decision: Implemented Kahn compiler in Go.
Implications: Deterministic crash recovery.

### docs/adr/0002-local-gbnf-constraints.md
Title: ADR 0002 GBNF Constraints
Status: Accepted
Date: 2026-07-29
Context: Constrain local model.
Decision: Use GBNF grammars.
Implications: Eliminates invalid JSON output.

### docs/adr/0003-proactive-observer-agent.md
Title: ADR 0003 Observer Agent
Status: Accepted
Date: 2026-07-29
Context: Proactive memory capture.
Decision: Async observer thread.
Implications: Continuous reflection without blocking.`

	mockEngine := &mockSectionInferenceEngine{
		responses: map[string]string{
			"0001": "### ADR-0001: Durable DAG Compiler\n- **Status**: Accepted\n- **Date**: 2026-07-29\n- **Context**: Need durable DAG\n- **Decision**: Go Kahn compiler\n- **Technical Implications**: Resilient execution",
			"0002": "### ADR-0002: Local GBNF Constraints\n- **Status**: Accepted\n- **Date**: 2026-07-29\n- **Context**: Constrain 4B model\n- **Decision**: GBNF grammar constraints\n- **Technical Implications**: Safe JSON generation",
			"0003": "### ADR-0003: Proactive Observer Agent\n- **Status**: Accepted\n- **Date**: 2026-07-29\n- **Context**: Memory reflection\n- **Decision**: Async observer\n- **Technical Implications**: Background reflection",
		},
	}

	result, err := ExecuteDirectChunkSummarization(context.Background(), goal, sec, secCtx, mockEngine)
	if err != nil {
		t.Fatalf("ExecuteDirectChunkSummarization failed: %v", err)
	}

	if !strings.HasPrefix(result, sec.Heading) {
		t.Errorf("expected result to start with section heading %q, got:\n%s", sec.Heading, result)
	}

	for _, expected := range []string{"ADR-0001", "ADR-0002", "ADR-0003"} {
		if !strings.Contains(result, expected) {
			t.Errorf("expected result to contain %q, got:\n%s", expected, result)
		}
	}
}




