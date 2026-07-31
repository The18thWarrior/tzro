package compactor

// tool_output.go — Content-aware compaction of ThoughtStep tool outputs
// for the Recall Node Refinement Pass (ADR-0064).
//
// Uses existing segmentation infrastructure. Code → skeleton (deterministic),
// tabular → header + sample rows, text → router LLM fact-extraction.
// Analyze Node tools (sql_cached_data, introspect_cache) are exempt.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ToolOutputStep represents a single ThoughtStep's tool output for compaction.
type ToolOutputStep struct {
	StepIndex  int
	ToolName   string
	ToolArgs   string
	ToolOutput string
}

// exemptTools are Analyze Node tools whose outputs are preserved at full
// fidelity — analytical evidence would be meaningless if summarized.
var exemptTools = map[string]bool{
	"sql_cached_data":  true,
	"introspect_cache": true,
}

// CompactToolOutputs compacts a slice of ThoughtStep tool outputs using
// content-aware strategies with a total budget cap.
//
// Compaction strategy per segment type:
//   - Code (SegmentCode) → ExtractSkeleton (deterministic, no LLM)
//   - Tabular (SegmentTabular) → TruncateTabular (header + sample rows)
//   - Text (SegmentText) → engine.CompactToolOutput (LLM fact-extraction)
//   - Exempt tools (sql_cached_data, introspect_cache) → full fidelity
//
// On LLM failure for text segments, falls back to TruncateTextMiddleOut.
// Final hard truncation if still over budget.
func CompactToolOutputs(ctx context.Context, steps []ToolOutputStep, budget int, engine CompactEngine) (CompactResult, error) {
	if len(steps) == 0 {
		return CompactResult{}, nil
	}

	inputChars := 0
	for _, s := range steps {
		inputChars += utf8.RuneCountInString(s.ToolOutput) + 50 // overhead for step header
	}

	// Per-step budget for non-exempt steps
	nonExemptCount := 0
	exemptChars := 0
	for _, s := range steps {
		if exemptTools[s.ToolName] {
			exemptChars += utf8.RuneCountInString(s.ToolOutput) + 50
		} else {
			nonExemptCount++
		}
	}
	perStepBudget := 0
	if budget > 0 && nonExemptCount > 0 {
		remaining := budget - exemptChars
		if remaining < 0 {
			remaining = 0
		}
		perStepBudget = remaining / nonExemptCount
		if perStepBudget < 500 {
			perStepBudget = 500
		}
	}

	var parts []string
	llmCalls := 0

	for _, s := range steps {
		header := fmt.Sprintf("### Step %d: %s(%s)\n", s.StepIndex, s.ToolName, s.ToolArgs)

		if exemptTools[s.ToolName] {
			// Exempt: preserve at full fidelity
			parts = append(parts, header+s.ToolOutput)
			continue
		}

		output := s.ToolOutput
		if output == "" {
			parts = append(parts, header+"(no output)")
			continue
		}

		// Segment and compact
		compacted := compactToolOutput(ctx, output, perStepBudget, engine, &llmCalls)
		parts = append(parts, header+compacted)
	}

	result := strings.Join(parts, "\n")
	outputChars := utf8.RuneCountInString(result)

	// Final budget enforcement
	if budget > 0 && outputChars > budget {
		runes := []rune(result)
		cutoff := budget - 50
		if cutoff < 1 {
			cutoff = budget
		}
		result = string(runes[:cutoff]) + "\n[... truncated ...]"
		outputChars = utf8.RuneCountInString(result)
	}

	return CompactResult{
		Output:      result,
		InputChars:  inputChars,
		OutputChars: outputChars,
		LLMCalls:    llmCalls,
	}, nil
}

// compactToolOutput compacts a single tool output using content-aware strategies.
func compactToolOutput(ctx context.Context, output string, budget int, engine CompactEngine, llmCalls *int) string {
	segments := SegmentContent(output)
	if len(segments) == 0 {
		return output
	}

	var parts []string
	for _, seg := range segments {
		switch seg.Type {
		case SegmentCode:
			// Deterministic skeleton — never LLM-compressed
			if budget > 0 {
				parts = append(parts, ExtractSkeleton(seg.Content, budget))
			} else {
				parts = append(parts, ExtractSkeleton(seg.Content, 0))
			}
		case SegmentTabular:
			if budget > 0 {
				parts = append(parts, TruncateTabular(seg.Content, budget))
			} else {
				parts = append(parts, TruncateTabular(seg.Content, 4096))
			}
		case SegmentText:
			// LLM fact-extraction for text content
			if engine != nil && utf8.RuneCountInString(seg.Content) > 200 {
				summarized, err := engine.CompactToolOutput(ctx, seg.Content)
				if err == nil {
					*llmCalls++
					parts = append(parts, summarized)
					continue
				}
				// Failure cascade: fall back to deterministic truncation
			}
			if budget > 0 {
				parts = append(parts, TruncateTextMiddleOut(seg.Content, budget))
			} else {
				parts = append(parts, seg.Content)
			}
		default:
			if budget > 0 {
				parts = append(parts, TruncateTextMiddleOut(seg.Content, budget))
			} else {
				parts = append(parts, seg.Content)
			}
		}
	}

	return strings.Join(parts, "\n\n")
}
