package executor

import (
	"context"
	"strings"
	"testing"

	"tzro/internal/compiler"
)

func TestResearchPhases_DeterministicSearchAndBrowse(t *testing.T) {
	var dispatchedTools []string

	ctx := context.WithValue(context.Background(), ToolDispatcherKey, func(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
		dispatchedTools = append(dispatchedTools, toolName)
		if toolName == "web_search" {
			query := args["query"].(string)
			return "Search results for " + query + ":\n- [Doc 1](https://example.com/doc1)\n- [Doc 2](https://example.com/doc2)", nil
		}
		if toolName == "web_browse" {
			url := args["url"].(string)
			return "Content from " + url, nil
		}
		return "ok", nil
	})

	mockEngine := NewMockPhaseEngine()
	// Mock 1-shot query generation
	mockEngine.PhaseResponses["queries"] = []MockPhaseStep{
		{Reasoning: `["tzro durable execution", "local model orchestration"]`},
	}
	// Mock 1-shot synthesis
	mockEngine.PhaseResponses["synthesize"] = []MockPhaseStep{
		{Reasoning: "Final research synthesis report"},
	}

	config := compiler.ProbeConfig{
		Goal: "Research tzro durable execution and local model orchestration",
	}

	synthesis, err := RunResearchPhases(ctx, "task_res_1", "probe_res_1", config, mockEngine, mockEngine, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if synthesis == "" {
		t.Errorf("expected non-empty synthesis")
	}

	// Verify dispatched tools: 2 web_search calls + 2 web_browse calls
	var searchCalls, browseCalls int
	for _, tool := range dispatchedTools {
		if tool == "web_search" {
			searchCalls++
		}
		if tool == "web_browse" {
			browseCalls++
		}
	}

	if searchCalls < 2 {
		t.Errorf("expected at least 2 web_search calls, got %d (dispatched: %v)", searchCalls, dispatchedTools)
	}
	if browseCalls < 1 {
		t.Errorf("expected at least 1 web_browse call on discovered URLs, got %d (dispatched: %v)", browseCalls, dispatchedTools)
	}
}

func TestResearchPhases_EntityPartitioning(t *testing.T) {
	goal := "Compare Temporal, Restate, and Inngest across architecture, language support, and deployment"
	
	candidates := []DiscoveredSearchResult{
		{URL: "https://docs.temporal.io/intro", Title: "Temporal Docs", Snippet: "Temporal durable workflows"},
		{URL: "https://docs.temporal.io/arch", Title: "Temporal Architecture", Snippet: "History service and workers"},
		{URL: "https://docs.temporal.io/best", Title: "Temporal Best Practices", Snippet: "Patterns for workflows"},
		{URL: "https://docs.restate.dev/overview", Title: "Restate Overview", Snippet: "Event-driven durable execution"},
		{URL: "https://inngest.com/docs", Title: "Inngest Docs", Snippet: "Serverless durable execution"},
		{URL: "https://markaicode.com/best-workflows-2026", Title: "Best 2026 workflows", Snippet: "SEO listicle summary"},
	}

	entities := extractTargetEntities(goal)
	if len(entities) < 3 {
		t.Fatalf("expected at least 3 target entities (Temporal, Restate, Inngest), got: %v", entities)
	}

	partitioned := PartitionDiscoveredURLsByEntity(candidates, entities, 6)
	if len(partitioned) < 3 {
		t.Fatalf("expected at least 3 partitioned URLs, got %d", len(partitioned))
	}

	// Verify multi-entity representation (at least 1 per entity)
	hasTemporal := false
	hasRestate := false
	hasInngest := false
	for _, res := range partitioned {
		u := strings.ToLower(res.URL)
		if strings.Contains(u, "temporal") {
			hasTemporal = true
		}
		if strings.Contains(u, "restate") {
			hasRestate = true
		}
		if strings.Contains(u, "inngest") {
			hasInngest = true
		}
	}

	if !hasTemporal || !hasRestate || !hasInngest {
		t.Errorf("expected URLs for Temporal, Restate, and Inngest. Got: %+v (temp=%v, res=%v, inng=%v)",
			partitioned, hasTemporal, hasRestate, hasInngest)
	}
}

func TestResearchPhases_DomainAuthorityScoring(t *testing.T) {
	docScore := calculateStructuralAuthority("https://docs.temporal.io/best-practices")
	repoScore := calculateStructuralAuthority("https://github.com/temporalio/temporal")
	seoScore := calculateStructuralAuthority("https://markaicode.com/best/best-local-llm-inference-tools-production-2026/")
	
	if docScore <= 1.0 {
		t.Errorf("expected docs URL to have authority boost > 1.0, got %f", docScore)
	}
	if repoScore <= 1.0 {
		t.Errorf("expected github repo URL to have authority boost > 1.0, got %f", repoScore)
	}
	if seoScore >= 1.0 {
		t.Errorf("expected SEO scraper URL to have authority penalty < 1.0, got %f", seoScore)
	}
}
