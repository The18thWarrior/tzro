package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"tzro/internal/inference"
	"tzro/internal/memory"
)

func TestClassifyHeuristic(t *testing.T) {
	// With both sidecars stopped, Classify falls back to "chat" for all prompts
	// (no keyword-based heuristic fallback exists — the function returns chat as default).
	inference.GlobalRouterModel.Status = "Stopped"
	inference.GlobalWorkerModel.Status = "Stopped"

	ctx := context.Background()

	// 1. Ambiguous intent should default to chat
	res1 := Classify(ctx, "tell me a story")
	if res1.Type != "chat" {
		t.Errorf("expected chat, got %s", res1.Type)
	}

	// 2. Scheduled/cron text also falls back to chat when inference unavailable
	res2 := Classify(ctx, "run every 5 minutes: check uptime")
	if res2.Type != "chat" {
		t.Errorf("expected chat fallback when sidecars stopped, got %s", res2.Type)
	}

	// 3. Research keywords also fall back to chat when inference unavailable
	res3 := Classify(ctx, "analyze system performance logs")
	if res3.Type != "chat" {
		t.Errorf("expected chat fallback when sidecars stopped, got %s", res3.Type)
	}
}

func TestClassifyLLM(t *testing.T) {
	// Mock local llama server chat/completions endpoint
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"type\":\"research\",\"confidence\":0.95,\"summary\":\"Deep research lookup\",\"params\":{\"query\":\"test\",\"depth\":\"standard\"}}"
				}
			}],
			"usage": {
				"prompt_tokens": 20,
				"completion_tokens": 15
			}
		}`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Classify goes through ExecuteRouterStructured → GlobalRouterModel.
	// Point the router at the mock server.
	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := server.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	res := Classify(ctx, "research tzro project")

	if res.Type != "research" {
		t.Errorf("expected research, got %s", res.Type)
	}
	if res.Confidence != 0.95 {
		t.Errorf("expected 0.95 confidence, got %f", res.Confidence)
	}
}

func TestClassifyComplexityLLM(t *testing.T) {
	// Mock local llama server for complexity check
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"complexity\":\"T1\"}"
				}
			}]
		}`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	savedStatus := inference.GlobalRouterModel.Status
	savedPort := inference.GlobalRouterModel.ActivePort
	defer func() {
		inference.GlobalRouterModel.Status = savedStatus
		inference.GlobalRouterModel.ActivePort = savedPort
	}()

	listenerAddr := server.Listener.Addr().String()
	_, portStr, _ := netSplitHostPort(listenerAddr)
	inference.GlobalRouterModel.ActivePort = parseInt(portStr)
	inference.GlobalRouterModel.Status = "Active"

	ctx := context.Background()
	prompt := "do something complex with salesforce and postgres"
	complexity := ClassifyComplexity(ctx, prompt, []string{"salesforce_query", "postgres_insert"})

	if complexity != "T1" {
		t.Errorf("expected T1 complexity, got %s", complexity)
	}
}

func TestClassifyComplexityHeuristicFallback(t *testing.T) {
	// With sidecars stopped, ClassifyComplexity falls back to T0 by default.
	// Only workflow promotion (tool cap > 12 or semantic triggers) returns T2.
	inference.GlobalRouterModel.Status = "Stopped"
	inference.GlobalWorkerModel.Status = "Stopped"

	ctx := context.Background()

	// Short request -> T0 (default fallback)
	c1 := ClassifyComplexity(ctx, "hello there", []string{"some_tool"})
	if c1 != "T0" {
		t.Errorf("expected T0 for short query, got %s", c1)
	}

	// Bulk keywords also fall back to T0 when inference is unavailable
	c2 := ClassifyComplexity(ctx, "bulk delete records from database", []string{"some_tool"})
	if c2 != "T0" {
		t.Errorf("expected T0 fallback for bulk query without sidecar, got %s", c2)
	}
}

// Helpers for testing
func netSplitHostPort(addr string) (string, string, error) {
	parts := parseAddrString(addr)
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return "", "", nil
}

func parseAddrString(addr string) []string {
	idx := len(addr) - 1
	for idx >= 0 {
		if addr[idx] == ':' {
			return []string{addr[:idx], addr[idx+1:]}
		}
		idx--
	}
	return []string{addr}
}

