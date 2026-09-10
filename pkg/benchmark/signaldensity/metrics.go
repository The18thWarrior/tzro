package signaldensity

import (
	"math"
)

// ComputeSignalRetention computes SR = sum(A_T) / sum(A_R).
// Handles division-by-zero gracefully per spec: if baseline fails all tasks (sum(A_R) == 0),
// Tzro passes are credited proportionally (1.0 + sum(A_T)). If both are 0, SR is 1.0.
func ComputeSignalRetention(accTzroSum, accRawSum float64) float64 {
	if accRawSum <= 0 {
		if accTzroSum <= 0 {
			return 1.0
		}
		return 1.0 + accTzroSum
	}
	return accTzroSum / accRawSum
}

// ComputeCompressionRatio computes CR = sum(K_R) / sum(K_T).
// If Tzro tokens are 0, returns 1.0.
func ComputeCompressionRatio(rawTokens, tzroTokens int) float64 {
	if tzroTokens <= 0 {
		if rawTokens <= 0 {
			return 1.0
		}
		return float64(rawTokens)
	}
	return float64(rawTokens) / float64(tzroTokens)
}

// ComputeSDM computes Signal Density Multiplier: SDM = SR * CR.
func ComputeSDM(sr, cr float64) float64 {
	return sr * cr
}

// ComputeCompositeSDM computes the geometric mean across component SDM scores:
// Composite SDM = (prod_{c=1}^M SDM_c)^(1/M)
func ComputeCompositeSDM(sdms []float64) float64 {
	if len(sdms) == 0 {
		return 0.0
	}
	var sumLog float64
	for _, val := range sdms {
		if val <= 0 {
			return 0.0
		}
		sumLog += math.Log(val)
	}
	return math.Exp(sumLog / float64(len(sdms)))
}

// AggregateBattery calculates aggregate metrics for a single battery of test cases.
func AggregateBattery(name string, cases []CaseResult) BatteryResult {
	res := BatteryResult{
		Name:  name,
		Cases: cases,
	}

	if len(cases) == 0 {
		return res
	}

	var rawTokensSum, tzroTokensSum int
	var rawPassedCount, tzroPassedCount float64

	for _, c := range cases {
		rawTokensSum += c.RawPromptTokens
		tzroTokensSum += c.TzroPromptTokens
		if c.RawPassed {
			rawPassedCount += 1.0
		}
		if c.TzroPassed {
			tzroPassedCount += 1.0
		}
	}

	n := float64(len(cases))
	res.RawTokens = rawTokensSum
	res.TzroTokens = tzroTokensSum
	res.AccuracyRaw = rawPassedCount / n
	res.AccuracyTzro = tzroPassedCount / n

	res.SignalRetention = ComputeSignalRetention(tzroPassedCount, rawPassedCount)
	res.CompressionRatio = ComputeCompressionRatio(rawTokensSum, tzroTokensSum)
	res.SDM = ComputeSDM(res.SignalRetention, res.CompressionRatio)

	return res
}

// AggregateSummary aggregates all battery results into a BenchmarkSummary.
func AggregateSummary(batteries []BatteryResult) BenchmarkSummary {
	var summary BenchmarkSummary
	if len(batteries) == 0 {
		return summary
	}

	var totalRawTok, totalTzroTok int
	var totalRawCases, totalTzroCases float64
	var totalCases float64
	var sdms []float64

	for _, b := range batteries {
		totalRawTok += b.RawTokens
		totalTzroTok += b.TzroTokens
		for _, c := range b.Cases {
			totalCases++
			if c.RawPassed {
				totalRawCases++
			}
			if c.TzroPassed {
				totalTzroCases++
			}
		}
		sdms = append(sdms, b.SDM)
	}

	summary.RawTokens = totalRawTok
	summary.TzroTokens = totalTzroTok
	if totalCases > 0 {
		summary.OverallAccuracyRaw = totalRawCases / totalCases
		summary.OverallAccuracyTzro = totalTzroCases / totalCases
		summary.SignalRetention = ComputeSignalRetention(totalTzroCases, totalRawCases)
	} else {
		summary.SignalRetention = 1.0
	}
	summary.CompressionRatio = ComputeCompressionRatio(totalRawTok, totalTzroTok)
	summary.CompositeSDM = ComputeCompositeSDM(sdms)

	return summary
}
