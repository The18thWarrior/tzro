package inference

import (
	"bytes"
	"compress/flate"
	"context"
	"strings"
)

// generationGuardContextKey is a private type for the GenerationGuard context key.
type generationGuardContextKey struct{}

// GenerationGuardKey is a context key that callers set to register a
// GenerationGuard for the inference call. When present, streaming backends
// invoke the guard on each new line and abort generation if it returns
// GuardAbort. Use context.WithValue(ctx, GenerationGuardKey, guard).
var GenerationGuardKey = generationGuardContextKey{}

// GetGenerationGuard extracts a GenerationGuard from context, if present.
func GetGenerationGuard(ctx context.Context) GenerationGuard {
	if g, ok := ctx.Value(GenerationGuardKey).(GenerationGuard); ok {
		return g
	}
	return nil
}

// GenerationAbortedMarker is appended to output when generation is aborted
// by a GenerationGuard. Downstream consumers can check for this marker.
const GenerationAbortedMarker = "\n[GENERATION_ABORTED: repetition detected]"

// GuardAction is the result of a GenerationGuard check.
type GuardAction int

const (
	// GuardContinue indicates generation should proceed.
	GuardContinue GuardAction = iota
	// GuardAbort indicates generation should be terminated immediately.
	GuardAbort
)

// GenerationGuard is a per-inference quality gate registered on the Inference
// Backend that monitors output during generation. Streaming backends invoke
// OnChunk as content accumulates; non-streaming backends invoke it once with
// the complete response. Returns GuardAbort to terminate generation.
//
// ADR-0060: First implementation is RepetitionGuard for character-level
// collapse and block-level repetition detection.
type GenerationGuard interface {
	// OnChunk is called with the full accumulated content so far.
	// Implementations should be efficient — this is called on every newline
	// during streaming generation.
	OnChunk(accumulated string) GuardAction
}

// ContentMode indicates the type of content being generated, used to select
// appropriate degeneration thresholds for compression-ratio detection.
type ContentMode int

const (
	// ContentModeCode uses stricter compression-ratio threshold (< 0.20).
	// Code has more structure and less natural variation, so degenerate code
	// compresses even more dramatically than degenerate prose.
	ContentModeCode ContentMode = iota
	// ContentModeProse uses a more lenient threshold (< 0.35).
	// Valid prose has higher natural repetition (common words, articles).
	ContentModeProse
	// ContentModeTabular uses a very lenient threshold (< 0.10).
	// Tables (CSV, TSV, markdown) are structurally repetitive by design:
	// repeated delimiters, column headers, and similar row patterns compress
	// extremely well without being degenerate.
	ContentModeTabular
)

// RepetitionGuard detects degenerate repetition in generated output.
// Three detection tiers:
//   - Character-level: trailing single-char/pair repetition (backtick-space, dots)
//   - Block-level: sliding 10-line window hash, flagged at 3 consecutive matches
//   - Compression-ratio: periodic flate compression of trailing content; degenerate
//     text compresses dramatically better than valid text
type RepetitionGuard struct {
	lastLineCount   int
	lineHashes      []uint64
	contentMode     ContentMode
	lastCheckLen    int  // track last compression check position
	autoPromoted    bool // true if content mode was auto-promoted by detection
}

// NewRepetitionGuard creates a new RepetitionGuard with code content mode (default).
func NewRepetitionGuard() *RepetitionGuard {
	return &RepetitionGuard{contentMode: ContentModeCode}
}

// NewRepetitionGuardWithMode creates a new RepetitionGuard with a specific content mode.
func NewRepetitionGuardWithMode(mode ContentMode) *RepetitionGuard {
	return &RepetitionGuard{contentMode: mode}
}

// blockWindowSize is the number of lines in each block for hash comparison.
const blockWindowSize = 10

// blockRepeatThreshold is how many consecutive identical block hashes trigger abort.
const blockRepeatThreshold = 3

// compressionCheckInterval is how many characters must accumulate between
// compression-ratio checks. Checking every ~1K chars balances detection
// latency against CPU cost of the flate compression.
const compressionCheckInterval = 1024

