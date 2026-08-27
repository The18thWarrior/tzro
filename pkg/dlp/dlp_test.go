package dlp

import (
	"strings"
	"testing"
)

func TestRedactor_RedactAndRehydrate(t *testing.T) {
	r := NewRedactor()

	input := `Here are the credentials:
OpenAI: sk-proj-1234567890abcdef1234567890abcdef
GitHub: ghp_1234567890abcdefghijklmnopqrstuvwxyz
AWS: AKIAIOSFODNN7EXAMPLE
`

	redacted, mapping := r.Redact(input)

	if strings.Contains(redacted, "sk-proj-") {
		t.Errorf("expected OpenAI key to be redacted")
	}
	if strings.Contains(redacted, "ghp_") {
		t.Errorf("expected GitHub PAT to be redacted")
	}
	if strings.Contains(redacted, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("expected AWS key to be redacted")
	}

	if !strings.Contains(redacted, "[REDACTED_OPENAI_KEY_1]") {
		t.Errorf("expected redacted placeholder, got %s", redacted)
	}

	// Rehydrate and verify exact match
	rehydrated := r.Rehydrate(redacted, mapping)
	if rehydrated != input {
		t.Errorf("expected exact rehydration match, got:\n%s", rehydrated)
	}
}
