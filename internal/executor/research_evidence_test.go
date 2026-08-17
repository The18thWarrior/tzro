package executor

import (
	"path/filepath"
	"strings"
	"testing"
	"tzro/internal/memory"
)

func TestExtractResearchEvidence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_research_evidence.db")
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbPath)
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init test db: %v", err)
	}
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	probeID := "task_res_1_probe_web"

	// Add thought steps including web_search and web_browse
	if err := memory.DB.AddThoughtStep(memory.ThoughtStep{
		ID:         "step_1",
		ProbeID:    probeID,
		StepIndex:  1,
		ToolName:   "web_search",
		ToolArgs:   `{"query": "Go CVE 2025 standard library"}`,
		ToolOutput: `[{"title": "Go Security Advisory 2025", "url": "https://go.dev/security/vuln/GO-2025-001", "snippet": "Fixed net/http vulnerability affecting Go 1.23.0. Severity High CVSS 7.8"}]`,
	}); err != nil {
		t.Fatalf("AddThoughtStep 1 failed: %v", err)
	}
	if err := memory.DB.AddThoughtStep(memory.ThoughtStep{
		ID:         "step_2",
		ProbeID:    probeID,
		StepIndex:  2,
		ToolName:   "web_browse",
		ToolArgs:   `{"url": "https://go.dev/security/vuln/GO-2025-001"}`,
		ToolOutput: `Detailed security advisory for GO-2025-001. Affected versions: < 1.23.1. Mitigation: Upgrade to Go 1.23.1.`,
	}); err != nil {
		t.Fatalf("AddThoughtStep 2 failed: %v", err)
	}
	if err := memory.DB.AddThoughtStep(memory.ThoughtStep{
		ID:         "step_3",
		ProbeID:    probeID,
		StepIndex:  3,
		ToolName:   "read_file",
		ToolArgs:   `{"path": "irrelevant.txt"}`,
		ToolOutput: `irrelevant file output`,
	}); err != nil {
		t.Fatalf("AddThoughtStep 3 failed: %v", err)
	}

	evidence := extractResearchEvidence(probeID, 12288)

	if !strings.Contains(evidence, "https://go.dev/security/vuln/GO-2025-001") {
		t.Errorf("expected evidence to contain source URL, got:\n%s", evidence)
	}
	if !strings.Contains(evidence, "CVSS 7.8") {
		t.Errorf("expected evidence to contain CVSS detail, got:\n%s", evidence)
	}
	if !strings.Contains(evidence, "Upgrade to Go 1.23.1") {
		t.Errorf("expected evidence to contain mitigation detail, got:\n%s", evidence)
	}
	if strings.Contains(evidence, "irrelevant.txt") {
		t.Errorf("expected non-web tool outputs to be excluded from research evidence")
	}
}
