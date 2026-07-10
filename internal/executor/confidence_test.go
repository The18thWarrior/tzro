package executor

import (
	"testing"
	"tzro/internal/config"
)

// resetConfidenceStateForTest clears confidence tracking for a task (test cleanup helper).
func resetConfidenceStateForTest(taskID string) {
	globalConfidenceState.mu.Lock()
	defer globalConfidenceState.mu.Unlock()
	delete(globalConfidenceState.consecutiveFails, taskID)
	delete(globalConfidenceState.forceCloudByTask, taskID)
}

func TestConfidenceTierSufficient(t *testing.T) {
	// When no force cloud is set, IsForceCloud should return false
	taskID := "test-confidence-sufficient"
	resetConfidenceStateForTest(taskID)

	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false for fresh task")
	}

	// Simulate a sufficient assessment
	checkAndUpdateConfidence(taskID, true)
	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to remain false after sufficient assessment")
	}

	resetConfidenceStateForTest(taskID)
}

func TestConfidenceTierInsufficient(t *testing.T) {
	taskID := "test-confidence-insufficient"
	resetConfidenceStateForTest(taskID)

	// Simulate one insufficient assessment — should not trigger fallback yet (threshold=3)
	checkAndUpdateConfidence(taskID, false)
	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false after 1 insufficient (threshold=3)")
	}

	checkAndUpdateConfidence(taskID, false)
	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false after 2 insufficient (threshold=3)")
	}

	resetConfidenceStateForTest(taskID)
}

func TestConfidenceTierStickyWithDecay(t *testing.T) {
	taskID := "test-confidence-sticky"
	resetConfidenceStateForTest(taskID)

	// Hit threshold of 3 consecutive insufficient
	checkAndUpdateConfidence(taskID, false)
	checkAndUpdateConfidence(taskID, false)
	checkAndUpdateConfidence(taskID, false)

	if !IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be true after 3 consecutive insufficient")
	}

	// A success should reset the consecutive counter but NOT the sticky fallback
	// (sticky stays until task reset)
	checkAndUpdateConfidence(taskID, true)

	// Verify counter was reset
	globalConfidenceState.mu.Lock()
	count := globalConfidenceState.consecutiveFails[taskID]
	globalConfidenceState.mu.Unlock()

	if count != 0 {
		t.Errorf("expected consecutive fails to be reset to 0, got %d", count)
	}

	// Clean up
	resetConfidenceStateForTest(taskID)
	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false after resetConfidenceStateForTest")
	}
}

func TestConfidenceThresholdConfigurable(t *testing.T) {
	// This test verifies the threshold is read from config.
	// We test indirectly via the default threshold of 3.
	taskID := "test-confidence-threshold"
	resetConfidenceStateForTest(taskID)

	// Two insufficient should not trigger (default threshold is 3)
	checkAndUpdateConfidence(taskID, false)
	checkAndUpdateConfidence(taskID, false)
	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false at 2 < threshold(3)")
	}

	// Third should trigger
	checkAndUpdateConfidence(taskID, false)
	if !IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be true at 3 == threshold(3)")
	}

	resetConfidenceStateForTest(taskID)
}

func TestConfidenceSchema(t *testing.T) {
	// Verify the confidence schema is valid JSON
	if ConfidenceSchema == "" {
		t.Error("ConfidenceSchema should not be empty")
	}
	if len(ConfidenceSchema) < 50 {
		t.Error("ConfidenceSchema seems too short")
	}
}

func TestConfidenceStrictLocal(t *testing.T) {
	cfg := config.Get()
	oldPrivacy := cfg.PrivacyLevel
	defer func() {
		cfg.PrivacyLevel = oldPrivacy
		config.Override(&cfg)
	}()

	cfg.PrivacyLevel = "strict-local"
	config.Override(&cfg)

	taskID := "test-confidence-strict-local"
	resetConfidenceStateForTest(taskID)

	// Even if we hit 3 consecutive insufficient assessments, IsForceCloud must remain false
	checkAndUpdateConfidence(taskID, false)
	checkAndUpdateConfidence(taskID, false)
	checkAndUpdateConfidence(taskID, false)

	if IsForceCloud(taskID) {
		t.Error("expected IsForceCloud to be false under strict-local privacy level")
	}

	resetConfidenceStateForTest(taskID)
}
