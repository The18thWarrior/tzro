package compactor

import (
	"fmt"
	"strings"
	"testing"
)

func TestDictionaryEncoder_RoundTrip(t *testing.T) {
	// Build a test payload > 4KB with repeated path prefixes and symbol names
	pathPrefix := "github.com/The18thWarrior/tzro/internal/executor/"
	commonSchema := `{"type": "object", "properties": {"node_id": {"type": "string"}}}`
	repeatedFunc := "ExecuteSectionedSynthesisWithPrefixSlotting"

	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString(fmt.Sprintf("File %d: %snode_%d.go\n", i, pathPrefix, i))
		sb.WriteString(fmt.Sprintf("Signature: func (e *ExecutionEngine) %s(ctx context.Context, id string) error\n", repeatedFunc))
		sb.WriteString(fmt.Sprintf("Schema: %s\n\n", commonSchema))
	}
	original := sb.String()

	if len(original) < 4096 {
		t.Fatalf("Test setup error: input length is %d, expected > 4096", len(original))
	}

	encoder := NewDictionaryEncoder()
	encoded, dict, applied := encoder.Encode(original)

	if !applied {
		t.Fatalf("Expected dictionary encoding to be applied for repeated payload")
	}

	if len(dict) == 0 {
		t.Fatalf("Expected non-empty dictionary, got %d entries", len(dict))
	}

	if len(encoded) >= len(original) {
		t.Errorf("Expected compression: original %d chars, encoded %d chars", len(original), len(encoded))
	}

	// Verify header format
	if !strings.HasPrefix(encoded, "[DICTIONARY:") {
		t.Errorf("Expected encoded string to start with [DICTIONARY: header, got: %s", encoded[:min(len(encoded), 100)])
	}

	// Verify roundtrip decoding with explicit dict
	decoded := encoder.Decode(encoded, dict)
	if decoded != original {
		t.Errorf("Decode mismatch!\nExpected length %d, got %d", len(original), len(decoded))
	}

	// Verify decoding from embedded header
	decodedFromHeader, err := DecodeWithHeader(encoded)
	if err != nil {
		t.Fatalf("DecodeWithHeader failed: %v", err)
	}
	if decodedFromHeader != original {
		t.Errorf("DecodeWithHeader mismatch!\nExpected length %d, got %d", len(original), len(decodedFromHeader))
	}
}

func TestDictionaryEncoder_BelowThreshold(t *testing.T) {
	shortText := "Short text under 4KB with repeated foo foo foo foo foo."
	encoder := NewDictionaryEncoder()
	encoded, dict, applied := encoder.Encode(shortText)

	if applied {
		t.Errorf("Expected applied=false for text under 4KB threshold")
	}
	if len(dict) != 0 {
		t.Errorf("Expected empty dictionary, got %d entries", len(dict))
	}
	if encoded != shortText {
		t.Errorf("Expected unchanged text, got %q", encoded)
	}
}

func TestDictionaryEncoder_LowRepetition(t *testing.T) {
	// 5KB of unique alphanumeric lines
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("unique_line_identifier_index_%06d_random_data_chunk_%x\n", i, i*7919))
	}
	uniqueText := sb.String()

	encoder := NewDictionaryEncoder()
	encoded, dict, applied := encoder.Encode(uniqueText)

	if applied {
		t.Errorf("Expected applied=false when repetition is low")
	}
	if len(dict) != 0 {
		t.Errorf("Expected empty dictionary, got %d entries", len(dict))
	}
	if encoded != uniqueText {
		t.Errorf("Expected unchanged text, got %q", encoded)
	}
}
