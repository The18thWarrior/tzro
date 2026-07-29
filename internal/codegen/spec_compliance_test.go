package codegen

import (
	"strings"
	"testing"
)

// ─── Slice 1: BuildRegenerationPrompt includes spec, checklist, and correct instructions ─

func TestBuildRegenerationPrompt_IncludesSpecAndChecklist(t *testing.T) {
	spec := "Implement a QueryBuilder that supports SELECT, JOIN, GROUP BY, HAVING, ORDER BY, and LIMIT"
	checklist := "1. SELECT: MISSING - Build() does not emit SELECT clauses\n2. JOIN: MISSING - No JOIN support\n3. GROUP BY: IMPLEMENTED"
	language := "go"
	maxLines := 500
	moduleContext := "package db\npackage models"

	prompt := BuildRegenerationPrompt(spec, checklist, language, maxLines, moduleContext)

	// Must include the original spec
	if !strings.Contains(prompt, spec) {
		t.Fatal("prompt must include the original spec")
	}

	// Must include the compliance checklist
	if !strings.Contains(prompt, "MISSING") {
		t.Fatal("prompt must include the compliance checklist with MISSING requirements")
	}

	// Must NOT contain Rule 4 "do not add new features" — that's for repair, not regeneration
	if strings.Contains(prompt, "Do not add new features") {
		t.Fatal("regeneration prompt must NOT contain repair Rule 4 ('Do not add new features')")
	}

	// Must instruct to implement ALL requirements
	if !strings.Contains(prompt, "ALL") || !strings.Contains(prompt, "requirements") {
		t.Fatal("regeneration prompt must instruct to implement ALL requirements")
	}

	// Must include module context
	if !strings.Contains(prompt, moduleContext) {
		t.Fatal("prompt must include module context")
	}
}

// ─── Slice 2: ParseComplianceChecklist parses IMPLEMENTED/MISSING ──────────

func TestParseComplianceChecklist_ParsesImplementedAndMissing(t *testing.T) {
	output := `1. SELECT: IMPLEMENTED - Build() emits SELECT clause
2. JOIN: MISSING - No JOIN support in Build()
3. GROUP BY: IMPLEMENTED - GroupBy() method exists
4. HAVING: MISSING - No HAVING support
5. ORDER BY: IMPLEMENTED - OrderBy() method works
6. LIMIT: MISSING - No LIMIT clause emitted`

	result := ParseComplianceChecklist(output)

	if result.Pass {
		t.Fatal("expected Pass=false when MISSING requirements exist")
	}

	if len(result.MissingRequirements) != 3 {
		t.Fatalf("expected 3 missing requirements, got %d: %v", len(result.MissingRequirements), result.MissingRequirements)
	}

	// Verify the missing requirements were extracted
	found := map[string]bool{"JOIN": false, "HAVING": false, "LIMIT": false}
	for _, req := range result.MissingRequirements {
		for key := range found {
			if strings.Contains(req, key) {
				found[key] = true
			}
		}
	}
	for key, wasFound := range found {
		if !wasFound {
			t.Errorf("expected %s in missing requirements, got: %v", key, result.MissingRequirements)
		}
	}
}

// ─── Slice 3: All IMPLEMENTED → Pass = true ────────────────────────────────

func TestParseComplianceChecklist_AllImplemented_Passes(t *testing.T) {
	output := `1. SELECT: IMPLEMENTED - Build() correctly emits SELECT
2. JOIN: IMPLEMENTED - JOIN clause supported
3. GROUP BY: IMPLEMENTED - GroupBy() works correctly`

	result := ParseComplianceChecklist(output)

	if !result.Pass {
		t.Fatal("expected Pass=true when all requirements are IMPLEMENTED")
	}
	if len(result.MissingRequirements) != 0 {
		t.Fatalf("expected 0 missing requirements, got %d", len(result.MissingRequirements))
	}
}

// ─── Slice 4: Any MISSING → Pass = false, MissingRequirements populated ────

func TestParseComplianceChecklist_SingleMissing_FailsWithRequirement(t *testing.T) {
	output := `1. Retry logic: IMPLEMENTED - retries up to 3 times
2. Exponential backoff: MISSING - uses fixed delay instead of exponential
3. Error logging: IMPLEMENTED - logs to stderr`

	result := ParseComplianceChecklist(output)

	if result.Pass {
		t.Fatal("expected Pass=false when any requirement is MISSING")
	}
	if len(result.MissingRequirements) != 1 {
		t.Fatalf("expected 1 missing requirement, got %d: %v", len(result.MissingRequirements), result.MissingRequirements)
	}
	if !strings.Contains(result.MissingRequirements[0], "Exponential backoff") {
		t.Fatalf("expected 'Exponential backoff' in missing, got: %s", result.MissingRequirements[0])
	}
}
