package executor

import (
	"context"
	"strings"
	"testing"

	"tzro/internal/inference"
)

// ---------------------------------------------------------------------------
// Slice 2: Line-range extraction — GBNF grammar + verbatim copy
// ---------------------------------------------------------------------------

func TestMergeAndClampRanges_OverlappingRanges(t *testing.T) {
	input := []LineRange{{20, 35}, {30, 40}}
	result := MergeAndClampRanges(input, 100)

	if len(result) != 1 {
		t.Fatalf("MergeAndClampRanges: expected 1 merged range, got %d: %v", len(result), result)
	}
	if result[0].StartLine != 20 || result[0].EndLine != 40 {
		t.Errorf("MergeAndClampRanges: expected [20,40], got [%d,%d]", result[0].StartLine, result[0].EndLine)
	}
}

func TestMergeAndClampRanges_AdjacentRanges(t *testing.T) {
	input := []LineRange{{10, 20}, {21, 30}}
	result := MergeAndClampRanges(input, 100)

	if len(result) != 1 {
		t.Fatalf("MergeAndClampRanges: expected 1 merged range, got %d: %v", len(result), result)
	}
	if result[0].StartLine != 10 || result[0].EndLine != 30 {
		t.Errorf("MergeAndClampRanges: expected [10,30], got [%d,%d]", result[0].StartLine, result[0].EndLine)
	}
}

func TestMergeAndClampRanges_OutOfBounds(t *testing.T) {
	input := []LineRange{{500, 600}}
	result := MergeAndClampRanges(input, 400)

	if len(result) != 1 {
		t.Fatalf("MergeAndClampRanges: expected 1 clamped range, got %d", len(result))
	}
	if result[0].StartLine != 400 || result[0].EndLine != 400 {
		t.Errorf("MergeAndClampRanges: expected clamped to [400,400], got [%d,%d]", result[0].StartLine, result[0].EndLine)
	}
}

func TestMergeAndClampRanges_ClampStartAndEnd(t *testing.T) {
	input := []LineRange{{0, 50}, {380, 500}}
	result := MergeAndClampRanges(input, 400)

	if len(result) != 2 {
		t.Fatalf("MergeAndClampRanges: expected 2 ranges, got %d", len(result))
	}
	// Start clamped to 1
	if result[0].StartLine != 1 {
		t.Errorf("MergeAndClampRanges: expected start clamped to 1, got %d", result[0].StartLine)
	}
	// End clamped to maxLine
	if result[1].EndLine != 400 {
		t.Errorf("MergeAndClampRanges: expected end clamped to 400, got %d", result[1].EndLine)
	}
}

func TestMergeAndClampRanges_Empty(t *testing.T) {
	result := MergeAndClampRanges(nil, 100)
	if len(result) != 0 {
		t.Errorf("MergeAndClampRanges(nil): expected empty, got %d ranges", len(result))
	}
}

func TestMergeAndClampRanges_NonOverlapping(t *testing.T) {
	input := []LineRange{{10, 20}, {40, 50}, {70, 80}}
	result := MergeAndClampRanges(input, 100)

	if len(result) != 3 {
		t.Fatalf("MergeAndClampRanges: expected 3 separate ranges, got %d", len(result))
	}
}

func TestFormatExtractedSnippets(t *testing.T) {
	lines := []string{
		"package cache",
		"",
		"import \"sync\"",
		"",
		"// CacheEnvelope wraps cached data.",
		"type CacheEnvelope struct {",
		"\tCacheID string",
		"}",
		"",
		"func NewCacheID() string {",
		"\treturn \"test\"",
		"}",
	}

	ranges := []LineRange{{5, 8}, {10, 12}}
	output := FormatExtractedSnippets("internal/cache/cache.go", lines, ranges)

	// Check annotated dividers
	if !strings.Contains(output, "--- file: internal/cache/cache.go lines: 5-8 ---") {
		t.Errorf("FormatExtractedSnippets: missing annotated divider for range 5-8\nGot: %s", output)
	}
	if !strings.Contains(output, "--- file: internal/cache/cache.go lines: 10-12 ---") {
		t.Errorf("FormatExtractedSnippets: missing annotated divider for range 10-12\nGot: %s", output)
	}
	// Check verbatim content
	if !strings.Contains(output, "type CacheEnvelope struct {") {
		t.Errorf("FormatExtractedSnippets: missing verbatim content 'CacheEnvelope'\nGot: %s", output)
	}
	if !strings.Contains(output, "func NewCacheID() string {") {
		t.Errorf("FormatExtractedSnippets: missing verbatim content 'NewCacheID'\nGot: %s", output)
	}
}

