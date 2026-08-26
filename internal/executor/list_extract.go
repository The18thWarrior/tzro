package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"tzro/internal/inference"
)

// ---------------------------------------------------------------------------
// List Node — line-range extraction mechanics (ADR-0090)
// ---------------------------------------------------------------------------

// LineRange represents a start/end line pair for extraction (1-indexed).
type LineRange struct {
	StartLine int
	EndLine   int
}

// FileChunk represents a window of a file for chunked extraction.
type FileChunk struct {
	Lines       []string // The lines in this chunk
	StartOffset int      // 0-indexed offset into the original file's line array
}

// LineRangeExtractionSchema is the GBNF-constraining JSON schema for
// line-range extraction. The model returns an array of [startLine, endLine] pairs.
const LineRangeExtractionSchema = `{
  "type": "array",
  "items": {
    "type": "array",
    "items": { "type": "integer" },
    "minItems": 2,
    "maxItems": 2
  }
}`

// lineRangeExtractionSystemPrompt instructs the model to identify relevant
// line ranges without rewriting content.
const lineRangeExtractionSystemPrompt = `You are a precise code extraction tool. Given a source file and an extraction goal, identify all line ranges that contain content relevant to the goal.

Rules:
- Return ONLY an array of [startLine, endLine] pairs using 1-indexed line numbers.
- Each pair identifies a contiguous block of relevant content.
- Include the full declaration (signature + body) for functions and types.
- If no content is relevant, return an empty array [].
- Do NOT include import statements or package declarations unless explicitly requested.
- Prefer complete logical blocks (entire function, entire struct) over partial snippets.`

