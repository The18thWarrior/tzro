package executor

import (
	"testing"
	"tzro/internal/compiler"
)

// TestPhaseGateApplies_RequiredToolDispatch validates ADR-0068:
// the phase gate fires when RequiredToolDispatch is set, regardless
// of SourceHint or isAnalyze detection from tool lists.
func TestPhaseGateApplies_RequiredToolDispatch(t *testing.T) {
	t.Run("BlocksWhenRequiredToolDispatchSet", func(t *testing.T) {
		config := &compiler.ProbeConfig{
			Goal:                "Count leads by country",
			AllowedTools:        []string{"sql_cached_data", "introspect_cache"},
			RequiredToolDispatch: []string{"sql_cached_data"},
			SourceHint:          "cache",
		}

		result := shouldPhaseGateApply(config)
		if !result {
			t.Error("phase gate should apply when RequiredToolDispatch is set")
		}
	})

	t.Run("BlocksEvenWithoutSourceHintCache", func(t *testing.T) {
		// Edge case: RequiredToolDispatch explicitly set but SourceHint
		// is missing (defensive). Gate should still fire.
		config := &compiler.ProbeConfig{
			Goal:                "Analyze data",
			AllowedTools:        []string{"sql_cached_data", "introspect_cache"},
			RequiredToolDispatch: []string{"sql_cached_data"},
			SourceHint:          "", // not set
		}

		result := shouldPhaseGateApply(config)
		if !result {
			t.Error("phase gate should apply when RequiredToolDispatch is set, even without SourceHint=cache")
		}
	})

	t.Run("DoesNotBlockWhenEmpty", func(t *testing.T) {
		// Regular probe with no RequiredToolDispatch and no cache SourceHint
		config := &compiler.ProbeConfig{
			Goal:         "Explore the codebase",
			AllowedTools: []string{"read_file", "list_dir"},
			SourceHint:   "filesystem",
		}

		result := shouldPhaseGateApply(config)
		if result {
			t.Error("phase gate should NOT apply for non-analyze probes without RequiredToolDispatch")
		}
	})

	t.Run("LegacyConditionStillWorks", func(t *testing.T) {
		// Legacy analyze detection: isAnalyze + SourceHint=cache + has sql_cached_data
		// but without RequiredToolDispatch set (backward compat)
		config := &compiler.ProbeConfig{
			Goal:         "Analyze data",
			AllowedTools: []string{"sql_cached_data", "introspect_cache"},
			SourceHint:   "cache",
		}

		result := shouldPhaseGateApply(config)
		if !result {
			t.Error("phase gate should apply for legacy analyze nodes with SourceHint=cache and sql_cached_data tool")
		}
	})
}

// TestRequiredToolsBlocked validates that the dispatch check correctly
// reports which required tools have and haven't been dispatched.
func TestRequiredToolsBlocked(t *testing.T) {
	t.Run("AllDispatched", func(t *testing.T) {
		required := []string{"sql_cached_data"}
		used := map[string]bool{"sql_cached_data": true, "introspect_cache": true}

		blocked, missing := requiredToolsBlocked(required, used)
		if blocked {
			t.Errorf("should not be blocked when all required tools dispatched, missing: %v", missing)
		}
	})

	t.Run("NoneDispatched", func(t *testing.T) {
		required := []string{"sql_cached_data"}
		used := map[string]bool{"introspect_cache": true}

		blocked, missing := requiredToolsBlocked(required, used)
		if !blocked {
			t.Error("should be blocked when required tools not dispatched")
		}
		if len(missing) != 1 || missing[0] != "sql_cached_data" {
			t.Errorf("expected missing=[sql_cached_data], got %v", missing)
		}
	})

	t.Run("EmptyRequiredNeverBlocks", func(t *testing.T) {
		required := []string{}
		used := map[string]bool{}

		blocked, _ := requiredToolsBlocked(required, used)
		if blocked {
			t.Error("empty RequiredToolDispatch should never block")
		}
	})
}