// compressionWindowSize is the number of trailing characters to compress
// for the ratio check. 2K chars provides enough signal for meaningful
// compression ratio measurement.
const compressionWindowSize = 2048

// OnChunk checks the accumulated content for degenerate repetition patterns.
// Only performs checks when new lines appear (per-newline, not per-token).
func (g *RepetitionGuard) OnChunk(accumulated string) GuardAction {
	// Count current lines
	lineCount := countLines(accumulated)

	// Only check when new lines have appeared
	if lineCount <= g.lastLineCount {
		return GuardContinue
	}
	g.lastLineCount = lineCount

	// Auto-detect tabular content and promote to lenient mode.
	// Check trailing 500 chars for table markers to avoid scanning the full output.
	if !g.autoPromoted && g.contentMode != ContentModeTabular {
		if detectTabularContent(accumulated) {
			g.contentMode = ContentModeTabular
			g.autoPromoted = true
		}
	}

	// Tier 1: Character-level degeneration check on trailing content
	if g.checkCharacterDegeneration(accumulated) {
		return GuardAbort
	}

	// Minimum content length gate: block-level and compression-ratio checks
	// need sufficient signal to avoid false positives on short tables/lists.
	if len(accumulated) < minContentLengthForGuard {
		return GuardContinue
	}

	// Tier 2: Block-level repetition check
	if g.checkBlockRepetition(accumulated) {
		return GuardAbort
	}

	// Tier 3: Compression-ratio check on trailing content
	if g.checkCompressionRatio(accumulated) {
		return GuardAbort
	}

	return GuardContinue
}

// minContentLengthForGuard is the minimum accumulated output length before
// block-level and compression-ratio checks engage. Short outputs (tables,
// lists, structured data) don't have enough signal for meaningful detection.
const minContentLengthForGuard = 500

// detectTabularContent checks the trailing portion of accumulated output for
// tabular format markers: markdown tables, CSV, TSV.
func detectTabularContent(s string) bool {
	// Check trailing 500 chars for efficiency
	tail := s
	if len(tail) > 500 {
		tail = tail[len(tail)-500:]
	}

	// Markdown table: |---| separator line
	if strings.Contains(tail, "|---") || strings.Contains(tail, "| ---") {
		return true
	}

	// Check line-by-line for CSV/TSV patterns
	lines := strings.Split(tail, "\n")
	var csvCount, tsvCount int
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 5 {
			continue
		}
		// TSV: lines with 2+ tab characters
		if strings.Count(line, "\t") >= 2 {
			tsvCount++
		}
		// CSV: lines with 2+ commas not inside prose sentences.
		// Heuristic: comma density > 1 per 20 chars suggests tabular data.
		commaCount := strings.Count(line, ",")
		if commaCount >= 2 && float64(commaCount)/float64(len(line)) > 0.05 {
			csvCount++
		}
	}

	// If 3+ lines match TSV or CSV pattern, it's tabular
	return tsvCount >= 3 || csvCount >= 3
}

// checkCharacterDegeneration scans the tail of the accumulated content for
// degenerate character-level patterns (e.g., "` ` ` ` " or "........").
func (g *RepetitionGuard) checkCharacterDegeneration(s string) bool {
	if len(s) < 200 {
		return false
	}

	// Check last 200 characters for degenerate patterns
	tail := s[len(s)-200:]

	// Count how many characters are part of a repeating 1-2 char pattern
	degenerateCount := 0
	for i := 2; i < len(tail); i++ {
		// Check for 1-char repetition (e.g., ".....")
		if tail[i] == tail[i-1] && tail[i] == tail[i-2] {
			degenerateCount++
		}
		// Check for 2-char pair repetition (e.g., "` ` ` ")
		if i >= 3 && tail[i] == tail[i-2] && tail[i-1] == tail[i-3] {
			degenerateCount++
		}
	}

	// If >80% of the tail is degenerate, abort
	return degenerateCount > len(tail)*80/100
}

