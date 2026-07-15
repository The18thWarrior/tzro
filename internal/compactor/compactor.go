package compactor

// compactor.go — Unified content-aware compaction module.
//
// Core principle: Code is NEVER LLM-compressed. LLM only compacts the
// model's own reasoning text. Tool outputs get deterministic content-aware
// truncation only.
//
// Three entry points:
//   - CompactContent: Deterministic-only, for accumulated context and tool outputs
//   - CompactSteps: Full pipeline for probe thought chains (replaces compactThoughtChain)
//   - CompactFacts: For recall refined context (replaces compactRefinedContext)

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// reasoningChunkSize is the target size for reasoning text chunks
// sent to the LLM for compression. ≤500 chars keeps within the
// router model's comfort zone.
const reasoningChunkSize = 500

// CompactResult holds the compacted output with metrics.
type CompactResult struct {
	Output      string
	InputChars  int
	OutputChars int
	LLMCalls    int // Number of LLM calls for reasoning compression
}

// Step represents a single thought chain step for CompactSteps.
type Step struct {
	Index      int
	Thought    string // Model's reasoning — LLM-compressed
	ToolName   string
	ToolArgs   string
	ToolOutput string // Classified per content type — deterministic only
}

// compactedStep holds a step after structured compaction.
type compactedStep struct {
	index        int
	thought      string
	toolName     string
	toolArgs     string
	toolOutput   string
	thoughtChars int
	outputChars  int
}

// CompactContent compacts a single piece of content using deterministic
// content-aware strategies. No LLM is used.
//
// This is the drop-in replacement for TruncateToolOutput in accumulated
// context assembly (executor_context.go) and edge thought evaluation.
func CompactContent(content string, budget int) string {
	if content == "" {
		return content
	}

	inputLen := utf8.RuneCountInString(content)
	if budget > 0 && inputLen <= budget {
		return content
	}

	// Segment the content
	segments := SegmentContent(content)
	if len(segments) == 0 {
		return content
	}

	// If single segment, apply type-specific compaction
	if len(segments) == 1 {
		return compactSegment(segments[0], budget)
	}

	// Multiple segments — compact each, then reassemble
	var parts []string
	totalBudget := budget
	perSegmentBudget := 0
	if totalBudget > 0 {
		perSegmentBudget = totalBudget / len(segments)
		if perSegmentBudget < codeFloorChars {
			perSegmentBudget = codeFloorChars
		}
	}

	for _, seg := range segments {
		compacted := compactSegment(seg, perSegmentBudget)
		parts = append(parts, compacted)
	}

	result := strings.Join(parts, "\n\n")

	// If still over budget after per-segment compaction, hard truncate
	if budget > 0 && utf8.RuneCountInString(result) > budget {
		runes := []rune(result)
		return string(runes[:budget-50]) + "\n[... truncated ...]"
	}

	return result
}

// compactSegment applies type-appropriate deterministic compaction to a single segment.
func compactSegment(seg Segment, budget int) string {
	switch seg.Type {
	case SegmentCode:
		return ExtractSkeleton(seg.Content, budget)
	case SegmentTabular:
		if budget > 0 {
			return TruncateTabular(seg.Content, budget)
		}
		return TruncateTabular(seg.Content, 4096) // reasonable default
	case SegmentText:
		if budget > 0 {
			return TruncateTextMiddleOut(seg.Content, budget)
		}
		return seg.Content
	default:
		if budget > 0 {
			return TruncateTextMiddleOut(seg.Content, budget)
		}
		return seg.Content
	}
}

