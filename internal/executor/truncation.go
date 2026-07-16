package executor

// truncation.go — Thin wrappers delegating to internal/compactor.
//
// This file preserves the public API (TruncateToolOutput, TruncateSynthesisContext,
// ContentType, SynthesisStep) for backward compatibility while routing all logic
// through the unified structured compactor.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"tzro/internal/compactor"
)

// maxSynthesisContextChars is the character budget for probe synthesis context.
// Set to ~40K tokens ≈ 160K chars (at ~4 chars/token), leaving ample context
// for the model's output generation.
const maxSynthesisContextChars = 160000

// ContentType classifies tool output for type-appropriate truncation.
// Kept for backward compatibility — maps to compactor.SegmentType.
type ContentType = compactor.SegmentType

const (
	ContentCode    = compactor.SegmentCode
	ContentTabular = compactor.SegmentTabular
	ContentText    = compactor.SegmentText
)

// codeFloorChars is the minimum chars to retain per code file during truncation.
// Function signatures may exceed this floor and are always retained.
const codeFloorChars = 500

// SynthesisStep holds the data for one probe exploration step,
// used by TruncateSynthesisContext. Kept for backward compatibility.
type SynthesisStep struct {
	StepIndex  int
	Thought    string
	ToolOutput string
}

// classifyContent delegates to the compactor's content classifier.
func classifyContent(content string) ContentType {
	return compactor.ClassifyContent(content)
}

// TruncateToolOutput applies content-aware truncation to a single tool output.
// Delegates to compactor.CompactContent for structured skeleton-based compaction.
func TruncateToolOutput(content string, targetChars int) string {
	if utf8.RuneCountInString(content) <= targetChars {
		return content
	}
	return compactor.CompactContent(content, targetChars)
}

// TruncateSynthesisContext takes the full list of thought steps with tool outputs
// and returns a truncated version that fits within maxSynthesisContextChars.
// Delegates to compactor.CompactSteps for content-aware compaction.
func TruncateSynthesisContext(steps []SynthesisStep) string {
	// First pass: compute total size
	totalChars := 0
	for _, s := range steps {
		totalChars += utf8.RuneCountInString(s.Thought) + utf8.RuneCountInString(s.ToolOutput) + 50
	}

	if totalChars <= maxSynthesisContextChars {
		// Everything fits — return all content with structured compaction
		return formatSynthesisSteps(steps, -1)
	}

	// Convert to compactor steps
	compactorSteps := make([]compactor.Step, len(steps))
	for i, s := range steps {
		compactorSteps[i] = compactor.Step{
			Index:      s.StepIndex,
			Thought:    s.Thought,
			ToolOutput: s.ToolOutput,
		}
	}

	// Use deterministic-only mode (nil engine, no LLM calls)
	result, err := compactor.CompactSteps(context.TODO(), compactorSteps, "", maxSynthesisContextChars, nil)
	if err != nil {
		// Fallback to legacy formatting
		return formatSynthesisSteps(steps, -1)
	}
	return result.Output
}

// formatSynthesisSteps formats steps into the context string for synthesis.
// If maxSteps >= 0, only the last maxSteps steps are included.
func formatSynthesisSteps(steps []SynthesisStep, maxSteps int) string {
	start := 0
	if maxSteps >= 0 && len(steps) > maxSteps {
		start = len(steps) - maxSteps
	}

	var sb strings.Builder
	for _, s := range steps[start:] {
		sb.WriteString(fmt.Sprintf("Step %d: %s\n", s.StepIndex, s.Thought))
		if s.ToolOutput != "" {
			sb.WriteString(fmt.Sprintf("  Tool Output:\n%s\n", s.ToolOutput))
		}
	}
	return sb.String()
}
