package inference

import (
	"testing"
)

func TestAssistantPrefilling_MessageInjection(t *testing.T) {
	// When OutputPrefix is set, an assistant message with the prefix content
	// should be appended to the messages array before calling the backend.
	req := StructuredInferenceRequest{
		Messages: []InferenceMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Write documentation for the API."},
		},
		OutputPrefix: "# API Documentation\n\n## Overview\n\n",
	}

	prepared := PrepareMessagesWithPrefix(req)

	// Should have 3 messages now: system, user, assistant
	if len(prepared) != 3 {
		t.Fatalf("expected 3 messages with prefix, got %d", len(prepared))
	}

	// Last message should be assistant with prefix content
	lastMsg := prepared[len(prepared)-1]
	if lastMsg.Role != "assistant" {
		t.Errorf("expected last message role 'assistant', got %q", lastMsg.Role)
	}
	if lastMsg.Content != "# API Documentation\n\n## Overview\n\n" {
		t.Errorf("expected assistant message to contain prefix, got %q", lastMsg.Content)
	}
}

func TestAssistantPrefilling_EmptyPrefix(t *testing.T) {
	// When OutputPrefix is empty, messages should be unchanged.
	req := StructuredInferenceRequest{
		Messages: []InferenceMessage{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Write documentation."},
		},
		OutputPrefix: "", // empty
	}

	prepared := PrepareMessagesWithPrefix(req)

	// Should still have 2 messages
	if len(prepared) != 2 {
		t.Fatalf("expected 2 messages without prefix, got %d", len(prepared))
	}
}

func TestAssistantPrefilling_ResultPrepended(t *testing.T) {
	// The OutputPrefix text should be prepended to the inference result
	// because llama.cpp returns only the generated continuation.
	prefix := "# Documentation\n\n"
	continuation := "This system uses a DAG-based execution model."

	result := PrependPrefixToResult(prefix, continuation)

	expected := "# Documentation\n\nThis system uses a DAG-based execution model."
	if result != expected {
		t.Errorf("expected prefix + continuation, got %q", result)
	}
}

func TestAssistantPrefilling_EmptyPrefixResult(t *testing.T) {
	// With empty prefix, result should be unchanged.
	result := PrependPrefixToResult("", "original content")
	if result != "original content" {
		t.Errorf("expected unchanged result, got %q", result)
	}
}
