package executor

import (
	"strings"
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
	// Repetitive content should fail — need 4+ repetitions with 5-gram threshold.
	output := "Let me run a query to see the data and results. " +
		"Let me run a query to see the data and results. " +
		"Let me run a query to see the data and results. " +
		"Let me run a query to see the data and results. " +
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
	// Tabular data that naturally repeats column headers across rows.
	// ADR-0066: With 5-gram + scaled threshold, short tabular data (~50 words)
	// is no longer flagged as repetitive even WITHOUT WithAnalyzeNode().
	output := "Analysis of leads by source:\n" +
		"- Account_Owner: John Smith\n  leads\n - Distinct Lead_Sources: Web, Referral\n  Total: 45\n" +
		"- Account_Owner: Jane Doe\n  leads\n - Distinct Lead_Sources: Event, Web\n  Total: 32\n" +
		"- Account_Owner: Bob Wilson\n  leads\n - Distinct Lead_Sources: Partner, Web\n  Total: 28\n" +
		"Overall the dataset contains 105 leads across 3 owners."

	// With the new threshold, this short tabular data should pass even without WithAnalyzeNode
	reason := validateSynthesisOutput(output)
	if reason != "" {
		t.Errorf("expected valid output for short tabular data, got reason: %s", reason)
	}

	// With WithAnalyzeNode — should also pass
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

func TestValidateSynthesisOutput_MetaCommentaryDegeneration(t *testing.T) {
	// Reproduces the R14 benchmark regression: the 4B model generates
	// varied-but-vacuous sentences about task completion instead of content.
	// Each sentence is unique, so n-gram detection misses it.
	output := "GGUF specification, migration reasons. " +
		"The synthesis is complete. The final answer is ready for use. " +
		"The engine is done. The synthesis is final. The answer is complete. " +
		"The engine has completed its task. The synthesis is over. " +
		"The final answer is set. The engine has stopped. " +
		"The synthesis is terminated. The final answer is done. " +
		"The engine has ceased. The synthesis is completed. " +
		"The final answer is finished. The engine has ended. " +
		"The synthesis is wrapped. The final answer is sealed."
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for meta-commentary degeneration")
	}
	if !strings.Contains(reason, "meta-commentary degeneration") {
		t.Errorf("expected meta-commentary reason, got: %s", reason)
	}
}

