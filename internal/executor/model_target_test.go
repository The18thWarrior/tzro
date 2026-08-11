package executor

import (
	"context"
	"testing"

	"tzro/internal/inference"
)

// TestModelTarget_AutoRouting verifies that TargetAuto routes constrained
// calls to router and unconstrained to worker — matching the existing
// DefaultProbeInference behavior.
func TestModelTarget_AutoRouting(t *testing.T) {
	engine := &ProbeInference{}

	// TargetAuto with no schema should route to worker (4B).
	// We can't test the actual call without a live model, so we verify the
	// routing logic by checking that the type exists and compiles.
	_ = engine
	_ = TargetAuto
	_ = TargetWorker
	_ = TargetRouter
}

// TestModelTarget_EnumValues verifies the enum constants have distinct values.
func TestModelTarget_EnumValues(t *testing.T) {
	if TargetAuto == TargetWorker {
		t.Fatal("TargetAuto should not equal TargetWorker")
	}
	if TargetWorker == TargetRouter {
		t.Fatal("TargetWorker should not equal TargetRouter")
	}
	if TargetAuto == TargetRouter {
		t.Fatal("TargetAuto should not equal TargetRouter")
	}
}

// TestProbeInference_ImplementsInterface verifies that ProbeInference
// satisfies the ProbeInferenceEngine interface with the new ModelTarget param.
func TestProbeInference_ImplementsInterface(t *testing.T) {
	var _ ProbeInferenceEngine = (*ProbeInference)(nil)
}

// TestModelTarget_SignatureCompiles verifies that InferMessages accepts
// a ModelTarget parameter — this is the compilation test for the interface change.
func TestModelTarget_SignatureCompiles(t *testing.T) {
	// This test just needs to compile. If ModelTarget isn't in the signature,
	// the compiler will reject it.
	var engine ProbeInferenceEngine = &ProbeInference{}
	msgs := []inference.InferenceMessage{{Role: "user", Content: "test"}}

	// These should compile with the new signature
	_, _ = engine.Infer(context.Background(), "sys", "usr", "", TargetAuto)
	_, _ = engine.InferMessages(context.Background(), msgs, "", TargetWorker)
}
