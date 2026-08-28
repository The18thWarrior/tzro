package dlp

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	openAIKeyRe = regexp.MustCompile(`\bsk-(?:proj-|live-)?[a-zA-Z0-9_\-]{20,}\b`)
	githubPatRe = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{36,}\b`)
	awsKeyRe    = regexp.MustCompile(`\b(?:AKIA|ASIA|AROA)[A-Z0-9]{16}\b`)
	jwtRe       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
	privKeyRe   = regexp.MustCompile(`-----BEGIN [A-Z ]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+ PRIVATE KEY-----`)
)

// Redactor handles on-device secret masking and bidirectional rehydration.
type Redactor struct {
	mu sync.RWMutex
}

// NewRedactor initializes a new DLP Redactor.
func NewRedactor() *Redactor {
	return &Redactor{}
}

// Redact scans the text for secrets, masks them, and returns the redacted text along with the restoration map.
func (r *Redactor) Redact(text string) (string, map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	mapping := make(map[string]string) // placeholder -> original
	counter := 1

	replaceFunc := func(orig, prefix string) string {
		placeholder := fmt.Sprintf("[%s_%d]", prefix, counter)
		counter++
		mapping[placeholder] = orig
		return placeholder
	}

	// 1. Private keys
	text = privKeyRe.ReplaceAllStringFunc(text, func(m string) string {
		return replaceFunc(m, "REDACTED_PRIVATE_KEY")
	})

	// 2. OpenAI API Keys
	text = openAIKeyRe.ReplaceAllStringFunc(text, func(m string) string {
		return replaceFunc(m, "REDACTED_OPENAI_KEY")
	})

	// 3. GitHub PATs
	text = githubPatRe.ReplaceAllStringFunc(text, func(m string) string {
		return replaceFunc(m, "REDACTED_GITHUB_TOKEN")
	})

	// 4. AWS Keys
	text = awsKeyRe.ReplaceAllStringFunc(text, func(m string) string {
		return replaceFunc(m, "REDACTED_AWS_KEY")
	})

	// 5. JWT Tokens
	text = jwtRe.ReplaceAllStringFunc(text, func(m string) string {
		return replaceFunc(m, "REDACTED_JWT_TOKEN")
	})

	return text, mapping
}

// Rehydrate replaces placeholder tokens with their original secret values.
func (r *Redactor) Rehydrate(text string, mapping map[string]string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for placeholder, original := range mapping {
		text = strings.ReplaceAll(text, placeholder, original)
	}
	return text
}
