//go:build integration

package hooks

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Quality Signal Scoring — verify that token-reduced modes still find bugs
// ---------------------------------------------------------------------------

// QualitySignal defines a bug or finding the agent should identify.
// Match is case-insensitive substring search across ALL keywords (AND logic).
// AltKeywords provides alternative keyword sets (OR with the primary).
type QualitySignal struct {
	Name        string     // Human-readable name of the bug
	Keywords    []string   // All must appear in the answer (AND)
	AltKeywords [][]string // Alternative keyword sets — any set matching counts (OR)
}

// QualityResult holds the scoring outcome for a single run.
type QualityResult struct {
	Found   int
	Total   int
	Score   float64 // Found / Total as percentage
	Details []SignalDetail
}

// SignalDetail records whether a specific signal was found.
type SignalDetail struct {
	Name  string
	Found bool
}

// scoreQuality checks the final answer against expected quality signals.
// Returns how many bugs/findings the agent correctly identified.
func scoreQuality(answer string, signals []QualitySignal) QualityResult {
	lower := strings.ToLower(answer)
	result := QualityResult{Total: len(signals)}

	for _, sig := range signals {
		found := matchKeywords(lower, sig.Keywords)

		// Try alternatives if primary didn't match
		if !found && len(sig.AltKeywords) > 0 {
			for _, alt := range sig.AltKeywords {
				if matchKeywords(lower, alt) {
					found = true
					break
				}
			}
		}

		if found {
			result.Found++
		}
		result.Details = append(result.Details, SignalDetail{Name: sig.Name, Found: found})
	}

	if result.Total > 0 {
		result.Score = float64(result.Found) / float64(result.Total) * 100
	}
	return result
}

// matchKeywords returns true if ALL keywords appear in the lowered text.
func matchKeywords(lower string, keywords []string) bool {
	for _, kw := range keywords {
		if !strings.Contains(lower, strings.ToLower(kw)) {
			return false
		}
	}
	return len(keywords) > 0
}

// logQualityComparison prints a quality comparison table for 3-way results.
func logQualityComparison(t *testing.T, scenarioName string, signals []QualitySignal,
	baseline, hooked, fullTzro QualityResult) {
	t.Helper()

	t.Logf("\n═══════════════════════════════════════════════════════")
	t.Logf("  QUALITY SCORECARD: %s", scenarioName)
	t.Logf("═══════════════════════════════════════════════════════")

	t.Logf("┌────────────────────────────────────┬──────────┬──────────┬──────────┐")
	t.Logf("│ Bug / Finding                      │ Baseline │ Hooked   │ Full Tzro│")
	t.Logf("├────────────────────────────────────┼──────────┼──────────┼──────────┤")

	for i, sig := range signals {
		bMark := "   ✗"
		hMark := "   ✗"
		fMark := "   ✗"
		if baseline.Details[i].Found {
			bMark = "   ✓"
		}
		if hooked.Details[i].Found {
			hMark = "   ✓"
		}
		if fullTzro.Details[i].Found {
			fMark = "   ✓"
		}
		// Pad name to 36 chars
		name := sig.Name
		if len(name) > 34 {
			name = name[:34]
		}
		t.Logf("│ %-34s │ %s      │ %s      │ %s      │", name, bMark, hMark, fMark)
	}

	t.Logf("├────────────────────────────────────┼──────────┼──────────┼──────────┤")
	t.Logf("│ TOTAL                              │   %d/%-3d  │   %d/%-3d  │   %d/%-3d  │",
		baseline.Found, baseline.Total, hooked.Found, hooked.Total, fullTzro.Found, fullTzro.Total)
	t.Logf("│ Score                              │  %5.1f%%  │  %5.1f%%  │  %5.1f%%  │",
		baseline.Score, hooked.Score, fullTzro.Score)
	t.Logf("└────────────────────────────────────┴──────────┴──────────┴──────────┘")
}

