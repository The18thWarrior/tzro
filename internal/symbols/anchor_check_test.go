package symbols

import (
	"strings"
	"testing"
)

func TestCheckAnchoring_AllAnchored(t *testing.T) {
	index := []Symbol{
		{Name: "InferenceBackend", Kind: SymbolInterface},
		{Name: "LlamaBackend", Kind: SymbolType},
		{Name: "NewLlamaBackend", Kind: SymbolFunc},
	}

	output := "The `InferenceBackend` interface is implemented by `LlamaBackend`. " +
		"Use `NewLlamaBackend` to create instances."

	result := CheckAnchoring(output, index, 0.20)

	if result.NeedsCorrection {
		t.Error("expected NeedsCorrection=false when all symbols are anchored")
	}
	if len(result.Unanchored) != 0 {
		t.Errorf("expected 0 unanchored, got %d: %v", len(result.Unanchored), result.Unanchored)
	}
	if result.Anchored != 3 {
		t.Errorf("expected 3 anchored, got %d", result.Anchored)
	}
}

func TestCheckAnchoring_HallucinatedNames(t *testing.T) {
	index := []Symbol{
		{Name: "InferenceBackend", Kind: SymbolInterface},
		{Name: "LlamaBackend", Kind: SymbolType},
	}

	// FakeModule and NonExistentType are hallucinated
	output := "The `InferenceBackend` handles requests. " +
		"The `FakeModule` manages inference. " +
		"The `NonExistentType` provides caching."

	result := CheckAnchoring(output, index, 0.20)

	if len(result.Unanchored) != 2 {
		t.Errorf("expected 2 unanchored, got %d: %v", len(result.Unanchored), result.Unanchored)
	}

	// Verify the hallucinated names are in the unanchored list
	unanchoredSet := make(map[string]bool)
	for _, name := range result.Unanchored {
		unanchoredSet[name] = true
	}
	if !unanchoredSet["FakeModule"] {
		t.Error("expected FakeModule in unanchored list")
	}
	if !unanchoredSet["NonExistentType"] {
		t.Error("expected NonExistentType in unanchored list")
	}
}

func TestCheckAnchoring_ExternalReferencesSkipped(t *testing.T) {
	index := []Symbol{
		{Name: "MyHandler", Kind: SymbolFunc},
	}

	// context.Context and sync.Mutex are dot-qualified — should be skipped
	output := "The `MyHandler` function takes a `context.Context` and uses a `sync.Mutex`."

	result := CheckAnchoring(output, index, 0.20)

	if result.ExternalSkipped < 1 {
		t.Errorf("expected at least 1 external reference skipped, got %d", result.ExternalSkipped)
	}
	if result.NeedsCorrection {
		t.Error("should not need correction when only external refs are unrecognized")
	}
}

func TestCheckAnchoring_ThresholdGating(t *testing.T) {
	// 10 symbols in the index
	index := make([]Symbol, 10)
	for i := range index {
		index[i] = Symbol{Name: strings.Repeat("Type", 1) + string(rune('A'+i)), Kind: SymbolType}
	}
	// TypeA through TypeJ

	t.Run("below_threshold", func(t *testing.T) {
		// Reference 10 symbols, 1 is fake (10% < 20% threshold)
		output := "`TypeA` and `TypeB` and `TypeC` and `TypeD` and `TypeE` " +
			"and `TypeF` and `TypeG` and `TypeH` and `TypeI` and `FakeOne`"

		result := CheckAnchoring(output, index, 0.20)

		if result.NeedsCorrection {
			t.Errorf("expected no correction at %.0f%% hallucination (threshold 20%%)", result.HallucinationPct*100)
		}
	})

	t.Run("above_threshold", func(t *testing.T) {
		// Reference 10 symbols, 4 are fake (40% > 20% threshold)
		output := "`TypeA` and `TypeB` and `TypeC` and `TypeD` " +
			"and `FakeOne` and `FakeTwo` and `FakeThree` and `FakeFour` " +
			"and `TypeE` and `TypeF`"

		result := CheckAnchoring(output, index, 0.20)

		if !result.NeedsCorrection {
			t.Errorf("expected correction at %.0f%% hallucination (threshold 20%%)", result.HallucinationPct*100)
		}
		if len(result.Unanchored) != 4 {
			t.Errorf("expected 4 unanchored, got %d: %v", len(result.Unanchored), result.Unanchored)
		}
	})
}

func TestBuildCorrectionPrompt(t *testing.T) {
	index := []Symbol{
		{Name: "InferenceBackend", Kind: SymbolInterface, Signature: "type InferenceBackend interface", File: "backend.go", Line: 5},
		{Name: "LlamaBackend", Kind: SymbolType, Signature: "type LlamaBackend struct", File: "backend.go", Line: 15},
	}

	output := "The `FakeModule` handles inference."
	unanchored := []string{"FakeModule"}

	prompt := BuildCorrectionPrompt(output, unanchored, index)

	// Should contain the hallucinated name
	if !strings.Contains(prompt, "FakeModule") {
		t.Error("correction prompt should contain the hallucinated name")
	}

	// Should contain the real symbols from the index
	if !strings.Contains(prompt, "InferenceBackend") {
		t.Error("correction prompt should contain InferenceBackend from the index")
	}
	if !strings.Contains(prompt, "LlamaBackend") {
		t.Error("correction prompt should contain LlamaBackend from the index")
	}

	// Should contain the original output
	if !strings.Contains(prompt, output) {
		t.Error("correction prompt should contain the original output")
	}

	// Should contain corrective instructions
	if !strings.Contains(prompt, "replacing") || !strings.Contains(prompt, "hallucinated") {
		t.Error("correction prompt should contain corrective instructions")
	}
}

func TestCheckAnchoring_EmptyIndex(t *testing.T) {
	output := "The `SomeType` does something."
	result := CheckAnchoring(output, nil, 0.20)

	// With empty index, everything is unanchored
	if result.TotalReferenced == 0 {
		t.Error("expected at least 1 referenced symbol")
	}
	// Should need correction if there are references and no index
	if result.TotalReferenced > 0 && !result.NeedsCorrection {
		t.Error("with empty index, all references should be unanchored → needs correction")
	}
}

func TestCheckAnchoring_EmptyOutput(t *testing.T) {
	index := []Symbol{
		{Name: "MyType", Kind: SymbolType},
	}
	result := CheckAnchoring("", index, 0.20)

	if result.NeedsCorrection {
		t.Error("empty output should not need correction")
	}
	if result.TotalReferenced != 0 {
		t.Errorf("expected 0 referenced in empty output, got %d", result.TotalReferenced)
	}
}

func TestCheckAnchoring_BoldReferences(t *testing.T) {
	index := []Symbol{
		{Name: "CompilerNode", Kind: SymbolType},
	}

	output := "The **CompilerNode** type represents a compiled DAG node."
	result := CheckAnchoring(output, index, 0.20)

	if result.Anchored != 1 {
		t.Errorf("expected 1 anchored from bold reference, got %d", result.Anchored)
	}
}

func TestCheckAnchoring_CodeBlockReferences(t *testing.T) {
	index := []Symbol{
		{Name: "RunProbe", Kind: SymbolFunc},
		{Name: "ProbeConfig", Kind: SymbolType},
	}

	output := "Example usage:\n```go\nconfig := ProbeConfig{}\nresult := RunProbe(ctx, config)\n```\n"
	result := CheckAnchoring(output, index, 0.20)

	if result.Anchored < 2 {
		t.Errorf("expected at least 2 anchored from code block, got %d", result.Anchored)
	}
}