func TestFormatExtractedSnippets_EmptyRanges(t *testing.T) {
	lines := []string{"line1", "line2"}
	output := FormatExtractedSnippets("file.go", lines, nil)
	if output != "" {
		t.Errorf("FormatExtractedSnippets(empty ranges): expected empty string, got %q", output)
	}
}

func TestChunkFile_SmallFile(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	chunks := ChunkFile(lines, 800, 50)
	if len(chunks) != 1 {
		t.Errorf("ChunkFile(100 lines, 800 chunk): expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].StartOffset != 0 {
		t.Errorf("ChunkFile: expected StartOffset=0, got %d", chunks[0].StartOffset)
	}
}

func TestChunkFile_LargeFile(t *testing.T) {
	lines := make([]string, 2000)
	for i := range lines {
		lines[i] = "line"
	}
	chunks := ChunkFile(lines, 800, 50)
	if len(chunks) < 3 {
		t.Errorf("ChunkFile(2000 lines, 800 chunk): expected >= 3 chunks, got %d", len(chunks))
	}
	// Verify overlap: second chunk starts before first chunk ends
	if chunks[1].StartOffset >= 800 {
		t.Errorf("ChunkFile: expected overlap, but chunk 2 starts at %d (no overlap with 800-line chunk)", chunks[1].StartOffset)
	}
	// Verify all lines are covered
	lastChunk := chunks[len(chunks)-1]
	lastEnd := lastChunk.StartOffset + len(lastChunk.Lines)
	if lastEnd < 2000 {
		t.Errorf("ChunkFile: last chunk ends at %d, but file has 2000 lines", lastEnd)
	}
}

type mockMessageCapturingEngine struct {
	capturedMessages []inference.InferenceMessage
	response         string
}

func (m *mockMessageCapturingEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string, target ModelTarget) (string, error) {
	return m.response, nil
}

func (m *mockMessageCapturingEngine) InferMessages(ctx context.Context, messages []inference.InferenceMessage, jsonSchema string, target ModelTarget) (string, error) {
	m.capturedMessages = messages
	return m.response, nil
}

func TestExtractLineRanges_StaticPrefixSlotting(t *testing.T) {
	mockEngine := &mockMessageCapturingEngine{
		response: "[[1, 5]]",
	}

	goal := "Find all cache functions"
	filePath := "internal/cache/cache.go"
	content := "func NewCache() {}\nfunc Get() {}\nfunc Set() {}\nfunc Delete() {}\nfunc Close() {}"
	lineCount := 5

	ranges, err := ExtractLineRanges(context.Background(), mockEngine, goal, filePath, content, lineCount)
	if err != nil {
		t.Fatalf("ExtractLineRanges failed: %v", err)
	}
	if len(ranges) != 1 || ranges[0].StartLine != 1 || ranges[0].EndLine != 5 {
		t.Fatalf("Unexpected ranges: %v", ranges)
	}

	// Verify 4-turn invariant structure (ADR-0092)
	msgs := mockEngine.capturedMessages
	if len(msgs) != 4 {
		t.Fatalf("Expected 4 messages for prefix slotting, got %d: %v", len(msgs), msgs)
	}

	if msgs[0].Role != "system" || msgs[0].Content != lineRangeExtractionSystemPrompt {
		t.Errorf("Turn 1 (system): expected %q, got %q", lineRangeExtractionSystemPrompt, msgs[0].Content)
	}

	if msgs[1].Role != "user" || !strings.Contains(msgs[1].Content, goal) {
		t.Errorf("Turn 2 (user): expected goal %q, got %q", goal, msgs[1].Content)
	}

	if msgs[2].Role != "assistant" || !strings.Contains(msgs[2].Content, "Ready") {
		t.Errorf("Turn 3 (assistant): expected synthetic ack, got %q", msgs[2].Content)
	}

	if msgs[3].Role != "user" || !strings.Contains(msgs[3].Content, filePath) || !strings.Contains(msgs[3].Content, content) {
		t.Errorf("Turn 4 (user): expected file content tail, got %q", msgs[3].Content)
	}
}