// ---------------------------------------------------------------------------
// Per-scenario quality signal definitions
// ---------------------------------------------------------------------------

// Go inventory workspace (used by TestPiCoderE2E)
var goInventorySignals = []QualitySignal{
	{
		Name:     "Token expiry: ns not seconds",
		Keywords: []string{"time.second"},
		AltKeywords: [][]string{
			{"nanosecond"},
			{"expiry", "wrong"},
			{"tokenttl", "duration"},
			{"3600ns"},
		},
	},
	{
		Name:     "Auth middleware: 500 not 401",
		Keywords: []string{"500", "401"},
		AltKeywords: [][]string{
			{"statusinternal", "unauthorized"},
			{"middleware", "wrong", "status"},
		},
	},
	{
		Name:     "UpdateStock: float64 not int",
		Keywords: []string{"float64", "int"},
		AltKeywords: [][]string{
			{"delta", "type mismatch"},
			{"delta", "float"},
			{"body.delta", "type"},
		},
	},
	{
		Name:     "Worker: race condition",
		Keywords: []string{"race"},
		AltKeywords: [][]string{
			{"concurrent", "grab", "job"},
			{"select", "update", "lock"},
			{"worker", "race"},
		},
	},
	{
		Name:     "Notification: no idempotency",
		Keywords: []string{"idempoten"},
		AltKeywords: [][]string{
			{"notification", "already sent"},
			{"notification", "duplicate"},
		},
	},
}

// TS Monorepo workspace
var tsMonorepoSignals = []QualitySignal{
	{
		Name:     "useAuth: stale token after refresh",
		Keywords: []string{"stale"},
		AltKeywords: [][]string{
			{"refresh", "context"},
			{"token", "not", "update"},
			{"settoken", "context"},
		},
	},
	{
		Name:     "Auth middleware: 200 not 401",
		Keywords: []string{"200", "401"},
		AltKeywords: [][]string{
			{"middleware", "wrong", "status"},
			{"unauthorized", "200"},
			{"status", "code", "200"},
		},
	},
	{
		Name:     "User.id type: string vs number",
		Keywords: []string{"string", "number"},
		AltKeywords: [][]string{
			{"type", "mismatch", "id"},
			{"id", "string", "type"},
			{"frontend", "backend", "type"},
		},
	},
}

// Python ML pipeline workspace
var pythonMLSignals = []QualitySignal{
	{
		Name:     "Learning rate: 0.1 too high",
		Keywords: []string{"learning_rate", "0.1"},
		AltKeywords: [][]string{
			{"lr", "0.1"},
			{"learning rate", "high"},
			{"learning_rate", "too"},
			{"0.001", "0.1"},
		},
	},
	{
		Name:     "Dropout: 0.0 disabled",
		Keywords: []string{"dropout", "0.0"},
		AltKeywords: [][]string{
			{"dropout", "disable"},
			{"dropout", "zero"},
			{"dropout", "removed"},
			{"no dropout"},
			{"dropout", "0"},
		},
	},
	{
		Name:     "Loss explosion / divergence",
		Keywords: []string{"explod"},
		AltKeywords: [][]string{
			{"diverge"},
			{"loss", "increasing"},
			{"loss", "unstable"},
			{"gradient", "explod"},
			{"nan", "loss"},
		},
	},
}

// Rust service workspace
var rustServiceSignals = []QualitySignal{
	{
		Name:     "Proto uint32 vs Rust i64 mismatch",
		Keywords: []string{"uint32", "i64"},
		AltKeywords: [][]string{
			{"u32", "i64"},
			{"proto", "type", "mismatch"},
			{"protobuf", "mismatch"},
			{"user_id", "type"},
		},
	},
	{
		Name:     "Serialization/encode error",
		Keywords: []string{"serializ"},
		AltKeywords: [][]string{
			{"encodeerror"},
			{"encode", "protobuf"},
			{"prost", "error"},
			{"encode", "mismatch"},
		},
	},
}
