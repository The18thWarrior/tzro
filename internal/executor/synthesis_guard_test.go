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