// CompactSteps compacts a series of thought chain steps.
//
// Tool outputs are compacted deterministically:
//   - Code → skeleton (signatures + body fingerprints)
//   - Tabular → header + sample rows
//   - Text → middle-out truncation
//
// Thought text (model reasoning) is compressed via LLM when engine is provided:
//   - Split into ~500-char chunks
//   - Each chunk compressed by router: "Extract key conclusion"
//
// When engine is nil, reasoning text is kept as-is (deterministic-only mode).
//
// Budget management uses a two-stage cascade:
//   - Stage 1: Structured compaction of all segments
//   - Stage 2: If over budget, drop oldest tool outputs first
func CompactSteps(ctx context.Context, steps []Step, goal string, budget int, engine CompactEngine) (CompactResult, error) {
	if len(steps) == 0 {
		return CompactResult{}, nil
	}

	inputChars := 0
	for _, s := range steps {
		inputChars += utf8.RuneCountInString(s.Thought) + utf8.RuneCountInString(s.ToolOutput) + 50
	}

	llmCalls := 0

	// Stage 1: Structured compaction
	compacted := make([]compactedStep, len(steps))
	for i, s := range steps {
		// Compact tool output deterministically
		toolOut := s.ToolOutput
		if toolOut != "" {
			toolOut = CompactContent(toolOut, 0) // No per-step budget in Stage 1
		}

		// Compact reasoning text via LLM
		thought := s.Thought
		if engine != nil && thought != "" {
			compressed, calls, err := compactReasoning(ctx, thought, engine)
			if err == nil {
				thought = compressed
				llmCalls += calls
			}
			// On error, keep original thought
		}

		compacted[i] = compactedStep{
			index:        s.Index,
			thought:      thought,
			toolName:     s.ToolName,
			toolArgs:     s.ToolArgs,
			toolOutput:   toolOut,
			thoughtChars: utf8.RuneCountInString(thought),
			outputChars:  utf8.RuneCountInString(toolOut),
		}
	}

	// Build output
	output := formatCompactedSteps(compacted)
	outputChars := utf8.RuneCountInString(output)

	// Stage 2: If over budget, apply oldest-first triage
	if budget > 0 && outputChars > budget {
		output = triageOldestFirst(compacted, budget)
		outputChars = utf8.RuneCountInString(output)
	}

	return CompactResult{
		Output:      output,
		InputChars:  inputChars,
		OutputChars: outputChars,
		LLMCalls:    llmCalls,
	}, nil
}

// compactReasoning splits reasoning text into chunks and compresses each.
func compactReasoning(ctx context.Context, thought string, engine CompactEngine) (string, int, error) {
	chunks := chunkBySentence(thought, reasoningChunkSize)
	if len(chunks) == 0 {
		return thought, 0, nil
	}

	// Don't compress if already short
	if len(chunks) == 1 && utf8.RuneCountInString(thought) <= reasoningChunkSize {
		return thought, 0, nil
	}

	var compressed []string
	calls := 0
	for _, chunk := range chunks {
		result, err := engine.CompactReasoning(ctx, chunk)
		if err != nil {
			compressed = append(compressed, chunk) // Keep original on error
			continue
		}
		calls++
		compressed = append(compressed, result)
	}

	return strings.Join(compressed, " "), calls, nil
}

// chunkBySentence splits text into chunks of approximately targetSize chars,
// breaking at sentence boundaries (". " or ".\n").
func chunkBySentence(text string, targetSize int) []string {
	if utf8.RuneCountInString(text) <= targetSize {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if utf8.RuneCountInString(remaining) <= targetSize {
			chunks = append(chunks, remaining)
			break
		}

		// Find a sentence boundary within targetSize
		runes := []rune(remaining)
		end := targetSize
		if end > len(runes) {
			end = len(runes)
		}
		candidate := string(runes[:end])

		// Look for last sentence boundary
		bestBreak := -1
		for _, sep := range []string{". ", ".\n", "? ", "!\n", "! ", "?\n"} {
			idx := strings.LastIndex(candidate, sep)
			if idx > bestBreak {
				bestBreak = idx + len(sep)
			}
		}

		if bestBreak > targetSize/4 { // Don't break too early
			chunks = append(chunks, string(runes[:bestBreak]))
			remaining = string(runes[bestBreak:])
		} else {
			// No good sentence break — break at target
			chunks = append(chunks, candidate)
			remaining = string(runes[end:])
		}
	}

	return chunks
}