// ExtractLineRanges runs a GBNF-constrained inference call against a single
// file's content and the extraction goal. Returns line ranges identifying
// relevant content. Returns nil for files with no relevant content.
func ExtractLineRanges(ctx context.Context, engine ProbeInferenceEngine, goal string, filePath string, content string, lineCount int) ([]LineRange, error) {
	userPrompt := fmt.Sprintf("## Goal\n%s\n\n## File: %s (%d lines)\n%s", goal, filePath, lineCount, content)

	// --- Defense 1: Proportional generation cap ---
	// Each range pair is ~8 tokens: [start, end],\n
	// A file can have at most lineCount/2 meaningful ranges.
	// Cap at lineCount * 3 tokens (generous) with floor 64 and ceiling 512.
	maxTok := lineCount * 3
	if maxTok < 64 {
		maxTok = 64
	}
	if maxTok > 512 {
		maxTok = 512
	}
	ctx = context.WithValue(ctx, inference.MaxTokensKey, maxTok)

	// --- Defense 2: DRY sampling to break repetition loops ---
	// The router model degenerates into [1,2],[1,4],[1,6]... patterns.
	// DRY detects and penalizes repeated token sequences at sampling time.
	ctx = context.WithValue(ctx, inference.DRYSamplingKey, inference.DRYSamplingConfig{
		Multiplier:    0.8,
		Base:          1.75,
		AllowedLength: 2,
		PenaltyLastN:  256,
	})

	// Use TargetWorker for line-range extraction — identifying which lines are
	// relevant to a goal requires content comprehension, not just classification.
	// The 1B router model lacks the capacity and degenerates into repetitive output.
	result, err := engine.Infer(ctx, lineRangeExtractionSystemPrompt, userPrompt, LineRangeExtractionSchema, TargetWorker)
	if err != nil {
		return nil, fmt.Errorf("line-range extraction failed for %s: %w", filePath, err)
	}

	// Parse the GBNF-constrained output: [[int, int], ...]
	var rawRanges [][]int
	if err := json.Unmarshal([]byte(result), &rawRanges); err != nil {
		// --- Defense 3: Truncation recovery ---
		// When generation hits the token cap, JSON is cut mid-array (e.g., "[[1,2],[1,4],[1,").
		// Try to salvage valid prefix data by finding the last complete "]" and closing the array.
		recovered := recoverTruncatedRanges(result)
		if recovered != nil {
			fmt.Fprintf(os.Stderr, "[ListNode] Recovered %d ranges from truncated output for %s\n", len(recovered), filePath)
			rawRanges = recovered
		} else {
			fmt.Fprintf(os.Stderr, "[ListNode] Failed to parse line ranges for %s: %v (raw len: %d)\n", filePath, err, len(result))
			return nil, nil // Graceful degradation — no ranges extracted
		}
	}

	var ranges []LineRange
	for _, pair := range rawRanges {
		if len(pair) != 2 {
			continue
		}
		ranges = append(ranges, LineRange{StartLine: pair[0], EndLine: pair[1]})
	}

	// --- Defense 4: Degeneration detector ---
	// The router model sometimes degenerates into patterns like [1,2],[1,4],[1,6]...
	// where every range starts at line 1. If >50% of ranges share the same start
	// line, the output is not meaningful extraction — collapse to the full file.
	if len(ranges) > 3 {
		startCounts := make(map[int]int)
		for _, r := range ranges {
			startCounts[r.StartLine]++
		}
		for startLine, count := range startCounts {
			if count > len(ranges)/2 {
				fmt.Fprintf(os.Stderr, "[ListNode] Degenerate output detected for %s: %d/%d ranges start at line %d, collapsing to full file\n",
					filePath, count, len(ranges), startLine)
				ranges = []LineRange{{StartLine: 1, EndLine: lineCount}}
				break
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[ListNode] Extracted %d ranges from %s\n", len(ranges), filePath)
	return ranges, nil
}

// MergeAndClampRanges deduplicates, merges overlapping/adjacent ranges,
// and clamps to file bounds [1, maxLine].
func MergeAndClampRanges(ranges []LineRange, maxLine int) []LineRange {
	if len(ranges) == 0 {
		return nil
	}

	// Clamp to bounds first
	var clamped []LineRange
	for _, r := range ranges {
		s := r.StartLine
		e := r.EndLine
		if s < 1 {
			s = 1
		}
		if e > maxLine {
			e = maxLine
		}
		if s > maxLine {
			s = maxLine
		}
		if s > e {
			s = e
		}
		clamped = append(clamped, LineRange{StartLine: s, EndLine: e})
	}

	// Sort by start line
	sort.Slice(clamped, func(i, j int) bool {
		return clamped[i].StartLine < clamped[j].StartLine
	})

	// Merge overlapping/adjacent ranges
	merged := []LineRange{clamped[0]}
	for i := 1; i < len(clamped); i++ {
		last := &merged[len(merged)-1]
		cur := clamped[i]
		if cur.StartLine <= last.EndLine+1 {
			// Overlapping or adjacent — extend
			if cur.EndLine > last.EndLine {
				last.EndLine = cur.EndLine
			}
		} else {
			merged = append(merged, cur)
		}
	}

	return merged
}

// FormatExtractedSnippets extracts the line ranges from file content and
// formats them with annotated dividers for human-readability and
// machine-parseability.
func FormatExtractedSnippets(filePath string, lines []string, ranges []LineRange) string {
	if len(ranges) == 0 {
		return ""
	}

	var b strings.Builder
	for i, r := range ranges {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("--- file: %s lines: %d-%d ---\n", filePath, r.StartLine, r.EndLine))

		// Extract lines (1-indexed to 0-indexed)
		start := r.StartLine - 1
		end := r.EndLine
		if start < 0 {
			start = 0
		}
		if end > len(lines) {
			end = len(lines)
		}
		for j := start; j < end; j++ {
			b.WriteString(lines[j])
			b.WriteString("\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// ChunkFile splits a large file into overlapping windows for extraction.
// Each chunk has a StartOffset indicating where it starts in the original file.
// Overlap ensures function boundaries aren't missed between chunks.
func ChunkFile(lines []string, chunkSize, overlap int) []FileChunk {
	if len(lines) <= chunkSize {
		return []FileChunk{{Lines: lines, StartOffset: 0}}
	}

	var chunks []FileChunk
	step := chunkSize - overlap
	if step < 1 {
		step = 1
	}

	for offset := 0; offset < len(lines); offset += step {
		end := offset + chunkSize
		if end > len(lines) {
			end = len(lines)
		}
		chunks = append(chunks, FileChunk{
			Lines:       lines[offset:end],
			StartOffset: offset,
		})
		if end == len(lines) {
			break
		}
	}

	return chunks
}

// recoverTruncatedRanges attempts to salvage valid line-range pairs from
// truncated JSON output. When the model hits the token limit mid-array,
// the JSON ends like "[[1,2],[1,4],[1," — find the last complete pair
// boundary and parse the valid prefix.
func recoverTruncatedRanges(raw string) [][]int {
	// Find the last complete inner array close: ]
	lastBracket := strings.LastIndex(raw, "]")
	if lastBracket < 2 {
		return nil
	}

	// Close the outer array after the last complete inner bracket
	candidate := raw[:lastBracket+1] + "]"

	var ranges [][]int
	if err := json.Unmarshal([]byte(candidate), &ranges); err != nil {
		return nil
	}

	// Filter to valid pairs only
	var valid [][]int
	for _, pair := range ranges {
		if len(pair) == 2 && pair[0] > 0 && pair[1] > 0 {
			valid = append(valid, pair)
		}
	}

	if len(valid) == 0 {
		return nil
	}
	return valid
}

// NumberFileContent prepends 1-indexed line numbers to file content for
// the extraction model's context.
func NumberFileContent(lines []string) string {
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%d: %s\n", i+1, line)
	}
	return b.String()
}
