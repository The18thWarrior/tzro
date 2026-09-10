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

	if !strings.Contains(redacted, "[REDACTED_OPENAI_KEY_") {
		t.Errorf("expected redacted placeholder, got %s", redacted)
	}

	// Rehydrate and verify exact match
	rehydrated := r.Rehydrate(redacted, mapping)
	if rehydrated != input {
		t.Errorf("expected exact rehydration match, got:\n%s", rehydrated)
	}
}

func TestRedactor_CustomDetectorsAndSessionIsolation(t *testing.T) {
	r := NewRedactor()
	err := r.AddDetector("internal_token", `\bcorp_token_[a-zA-Z0-9]{8,}\b`, "REDACTED_CORP_TOKEN")
	if err != nil {
		t.Fatalf("AddDetector failed: %v", err)
	}

	text := "Here is the internal token corp_token_999888777xyz"
	redacted1, map1 := r.RedactWithSession(text, "procA")
	redacted2, map2 := r.RedactWithSession(text, "procB")

	if !strings.Contains(redacted1, "[REDACTED_CORP_TOKEN_procA_1]") {
		t.Errorf("expected session procA placeholder, got %s", redacted1)
	}
	if !strings.Contains(redacted2, "[REDACTED_CORP_TOKEN_procB_1]") {
		t.Errorf("expected session procB placeholder, got %s", redacted2)
	}

	// Verify isolated rehydration
	if r.Rehydrate(redacted1, map1) != text {
		t.Errorf("failed to rehydrate procA")
	}
	if r.Rehydrate(redacted2, map2) != text {
		t.Errorf("failed to rehydrate procB")
	}
}
