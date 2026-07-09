package executor

import (
	"testing"

	"tzro/internal/tools"
)

// TestSpeculationFenceL0RealExecution verifies that L0 (read-only) tools
// execute for real during multi-branch rollout evaluation.
func TestSpeculationFenceL0RealExecution(t *testing.T) {
	mode := ClassifySpeculation("read_file", 2)
	if mode != SpecReal {
		t.Errorf("expected SpecReal for L0 tool 'read_file' with ceil=2, got %v", mode)
	}
}

// TestSpeculationFenceL1RealExecution verifies that L1 (prepare) tools
// execute for real when within the speculation ceiling.
func TestSpeculationFenceL1RealExecution(t *testing.T) {
	mode := ClassifySpeculation("save_memory", 2)
	if mode != SpecReal {
		t.Errorf("expected SpecReal for L1 tool 'save_memory' with ceil=2, got %v", mode)
	}
}

// TestSpeculationFenceL3Imagined verifies that L3 (reversible action) tools
// are simulated via the Local Model during rollout evaluation.
func TestSpeculationFenceL3Imagined(t *testing.T) {
	mode := ClassifySpeculation("create_table", 2)
	if mode != SpecImagined {
		t.Errorf("expected SpecImagined for L3 tool 'create_table' with ceil=2, got %v", mode)
	}
}

// TestSpeculationFenceL4Blocked verifies that L4 (external side effect) tools
// are completely blocked during speculative evaluation.
func TestSpeculationFenceL4Blocked(t *testing.T) {
	mode := ClassifySpeculation("web_search", 2)
	if mode != SpecBlocked {
		t.Errorf("expected SpecBlocked for L4 tool 'web_search' with ceil=2, got %v", mode)
	}
}

// TestSpeculationFenceCeilShiftsBoundary verifies that changing the ceiling
// reclassifies tools. With ceil=3, L3 tools should be SpecReal.
func TestSpeculationFenceCeilShiftsBoundary(t *testing.T) {
	mode := ClassifySpeculation("create_table", 3)
	if mode != SpecReal {
		t.Errorf("expected SpecReal for L3 tool 'create_table' with ceil=3, got %v", mode)
	}
}

// TestSpeculationFenceCeilAtL4IncludesExternal verifies that raising the
// ceiling to 4 allows L4 tools to be real-executed (not blocked).
func TestSpeculationFenceCeilAtL4IncludesExternal(t *testing.T) {
	mode := ClassifySpeculation("web_search", 4)
	if mode != SpecReal {
		t.Errorf("expected SpecReal for L4 tool 'web_search' with ceil=4, got %v", mode)
	}
}

// TestSpeculationFenceOverrideRespected verifies that proactivity level
// overrides are picked up by the Speculation Fence.
func TestSpeculationFenceOverrideRespected(t *testing.T) {
	// Override read_file from L0 to L4 — should now be blocked at default ceil
	tools.SetProactivityLevelOverride("read_file", tools.PLevelExternalSideEffect)
	defer tools.ClearProactivityLevelOverride("read_file")

	mode := ClassifySpeculation("read_file", 2)
	if mode != SpecBlocked {
		t.Errorf("expected SpecBlocked for overridden L4 'read_file' with ceil=2, got %v", mode)
	}
}

// TestSpeculationFenceUnknownToolDefaultsReal verifies that unknown tools
// (defaulting to L1 Prepare) are classified as SpecReal within the default ceiling.
func TestSpeculationFenceUnknownToolDefaultsReal(t *testing.T) {
	mode := ClassifySpeculation("completely_unknown_tool_xyz", 2)
	if mode != SpecReal {
		t.Errorf("expected SpecReal for unknown tool (defaults L1) with ceil=2, got %v", mode)
	}
}