// formatCompactedSteps formats compacted steps into the output string.
func formatCompactedSteps(steps []compactedStep) string {
	var sb strings.Builder
	for _, s := range steps {
		sb.WriteString(fmt.Sprintf("Step %d: %s", s.index, s.thought))
		if s.toolName != "" {
			sb.WriteString(fmt.Sprintf(" → %s(%s)", s.toolName, s.toolArgs))
			if s.toolOutput != "" {
				sb.WriteString(fmt.Sprintf(" → %s", s.toolOutput))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// triageOldestFirst drops tool outputs from oldest steps until within budget.
// Most recent N steps always keep their tool outputs.
func triageOldestFirst(steps []compactedStep, budget int) string {
	// Minimum steps to preserve with full tool output
	preserveRecent := 3
	if preserveRecent > len(steps) {
		preserveRecent = len(steps)
	}

	// Try progressively dropping older tool outputs
	for dropCount := 1; dropCount <= len(steps)-preserveRecent; dropCount++ {
		var sb strings.Builder
		for i, s := range steps {
			sb.WriteString(fmt.Sprintf("Step %d: %s", s.index, s.thought))
			if s.toolName != "" {
				sb.WriteString(fmt.Sprintf(" → %s(%s)", s.toolName, s.toolArgs))
				if i >= dropCount && s.toolOutput != "" {
					sb.WriteString(fmt.Sprintf(" → %s", s.toolOutput))
				}
			}
			sb.WriteString("\n")
		}
		result := sb.String()
		if utf8.RuneCountInString(result) <= budget {
			return result
		}
	}

	// Still over budget — drop all tool outputs except the last preserveRecent
	var sb strings.Builder
	for i, s := range steps {
		sb.WriteString(fmt.Sprintf("Step %d: %s", s.index, s.thought))
		if s.toolName != "" {
			sb.WriteString(fmt.Sprintf(" → %s(%s)", s.toolName, s.toolArgs))
			if i >= len(steps)-preserveRecent && s.toolOutput != "" {
				sb.WriteString(fmt.Sprintf(" → %s", s.toolOutput))
			}
		}
		sb.WriteString("\n")
	}

	result := sb.String()
	if utf8.RuneCountInString(result) > budget {
		runes := []rune(result)
		return string(runes[:budget-50]) + "\n[... truncated ...]"
	}
	return result
}

// CompactFacts compacts a list of refined context facts (recall node).
// Code/data facts are preserved deterministically.
// Text-only facts are compressed via LLM when engine is provided.
//
// This replaces compactRefinedContext() in recall.go.
func CompactFacts(ctx context.Context, facts string, goal string, budget int, engine CompactEngine) (CompactResult, error) {
	inputChars := utf8.RuneCountInString(facts)

	if budget > 0 && inputChars <= budget {
		return CompactResult{
			Output:      facts,
			InputChars:  inputChars,
			OutputChars: inputChars,
		}, nil
	}

	// Split facts into individual lines
	lines := strings.Split(facts, "\n")
	var compactedLines []string
	llmCalls := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check if this fact contains code or data
		if looksLikeCode(trimmed) || looksTabular(trimmed) {
			// Preserve code/data facts — compact deterministically
			compactedLines = append(compactedLines, CompactContent(trimmed, 0))
			continue
		}

		// Text fact — compress via LLM if engine available and chunk is large
		if engine != nil && utf8.RuneCountInString(trimmed) > reasoningChunkSize {
			result, err := engine.CompactReasoning(ctx, trimmed)
			if err == nil {
				llmCalls++
				compactedLines = append(compactedLines, result)
				continue
			}
		}

		compactedLines = append(compactedLines, trimmed)
	}

	output := strings.Join(compactedLines, "\n")
	outputChars := utf8.RuneCountInString(output)

	// If still over budget, apply middle-out truncation
	if budget > 0 && outputChars > budget {
		output = TruncateTextMiddleOut(output, budget)
		outputChars = utf8.RuneCountInString(output)
	}

	return CompactResult{
		Output:      output,
		InputChars:  inputChars,
		OutputChars: outputChars,
		LLMCalls:    llmCalls,
	}, nil
}
