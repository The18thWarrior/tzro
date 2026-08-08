package inference

// PrepareMessagesWithPrefix applies assistant prefilling (FM1 mitigation) to
// the request messages. When OutputPrefix is set, an assistant message containing
// the prefix is appended to the messages array. The llama.cpp /v1/chat/completions
// endpoint continues generation from this prefix, bypassing the model's
// instruction-tuned "helpful assistant" response patterns.
//
// When OutputPrefix is empty, returns the original messages unchanged.
func PrepareMessagesWithPrefix(req StructuredInferenceRequest) []InferenceMessage {
	if req.OutputPrefix == "" {
		return req.Messages
	}

	// Copy messages to avoid mutating the original slice
	prepared := make([]InferenceMessage, len(req.Messages), len(req.Messages)+1)
	copy(prepared, req.Messages)

	// Append assistant message with prefix
	prepared = append(prepared, InferenceMessage{
		Role:    "assistant",
		Content: req.OutputPrefix,
	})

	return prepared
}

// PrependPrefixToResult combines the output prefix with the model's generated
// continuation. The llama.cpp server returns only the tokens generated AFTER
// the assistant prefix, so the prefix must be prepended to form the complete output.
//
// When prefix is empty, returns the continuation unchanged.
func PrependPrefixToResult(prefix, continuation string) string {
	if prefix == "" {
		return continuation
	}
	return prefix + continuation
}
