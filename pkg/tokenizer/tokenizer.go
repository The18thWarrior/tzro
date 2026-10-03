package tokenizer

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
)

// Supported encoding names.
const (
	EncodingCl100kBase = "cl100k_base"
	EncodingO200kBase  = "o200k_base"
	EncodingDefault    = EncodingCl100kBase
)

// Token counting modes.
const (
	ModeExact     = "exact"
	ModeEstimated = "estimated"
)

var (
	ErrUnknownEncoding = errors.New("unknown tokenizer encoding; supported: cl100k_base, o200k_base")
	ErrBudgetTooSmall  = errors.New("token budget too small for minimum context envelope")
)

var (
	codecCache = make(map[string]tokenizer.Codec)
	cacheMu    sync.RWMutex
)

// GetCodec retrieves or initializes a cached Codec for the specified encoding.
func GetCodec(encName string) (tokenizer.Codec, error) {
	norm := strings.ToLower(strings.TrimSpace(encName))
	if norm == "" {
		norm = EncodingDefault
	}

	cacheMu.RLock()
	c, ok := codecCache[norm]
	cacheMu.RUnlock()
	if ok {
		return c, nil
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()

	// Double-check after lock
	if c, ok := codecCache[norm]; ok {
		return c, nil
	}

	var codec tokenizer.Codec
	var err error

	switch norm {
	case EncodingCl100kBase:
		codec, err = tokenizer.Get(tokenizer.Cl100kBase)
	case EncodingO200kBase:
		codec, err = tokenizer.Get(tokenizer.O200kBase)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownEncoding, encName)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize codec for %s: %w", norm, err)
	}

	codecCache[norm] = codec
	return codec, nil
}

// Count returns the exact BPE token count for text using the specified encoding.
func Count(text string, encName string) (int, error) {
	if text == "" {
		return 0, nil
	}
	codec, err := GetCodec(encName)
	if err != nil {
		return 0, err
	}
	return codec.Count(text)
}

// CountDefault returns exact BPE tokens using cl100k_base, falling back to rule-of-thumb estimate on error.
func CountDefault(text string) int {
	if text == "" {
		return 0
	}
	count, err := Count(text, EncodingDefault)
	if err == nil {
		return count
	}
	// Fallback rule of thumb (~4 chars per token)
	t := len(text) / 4
	if t == 0 && len(text) > 0 {
		return 1
	}
	return t
}

// TruncateToBudget trims text to a prefix containing no more than maxTokens, guaranteeing valid UTF-8.
func TruncateToBudget(text string, encName string, maxTokens int) (string, int, error) {
	if maxTokens <= 0 {
		return "", 0, nil
	}
	if text == "" {
		return "", 0, nil
	}

	codec, err := GetCodec(encName)
	if err != nil {
		return "", 0, err
	}

	currCount, err := codec.Count(text)
	if err != nil {
		return "", 0, err
	}
	if currCount <= maxTokens {
		return text, currCount, nil
	}

	// Tokenize to token IDs
	ids, _, err := codec.Encode(text)
	if err != nil {
		return "", 0, err
	}

	if len(ids) > maxTokens {
		ids = ids[:maxTokens]
	}

	// Decode back to string
	decoded, err := codec.Decode(ids)
	if err != nil {
		// Fallback slice by runes
		runes := []rune(text)
		estRunes := maxTokens * 4
		if estRunes > len(runes) {
			estRunes = len(runes)
		}
		decoded = string(runes[:estRunes])
	}

	// Ensure valid UTF-8
	for !utf8.ValidString(decoded) && len(decoded) > 0 {
		decoded = decoded[:len(decoded)-1]
	}

	// Re-verify exact token count
	finalCount, _ := codec.Count(decoded)
	for finalCount > maxTokens && len(ids) > 0 {
		ids = ids[:len(ids)-1]
		decoded, _ = codec.Decode(ids)
		for !utf8.ValidString(decoded) && len(decoded) > 0 {
			decoded = decoded[:len(decoded)-1]
		}
		finalCount, _ = codec.Count(decoded)
	}

	return decoded, finalCount, nil
}
