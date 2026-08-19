package executor

import (
	"context"
	"strings"
	"testing"

	"tzro/internal/inference"
)

type mockSectionInferenceEngine struct {
	responses map[string]string
}

func (m *mockSectionInferenceEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	if strings.Contains(systemPrompt, "Comparison Matrix") {
		return `## 2. Comparative Analysis Matrix & Benchmarks

| System | Architecture | Language SDKs | Production Users |
| :--- | :--- | :--- | :--- |
| Temporal | Event Sourcing / Replay | Go, TypeScript, Python, Java | Netflix, Stripe, Descript |
| Restate | Durable Async Functions | TypeScript, Java, Rust | Emerging |
| Inngest | Event-Driven Functions | TypeScript, Python, Go | SoundCloud |`, nil
	}

	if strings.Contains(systemPrompt, "Core Architectural Patterns") {
		return `## 1. Core Architectural Patterns & Mechanics

Temporal relies on event sourcing and deterministic replay through history events. Restate models execution as durable execution log RPCs. Inngest uses step-based event-driven execution.`, nil
	}

	if strings.Contains(systemPrompt, "Cost Arbitrage") {
		return `## 3. Cost Arbitrage, Pricing & Economics

Self-hosting Temporal requires Cassandra/PostgreSQL clusters ($200-$500/mo) vs Temporal Cloud starting with free credits and $0.000025 per action.`, nil
	}

	return `## 4. Decision Framework & Recommendations

Choose Temporal for mission-critical complex financial workflows. Choose Restate for lightweight low-latency RPCs. Choose Inngest for event-driven serverless background jobs.`, nil
}

func (m *mockSectionInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	return "", nil
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
	if strings.Contains(systemPrompt, "Current Section: ## 1.") || strings.Contains(userPrompt, "## 1.") {
		return "## 1. Engine Architectures\n\nOllama provides a simplified CLI interface on port 11434 [1]. Llama.cpp provides raw C++ performance bindings [1].", nil
	}
	if strings.Contains(systemPrompt, "Current Section: ## 2.") || strings.Contains(userPrompt, "## 2.") {
		return "## 2. Comparative Matrix\n\n| Engine | Throughput | API |\n| :--- | :--- | :--- |\n| Ollama | 85 tok/s [1] | REST 11434 [1] |\n| vLLM | 320 tok/s [2] | OpenAI API [2] |", nil
	}

	// Terminal section
	return "## 3. Deployment Conclusions\n\nBased on all evaluated engines [1], [2], Ollama is optimal for developer workstations while vLLM is ideal for cluster serving.", nil
}

func (m *mockPipelineInferenceEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	return "", nil
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
