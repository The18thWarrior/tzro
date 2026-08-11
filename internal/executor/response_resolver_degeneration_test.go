package executor

import "testing"

func TestIsDegenerateRepetition(t *testing.T) {
	t.Run("SingleWordRepeated100x", func(t *testing.T) {
		var s string
		for i := 0; i < 100; i++ {
			s += "synthesis\n"
		}
		if !isDegenerateRepetition(s) {
			t.Error("expected degenerate detection for single word repeated 100x")
		}
	})

	t.Run("SingleWordRepeated10x", func(t *testing.T) {
		var s string
		for i := 0; i < 10; i++ {
			s += "synthesis "
		}
		if !isDegenerateRepetition(s) {
			t.Error("expected degenerate detection for single word repeated 10x")
		}
	})

	t.Run("NormalParagraph", func(t *testing.T) {
		s := "The internal cache package provides a layered abstraction for AI model inference, " +
			"execution management, routing, and operational support. It supports local execution " +
			"via llama.cpp bindings or local runtimes and remote cloud backend execution, " +
			"featuring adaptive routing, token tracking, thermal control, and metrics persistence."
		if isDegenerateRepetition(s) {
			t.Error("normal paragraph text should not be detected as degenerate")
		}
	})

	t.Run("ShortString", func(t *testing.T) {
		if isDegenerateRepetition("hello world") {
			t.Error("short string should not be detected as degenerate")
		}
	})

	t.Run("EmptyString", func(t *testing.T) {
		if isDegenerateRepetition("") {
			t.Error("empty string should not be detected as degenerate")
		}
	})

	t.Run("FewTokensBelowThreshold", func(t *testing.T) {
		// 9 tokens — below the 10-token minimum
		s := "a a a a a a a a a"
		if isDegenerateRepetition(s) {
			t.Error("fewer than 10 tokens should not trigger degeneration")
		}
	})

	t.Run("MixedContentWithSomeRepetition", func(t *testing.T) {
		// 5 repeats of "data" among varied content — should NOT trigger
		s := "The data shows that data processing in this data pipeline handles data " +
			"transformation and data validation alongside error handling and logging infrastructure."
		if isDegenerateRepetition(s) {
			t.Error("varied content with some natural repetition should not be degenerate")
		}
	})

	t.Run("TwoWordPhraseRepeated", func(t *testing.T) {
		// One dominant word accounts for >80% with 10+ occurrences
		var s string
		for i := 0; i < 50; i++ {
			s += "output output "
		}
		if !isDegenerateRepetition(s) {
			t.Error("expected degenerate detection for repeated two-word phrase")
		}
	})

	t.Run("NewlineSeparatedRepetition", func(t *testing.T) {
		// Exact pattern from the benchmark failure
		var s string
		for i := 0; i < 200; i++ {
			s += "synthesis\n"
		}
		if !isDegenerateRepetition(s) {
			t.Error("expected degenerate detection for newline-separated repetition")
		}
	})

	t.Run("MixedCaseRepetition", func(t *testing.T) {
		var s string
		for i := 0; i < 20; i++ {
			if i%2 == 0 {
				s += "Synthesis "
			} else {
				s += "synthesis "
			}
		}
		if !isDegenerateRepetition(s) {
			t.Error("expected case-insensitive degenerate detection")
		}
	})
}
