package laya

import (
	"strings"
	"testing"
)

func TestSquash_LargeCompilerOutput(t *testing.T) {
	sa := NewStateAssembler(450)
	longLog := strings.Repeat("error on line 1\n", 1000)
	state := map[string]interface{}{
		"diagnostic": longLog,
	}

	compact, tokens, err := sa.CompactState(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens > 450 {
		t.Errorf("expected <= 450 tokens, got %d", tokens)
	}

	diag := compact["diagnostic"].(string)
	if !strings.Contains(diag, "lines elided") {
		t.Errorf("expected elided marker, got %s", diag)
	}
}

func TestSquash_50Candidates(t *testing.T) {
	sa := NewStateAssembler(450)
	var candidates []interface{}
	for i := 0; i < 50; i++ {
		candidates = append(candidates, "candidate")
	}
	state := map[string]interface{}{
		"candidates": candidates,
	}

	compact, _, err := sa.CompactState(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cands := compact["candidates"].([]interface{})
	if len(cands) != 5 {
		t.Errorf("expected 5 candidates, got %d", len(cands))
	}
}

func TestSquash_AlreadySmall(t *testing.T) {
	sa := NewStateAssembler(450)
	state := map[string]interface{}{
		"message": "hello",
	}

	compact, tokens, err := sa.CompactState(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if compact["message"] != "hello" {
		t.Errorf("expected 'hello', got %v", compact["message"])
	}
	if tokens != EstimateTokens(`{"message":"hello"}`) {
		t.Errorf("token mismatch: %d", tokens)
	}
}

func TestSquash_StrictBound(t *testing.T) {
	sa := NewStateAssembler(450)
	state := map[string]interface{}{}
	for i := 0; i < 5; i++ {
		state[string(rune('a'+i))] = "// docstring\n" + strings.Repeat("X", 400)
	}

	compact, tokens, err := sa.CompactState(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens > 450 {
		t.Errorf("expected <= 450, got %d", tokens)
	}
	if len(compact["a"].(string)) > 200 {
		t.Errorf("expected strict truncation to 200 chars")
	}
	if strings.Contains(compact["a"].(string), "// docstring") {
		t.Errorf("expected docstrings to be stripped")
	}
}

func TestEstimateTokens(t *testing.T) {
	text := "1234"
	tokens := EstimateTokens(text)
	if tokens != 2 {
		t.Errorf("expected 2, got %d", tokens)
	}
}