func TestValidateSynthesisOutput_LegitimateWithMinorMeta(t *testing.T) {
	// Legitimate synthesis that happens to contain a couple of meta sentences
	// should NOT be flagged. Only flag when >40% of sentences match.
	output := "The GGUF format evolved from GGML in August 2023. " +
		"It introduced structured Key-Value metadata embedded directly in the file. " +
		"The binary layout consists of three sections: magic header, metadata block, and tensor data. " +
		"Q4_K_M is the recommended quantization for most consumer hardware. " +
		"It reduces VRAM usage by 72% with minimal quality loss. " +
		"The format supports mixed-precision quantization per tensor. " +
		"llama.cpp provides the primary inference engine for GGUF files. " +
		"Ollama wraps llama.cpp with a user-friendly CLI interface. " +
		"In conclusion, this synthesis is complete."
	reason := validateSynthesisOutput(output)
	if reason != "" {
		t.Errorf("expected valid output for legitimate synthesis with minor meta, got reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_MetaCommentary_SkippedForAnalyzeNode(t *testing.T) {
	// Analyze nodes should skip the meta-commentary check
	output := "The synthesis is complete. The final answer is ready for use. " +
		"The engine is done. The synthesis is final. The answer is complete. " +
		"The engine has completed its task. The synthesis is over. " +
		"The final answer is set. The engine has stopped. " +
		"The synthesis is terminated. The final answer is done."
	reason := validateSynthesisOutput(output, WithAnalyzeNode())
	if reason != "" {
		t.Errorf("expected meta-commentary check skipped for analyze node, got reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_TrailingMetaCommentary(t *testing.T) {
	// Reproduces R16 market_analysis_local_ai regression: ~2000 chars of valid
	// analysis followed by a degenerate tail of meta-commentary sentences.
	// The overall ratio check misses this because the valid preamble dilutes
	// the meta percentage below 40%.
	validPreamble := "The exploration identified four dominant local-first AI inference engines: " +
		"Ollama, llama.cpp, vLLM, and Apple MLX. They are evaluated across supported model formats, " +
		"hardware requirements, performance benchmarks, and business models. " +
		"Key findings: Ollama emphasizes ease of use and broad compatibility. " +
		"llama.cpp provides low-level flexibility and quantization support. " +
		"vLLM excels in production throughput and paged attention. " +
		"MLX targets Apple Silicon with native Metal acceleration. " +
		"All are open-source with no restrictive licensing barriers. " +
		"Performance varies by hardware: NVIDIA GPUs are optimal for Ollama and vLLM. " +
		"Quantization formats like GGUF and GPTQ are critical for consumer hardware. " +
		"The engines serve different use cases from personal AI to production deployment. " +
		"The landscape is dynamic with ongoing improvements in quantization and hardware acceleration. "

	degenerateTail := "The synthesis is ready for delivery. The final answer is provided below. " +
		"The analysis is complete. The output is ready. " +
		"The synthesis is done. The answer is complete. " +
		"The final answer is ready. The synthesis is complete. " +
		"The answer is ready. The synthesis is done. " +
		"The answer is complete. The synthesis is finished."

	output := validPreamble + degenerateTail
	reason := validateSynthesisOutput(output)
	if reason == "" {
		t.Error("expected validation failure for trailing meta-commentary degeneration")
	}
	if !strings.Contains(reason, "trailing meta-commentary") {
		t.Errorf("expected trailing meta-commentary reason, got: %s", reason)
	}
}

func TestValidateSynthesisOutput_TrailingMeta_NotFlaggedWhenClean(t *testing.T) {
	// A clean output with one closing meta sentence at the very end should NOT
	// be flagged — only flag when the tail is dominated by meta-commentary.
	output := "The GGUF format evolved from GGML in August 2023. " +
		"It introduced structured metadata embedded directly in the binary file. " +
		"The layout consists of three sections: magic header, metadata block, and tensor data. " +
		"Q4_K_M is recommended for consumer hardware with 8GB VRAM. " +
		"It reduces memory usage by 72% with minimal quality loss. " +
		"The format supports mixed-precision quantization per tensor. " +
		"llama.cpp provides the primary inference engine for GGUF files. " +
		"Ollama wraps llama.cpp with a user-friendly CLI interface. " +
		"LM Studio offers a desktop GUI for loading quantized models. " +
		"Hugging Face hosts most models as GGUF variants. " +
		"The analysis is complete."
	reason := validateSynthesisOutput(output)
	if reason != "" {
		t.Errorf("expected valid output with clean tail, got reason: %s", reason)
	}
}

func TestValidateSynthesisOutput_BulletList_RepeatedMetricsPass(t *testing.T) {
	// Real-world pattern from lead_source_by_owner: distinct account owners
	// that share an identical metric value ("— 1 total lead -").
	// This must NOT trigger false positive repetition rejections.
	output := "# Leads by Account Owner\n\n" +
		"- Alice — 1 total lead - Sources: Web\n" +
		"- Bob — 1 total lead - Sources: Referral\n" +
		"- Charlie — 1 total lead - Sources: Email\n" +
		"- Dave — 1 total lead - Sources: Direct\n" +
		"- Eve — 1 total lead - Sources: Partner\n" +
		"\nTotal 5 leads across 5 owners."

	reason := validateSynthesisOutput(output)
	if reason != "" {
		t.Errorf("expected structured bullet list with shared metric counts to pass, got: %s", reason)
	}

	preCheckResult, preCheckReason := StructuralPreCheck(output)
	if preCheckResult != "passed" {
		t.Errorf("expected StructuralPreCheck to pass for bullet list, got: %s (%s)", preCheckResult, preCheckReason)
	}
}

