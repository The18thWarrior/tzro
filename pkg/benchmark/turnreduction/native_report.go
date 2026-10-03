package turnreduction

import "math"

type ConditionTotal struct {
	Passed          int     `json:"passed"`
	Expected        int     `json:"expected"`
	Eligible        bool    `json:"eligible"`
	VerifiedSeconds float64 `json:"verified_total_s"`
	ElapsedSeconds  float64 `json:"elapsed_total_s"`
}

type EvaluationSummary struct {
	Status            string                       `json:"status"`
	Conditions        map[Condition]ConditionTotal `json:"conditions"`
	Comparator        Condition                    `json:"comparator,omitempty"`
	SpeedReductionPct *float64                     `json:"speed_reduction_pct"`
	Confirmed         bool                         `json:"confirmed"`
	Reason            string                       `json:"reason"`
}

// EvaluateMatrix applies the full-suite correctness gate before comparing time.
func EvaluateMatrix(fixtures []Fixture, cells []Observation, offline bool) EvaluationSummary {
	result := EvaluationSummary{Status: "incomplete", Conditions: map[Condition]ConditionTotal{}, Reason: "all nine fixtures and three conditions are required"}
	conditions := []Condition{ConditionNative, ConditionSimple, ConditionTzro}
	ids := map[string]bool{}
	for _, f := range fixtures {
		if ids[f.ID] {
			return result
		}
		ids[f.ID] = true
	}
	seen := map[string]bool{}
	for _, condition := range conditions {
		result.Conditions[condition] = ConditionTotal{Expected: len(fixtures), Eligible: true}
	}
	valid := true
	for _, cell := range cells {
		key := cell.FixtureID + "/" + string(cell.Condition)
		total, known := result.Conditions[cell.Condition]
		if !ids[cell.FixtureID] || !known || seen[key] {
			valid = false
			continue
		}
		seen[key] = true
		total.ElapsedSeconds += cell.ElapsedSeconds
		if cell.Passed && cell.VerifiedCompletionSeconds != nil && *cell.VerifiedCompletionSeconds > 0 && !math.IsNaN(*cell.VerifiedCompletionSeconds) && !math.IsInf(*cell.VerifiedCompletionSeconds, 0) {
			total.Passed++
			total.VerifiedSeconds += *cell.VerifiedCompletionSeconds
		}
		result.Conditions[cell.Condition] = total
	}
	for _, condition := range conditions {
		total := result.Conditions[condition]
		total.Eligible = valid && total.Passed == len(fixtures) && len(fixtures) == 9
		result.Conditions[condition] = total
	}
	if !valid || len(fixtures) != 9 || len(seen) != 27 {
		return result
	}
	if !result.Conditions[ConditionTzro].Eligible {
		result.Status = "incorrect"
		result.Reason = "Tzro did not verify every task"
		return result
	}
	for _, condition := range []Condition{ConditionNative, ConditionSimple} {
		total := result.Conditions[condition]
		if total.Eligible && (result.Comparator == "" || total.VerifiedSeconds < result.Conditions[result.Comparator].VerifiedSeconds) {
			result.Comparator = condition
		}
	}
	if result.Comparator == "" {
		result.Status = "ineligible_control"
		result.Reason = "neither control verified its complete suite"
		return result
	}
	control := result.Conditions[result.Comparator].VerifiedSeconds
	treatment := result.Conditions[ConditionTzro].VerifiedSeconds
	reduction := 100 * (1 - treatment/control)
	result.SpeedReductionPct = &reduction
	result.Status = "below_threshold"
	result.Reason = "observed reduction is below 20 percent"
	if treatment <= control*0.8 {
		result.Status = "screen_pass"
		result.Reason = "observed full-suite reduction is at least 20 percent; one pass is not confirmation"
	}
	if offline {
		result.Status = "offline_only"
		result.Reason = "scripted-client evidence validates the harness, not product performance"
	}
	return result
}
