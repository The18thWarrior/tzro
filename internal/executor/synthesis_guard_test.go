package executor

import (
	"testing"
)

func TestValidateSynthesisOutput_Valid(t *testing.T) {
	// A good synthesis should pass validation
	output := "The dataset contains 50 records. The top 5 countries by lead count are: " +
		"USA (20), UK (10), Germany (8), France (7), and Japan (5). " +
		"Together these countries represent 100% of the leads in the dataset."
	if reason := validateSynthesisOutput(output); reason != "" {
		t.Errorf("expected valid output, got reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_ControlTokenLeak(t *testing.T) {
	// Bare control token should fail (caught by degenerate output check
	// since stripping SYNTHESIZE_READY leaves 0 chars)
	output := "SYNTHESIZE_READY"
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for bare control token")
	}
	// Should be caught as degenerate (0 chars after cleaning)
	if reason != "degenerate output (0 chars after cleaning)" {
		t.Errorf("unexpected reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_DegenerateOutput(t *testing.T) {
	// Very short output should fail
	output := "No data found."
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for degenerate output")
	}
}

func TestValidateSynthesisOutput_RepetitiveContent(t *testing.T) {
	// Repetitive content should fail
	output := "Let me run a query to see the data. " +
		"Let me run a query to see the data. " +
		"Let me run a query to see the data. " +
		"And then check the results after that."
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for repetitive content")
	}
}

func TestValidateSynthesisOutput_PlaceholderTemplates(t *testing.T) {
	// Template placeholders should fail
	output := "The analysis shows the following breakdown:\n" +
		"| Sector | Count | Percentage |\n" +
		"| *[Top Sector]* | *[X]* | *[Y.Y]%* |\n" +
		"| *[Second]* | *[Z]* | *[W.W]%* |\n" +
		"This is a placeholder result."
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for placeholder templates")
	}
}

func TestValidateSynthesisOutput_AnalyzeNode_AllowsTabularRepetition(t *testing.T) {
	// Tabular data that naturally repeats column headers across rows —
	// this is the exact pattern that caused lead_source_by_owner to fail.
	output := "Analysis of leads by source:\n" +
		"- Account_Owner: John Smith\n  leads\n - Distinct Lead_Sources: Web, Referral\n  Total: 45\n" +
		"- Account_Owner: Jane Doe\n  leads\n - Distinct Lead_Sources: Event, Web\n  Total: 32\n" +
		"- Account_Owner: Bob Wilson\n  leads\n - Distinct Lead_Sources: Partner, Web\n  Total: 28\n" +
		"Overall the dataset contains 105 leads across 3 owners."

	// Without WithAnalyzeNode — should detect repetition
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected repetition detection WITHOUT WithAnalyzeNode()")
	}

	// With WithAnalyzeNode — should pass (tabular repetition is valid)
	reason = validateSynthesisOutput(output, WithAnalyzeNode())
	if reason != "" {
		t.Errorf("expected valid output WITH WithAnalyzeNode(), got reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_AnalyzeNode_StillCatchesDegenerate(t *testing.T) {
	// Even with WithAnalyzeNode, degenerate output should still fail
	output := "No data."
	reason := validateSynthesisOutput(output, WithAnalyzeNode())
	if reason == "" {
		t.Error("expected degenerate detection even with WithAnalyzeNode()")
	}
}


func TestStripControlTokens(t *testing.T) {
	input := "Here is the result <SYNTHESIZE_READY>\nMore content <ACTION>test</ACTION>"
	result := stripControlTokens(input)
	if result != "Here is the result \nMore content test" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripControlTokens_BareToken(t *testing.T) {
	input := "SYNTHESIZE_READY"
	result := stripControlTokens(input)
	if result != "" {
		t.Errorf("expected empty string, got: %q", result)
	}
}

// NOTE: TestStripTrailingRepetition_* tests removed (ADR-0060).
// Character-level degeneration detection is now handled by the GenerationGuard
// at the Inference Backend layer. See internal/inference/generation_guard_test.go.