// checkBlockRepetition uses a sliding window of blockWindowSize lines,
// hashing each block and checking for blockRepeatThreshold consecutive
// identical hashes.
func (g *RepetitionGuard) checkBlockRepetition(s string) bool {
	lines := splitLines(s)
	if len(lines) < blockWindowSize*blockRepeatThreshold {
		return false // Not enough lines for detection
	}

	// Rebuild line hashes for the current content
	// We hash each blockWindowSize-line window
	numBlocks := len(lines) - blockWindowSize + 1
	if numBlocks < blockRepeatThreshold {
		return false
	}

	// Check from the end — most recent blocks
	// Hash the last few blockWindowSize-line blocks
	endIdx := len(lines)

	// We need at least blockRepeatThreshold consecutive identical blocks
	// Check if the last N blocks (stepping by blockWindowSize) are identical
	var lastHash uint64
	consecutiveMatches := 0

	for blockStart := endIdx - blockWindowSize; blockStart >= 0; blockStart -= blockWindowSize {
		blockEnd := blockStart + blockWindowSize
		h := hashLines(lines[blockStart:blockEnd])

		if consecutiveMatches == 0 {
			lastHash = h
			consecutiveMatches = 1
		} else if h == lastHash {
			consecutiveMatches++
			if consecutiveMatches >= blockRepeatThreshold {
				return true
			}
		} else {
			// Reset — blocks differ
			lastHash = h
			consecutiveMatches = 1
		}
	}

	return false
}

// checkCompressionRatio compresses the trailing content and checks if the
// compression ratio indicates degenerate repetition. Highly repetitive text
// compresses dramatically better than non-repetitive text.
//
// Research finding: compression-ratio detection is deterministic, zero-cost,
// and catches both exact and semantic repetition. Valid Go/TS code with
// Strategy patterns compresses to ~0.30-0.40; degenerate loops compress to
// ~0.05-0.15.
func (g *RepetitionGuard) checkCompressionRatio(s string) bool {
	// Code mode requires more content before compression checks fire.
	// Structured code (Go interfaces, Strategy patterns, repeated error
	// handling) compresses well by nature — 2048 chars is insufficient
	// signal to distinguish valid patterns from degenerate loops.
	minLen := compressionWindowSize
	if g.contentMode == ContentModeCode {
		minLen = 4096
	}
	if len(s) < minLen {
		return false
	}
	if len(s)-g.lastCheckLen < compressionCheckInterval {
		return false
	}
	g.lastCheckLen = len(s)

	// Take the trailing window for compression
	window := s
	if len(window) > compressionWindowSize {
		window = window[len(window)-compressionWindowSize:]
	}

	ratio := compressionRatio(window)

	// Select threshold based on content mode
	threshold := 0.35 // code: valid Go Strategy patterns / TS interfaces compress to ~0.30-0.40,
	                   // degenerate repetition loops compress to ~0.05-0.15
	switch g.contentMode {
	case ContentModeProse:
		threshold = 0.35 // prose: more lenient, valid prose compresses to ~0.45-0.60
	case ContentModeTabular:
		threshold = 0.10 // tabular: very lenient, tables compress to ~0.15-0.25 by design
	}

	return ratio < threshold
}

// compressionRatio computes compressed_size / uncompressed_size using flate.
// Returns a value between 0.0 (perfectly compressible / degenerate) and 1.0
// (incompressible / random). Returns 1.0 on error to avoid false positives.
func compressionRatio(s string) float64 {
	if len(s) == 0 {
		return 1.0
	}

	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return 1.0 // error → don't abort
	}
	_, _ = w.Write([]byte(s))
	_ = w.Close()

	return float64(buf.Len()) / float64(len(s))
}

// countLines counts the number of newline characters in s.
func countLines(s string) int {
	count := 0
	for _, c := range s {
		if c == '\n' {
			count++
		}
	}
	return count
}

// splitLines splits s into lines, preserving empty lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// hashLines produces a simple FNV-1a hash of the concatenated lines.
func hashLines(lines []string) uint64 {
	// FNV-1a
	var h uint64 = 14695981039346656037
	for _, line := range lines {
		for i := 0; i < len(line); i++ {
			h ^= uint64(line[i])
			h *= 1099511628211
		}
		// Include newline separator in hash
		h ^= uint64('\n')
		h *= 1099511628211
	}
	return h
}