func parseInt(s string) int {
	var val int
	for _, char := range s {
		if char >= '0' && char <= '9' {
			val = val*10 + int(char-'0')
		}
	}
	return val
}

func TestMain(m *testing.M) {
	testDB := "test_tzro_classifier.db"
	_ = os.Remove(testDB)
	memory.DB.SetDBPathForTesting(testDB)

	err := memory.DB.Init()
	if err != nil {
		panic(err)
	}

	code := m.Run()

	_ = memory.DB.Close()
	_ = os.Remove(testDB)
	os.Exit(code)
}

func TestTaskToWorkflowPromotionEngine_TemporalAndHITL(t *testing.T) {
	inference.GlobalRouterModel.Status = "Stopped"
	inference.GlobalWorkerModel.Status = "Stopped"
	ctx := context.Background()

	// 1. Semantic Temporal Delay trigger
	res1 := Classify(ctx, "Wait 3 days and notify me when Salesforce updates the status")
	if res1.Type != "workflow" {
		t.Errorf("expected workflow due to temporal wait trigger, got %s", res1.Type)
	}
	promoted1 := res1.Params["promoted"]
	if promoted1 != true {
		t.Errorf("expected promoted flag to be true")
	}

	// Verify decomposition for approval/wait
	var decomposedTasks []memory.WorkflowTask
	tasksJSON := res1.Params["tasks"].(string)
	if err := json.Unmarshal([]byte(tasksJSON), &decomposedTasks); err != nil {
		t.Fatalf("failed to unmarshal tasks: %v", err)
	}
	if len(decomposedTasks) != 3 {
		t.Errorf("expected 3 decomposed tasks for approval/wait workflow, got %d", len(decomposedTasks))
	}
	if decomposedTasks[0].TaskTemplateID != "prepare_sync" || decomposedTasks[1].Dependencies != "prepare_sync" {
		t.Errorf("invalid dependency decomposition: %+v", decomposedTasks)
	}

	// 2. ClassifyComplexity returns T2 for temporal triggers
	c1 := ClassifyComplexity(ctx, "Run every Monday at 9am: verify db integrity", []string{"postgres_insert"})
	if c1 != "T2" {
		t.Errorf("expected T2 for cron trigger prompt, got %s", c1)
	}
}

func TestTaskToWorkflowPromotionEngine_ToolCap(t *testing.T) {
	inference.GlobalRouterModel.Status = "Stopped"
	inference.GlobalWorkerModel.Status = "Stopped"
	ctx := context.Background()

	// Seed knowledge graph to simulate tool/skill neighborhood BFS > 12 tools
	// Tool 1: fetch_sheet_records
	// We'll connect it to 13 other tools and skills
	_ = memory.DB.AddNode(memory.KGNode{ID: "fetch_sheet_records", NodeType: "tool", Name: "Fetch Sheets"})

	// Let's add 13 nodes (making the neighborhood total 14 tools/skills)
	for i := 1; i <= 13; i++ {
		nodeID := fmt.Sprintf("helper_tool_%d", i)
		nodeType := "tool"
		if i%2 == 0 {
			nodeType = "skill"
		}
		_ = memory.DB.AddNode(memory.KGNode{ID: nodeID, NodeType: nodeType, Name: "Helper Node"})

		// Connect them
		edgeID := fmt.Sprintf("edge_h_%d", i)
		_ = memory.DB.AddEdge(memory.KGEdge{
			ID:       edgeID,
			EdgeType: "references",
			SourceID: "fetch_sheet_records",
			TargetID: nodeID,
			Weight:   1.0,
		})
	}

	// Now run Classify on a prompt that references the sheet records tool
	prompt := "Sync spreadsheet records using fetch_sheet_records and postgres"
	res := Classify(ctx, prompt)

	if res.Type != "workflow" {
		t.Errorf("expected promoted workflow due to BFS tool cap > 12, got %s", res.Type)
	}
	promoted := res.Params["promoted"]
	if promoted != true {
		t.Errorf("expected promoted flag to be true")
	}

	// Check complexity
	comp := ClassifyComplexity(ctx, prompt, []string{"fetch_sheet_records"})
	if comp != "T2" {
		t.Errorf("expected complexity promoted to T2 due to BFS tool cap, got %s", comp)
	}
}
