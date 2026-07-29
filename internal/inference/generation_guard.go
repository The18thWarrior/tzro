package inference

import "context"

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

// RepetitionGuard detects degenerate repetition in generated output.
// Two detection tiers:
//   - Character-level: trailing single-char/pair repetition (backtick-space, dots)
//   - Block-level: sliding 10-line window hash, flagged at 3 consecutive matches
type RepetitionGuard struct {
	lastLineCount int
	lineHashes    []uint64
}

// NewRepetitionGuard creates a new RepetitionGuard.
func NewRepetitionGuard() *RepetitionGuard {
	return &RepetitionGuard{}
}

// blockWindowSize is the number of lines in each block for hash comparison.
const blockWindowSize = 10

// blockRepeatThreshold is how many consecutive identical block hashes trigger abort.
const blockRepeatThreshold = 3

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

	// Tier 1: Character-level degeneration check on trailing content
	if g.checkCharacterDegeneration(accumulated) {
		return GuardAbort
	}

	// Tier 2: Block-level repetition check
	if g.checkBlockRepetition(accumulated) {
		return GuardAbort
	}

	return GuardContinue
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
