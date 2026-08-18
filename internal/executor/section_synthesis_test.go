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
