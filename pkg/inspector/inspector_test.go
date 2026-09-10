package inspector_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tzro/pkg/dlp"
	"tzro/pkg/inspector"
	"tzro/pkg/store"
)

func TestInspector_SixStageTraceRecordingAndReplay(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspaces/my-repo"

	// Create a 6-stage context trace
	trace := &inspector.Trace{
		ID:        "trace_abc123",
		Workspace: ws,
		CreatedAt: time.Now().UTC(),
		Query:     "sliding window rate limiter",
		Config: inspector.TraceConfig{
			Budget:         200,
			PolicyVersion:  "1.0",
			IncludeGenCode: false,
		},
		Discovery: []inspector.CandidateTrace{
			{Path: "pkg/limiter/sliding.go", Hash: "hash1", SourceKind: "code", Score: 95.0, Tokens: 80},
			{Path: "pkg/limiter/sliding_test.go", Hash: "hash2", SourceKind: "code", Score: 85.0, Tokens: 90},
			{Path: "docs/design.md", Hash: "hash3", SourceKind: "doc", Score: 75.0, Tokens: 100},
			{Path: "vendor/external.go", Hash: "hash4", SourceKind: "code", Score: 60.0, Tokens: 50},
			{Path: "secrets.env", Hash: "hash5", SourceKind: "config", Score: 70.0, Tokens: 20},
		},
		Filtering: []inspector.FilterDecision{
			{Path: "vendor/external.go", Passed: false, Stage: "gitignore", Reason: "vendor directory ignored"},
			{Path: "secrets.env", Passed: false, Stage: "privacy", Reason: "denied by policy rule"},
			{Path: "pkg/limiter/sliding.go", Passed: true, Stage: "allow"},
			{Path: "pkg/limiter/sliding_test.go", Passed: true, Stage: "allow"},
			{Path: "docs/design.md", Passed: true, Stage: "allow"},
		},
		Ranking: []inspector.RankedCandidate{
			{Path: "pkg/limiter/sliding.go", Rank: 1, Score: 95.0, Precision: "precise", Relationship: "caller"},
			{Path: "pkg/limiter/sliding_test.go", Rank: 2, Score: 85.0, Precision: "syntactic", Relationship: "test"},
			{Path: "docs/design.md", Rank: 3, Score: 75.0, Precision: "syntactic", Relationship: "doc"},
		},
		Packing: inspector.PackingStage{
			TotalBudget:     200,
			BudgetRemaining: 30,
			IncludedItems: []inspector.PackedItem{
				{Path: "pkg/limiter/sliding.go", TokensUsed: 80, Tier: inspector.TierMeasured},
				{Path: "pkg/limiter/sliding_test.go", TokensUsed: 90, Tier: inspector.TierMeasured},
			},
			TruncatedManifest: []string{"docs/design.md"},
		},
		Transformation: []inspector.TransformDetail{
			{Path: "pkg/limiter/sliding.go", Action: "declaration_span", LinesElided: 45},
			{Path: "pkg/limiter/sliding_test.go", Action: "full_content", LinesElided: 0},
		},
		Policy: inspector.PolicyStage{
			PolicyVersion:   "1.0",
			RedactionsCount: 0,
			DenialsCount:    1,
		},
	}

	// 1. Record trace in store
	engine := inspector.NewEngine(s, nil)
	if err := engine.RecordTrace(trace); err != nil {
		t.Fatalf("RecordTrace failed: %v", err)
	}

	// 2. Explain omission of docs/design.md
	explanation, err := engine.ExplainCandidate("trace_abc123", ws, "docs/design.md")
	if err != nil {
		t.Fatalf("ExplainCandidate failed: %v", err)
	}

	if explanation.Stage != "packing" {
		t.Errorf("expected omission stage packing, got %s", explanation.Stage)
	}
	if !strings.Contains(explanation.Reason, "budget") {
		t.Errorf("expected explanation reason to mention budget, got: %s", explanation.Reason)
	}
	if explanation.Tier != inspector.TierMeasured {
		t.Errorf("expected TierMeasured, got %s", explanation.Tier)
	}

	// 3. Snapshot Replay with higher budget (350 tokens) -> docs/design.md should now be included!
	replay, err := engine.Replay("trace_abc123", ws, 350)
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}

	foundInReplay := false
	for _, it := range replay.IncludedItems {
		if it.Path == "docs/design.md" {
			foundInReplay = true
			if it.Tier != inspector.TierCounterfactual {
				t.Errorf("expected replay item to have TierCounterfactual, got %s", it.Tier)
			}
		}
	}
	if !foundInReplay {
		t.Errorf("docs/design.md should be included under increased budget 350")
	}

	// 4. External harness hook: attach and query outcome
	outcomeData := map[string]any{
		"task_completed": true,
		"model_retries":  0,
		"duration_ms":    450,
	}
	outcomeJSON, _ := json.Marshal(outcomeData)
	if err := s.PutTraceOutcome("trace_abc123", string(outcomeJSON)); err != nil {
		t.Fatalf("PutTraceOutcome failed: %v", err)
	}

	gotOutcome, err := engine.GetOutcome("trace_abc123")
	if err != nil {
		t.Fatalf("GetOutcome failed: %v", err)
	}
	if gotOutcome == nil || gotOutcome["task_completed"] != true {
		t.Errorf("unexpected outcome: %v", gotOutcome)
	}
}

func TestInspector_PrivacyAtExport(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	ws := "/workspaces/my-repo"

	trace := &inspector.Trace{
		ID:        "trace_priv",
		Workspace: ws,
		CreatedAt: time.Now().UTC(),
		Query:     "query",
		Discovery: []inspector.CandidateTrace{
			{Path: "secret/credentials.json", Hash: "h1", Tokens: 50},
			{Path: "public/readme.md", Hash: "h2", Tokens: 50},
		},
		Packing: inspector.PackingStage{
			IncludedItems: []inspector.PackedItem{
				{Path: "secret/credentials.json", TokensUsed: 50, Tier: inspector.TierMeasured},
				{Path: "public/readme.md", TokensUsed: 50, Tier: inspector.TierMeasured},
			},
		},
	}

	wp := &dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{PathPattern: "secret/*", Action: dlp.ActionDeny},
		},
	}
	policy := dlp.NewPolicyEngine(wp)

	engine := inspector.NewEngine(s, policy)
	_ = engine.RecordTrace(trace)

	exported, err := engine.ExportTraceMarkdown("trace_priv", ws)
	if err != nil {
		t.Fatalf("ExportTraceMarkdown failed: %v", err)
	}

	if strings.Contains(exported, "secret/credentials.json") {
		t.Errorf("exported markdown contains privacy-denied path: %s", exported)
	}
	if !strings.Contains(exported, "public/readme.md") {
		t.Errorf("exported markdown missing allowed path: %s", exported)
	}
}
