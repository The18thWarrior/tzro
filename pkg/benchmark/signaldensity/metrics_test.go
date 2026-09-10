package signaldensity

import (
	"math"
	"testing"
)

func TestMetrics_SignalRetention(t *testing.T) {
	// Standard case: baseline 4, tzro 5
	sr := ComputeSignalRetention(5.0, 4.0)
	if math.Abs(sr-1.25) > 1e-6 {
		t.Errorf("expected SR=1.25, got %f", sr)
	}

	// Division by zero safeguard: baseline 0, tzro 0
	srBothZero := ComputeSignalRetention(0.0, 0.0)
	if srBothZero != 1.0 {
		t.Errorf("expected SR=1.0 for (0,0), got %f", srBothZero)
	}

	// Baseline fails all, tzro passes 2
	srBaseZero := ComputeSignalRetention(2.0, 0.0)
	if srBaseZero != 3.0 { // 1.0 + 2.0
		t.Errorf("expected credited SR=3.0, got %f", srBaseZero)
	}
}

func TestMetrics_CompressionRatio(t *testing.T) {
	cr := ComputeCompressionRatio(20000, 4000)
	if math.Abs(cr-5.0) > 1e-6 {
		t.Errorf("expected CR=5.0, got %f", cr)
	}

	// Zero tzro tokens
	crZero := ComputeCompressionRatio(1000, 0)
	if crZero != 1000.0 {
		t.Errorf("expected CR=1000, got %f", crZero)
	}
}

func TestMetrics_SDM(t *testing.T) {
	sdm := ComputeSDM(1.25, 5.0)
	if math.Abs(sdm-6.25) > 1e-6 {
		t.Errorf("expected SDM=6.25, got %f", sdm)
	}
}

func TestMetrics_CompositeSDM(t *testing.T) {
	// Geometric mean of [4.0, 9.0] = sqrt(36) = 6.0
	sdms := []float64{4.0, 9.0}
	comp := ComputeCompositeSDM(sdms)
	if math.Abs(comp-6.0) > 1e-6 {
		t.Errorf("expected Composite SDM=6.0, got %f", comp)
	}

	// Empty
	if ComputeCompositeSDM(nil) != 0.0 {
		t.Errorf("expected 0 for empty slice")
	}

	// With zero or negative
	if ComputeCompositeSDM([]float64{4.0, 0.0}) != 0.0 {
		t.Errorf("expected 0 when an SDM is 0")
	}
}

func TestMetrics_AggregateBatteryAndSummary(t *testing.T) {
	cases := []CaseResult{
		{
			TaskID:           "case_1",
			RawPromptTokens:  1000,
			TzroPromptTokens: 200,
			RawPassed:        true,
			TzroPassed:       true,
		},
		{
			TaskID:           "case_2",
			RawPromptTokens:  1000,
			TzroPromptTokens: 200,
			RawPassed:        false,
			TzroPassed:       true,
		},
	}

	bat := AggregateBattery("test_battery", cases)
	if bat.RawTokens != 2000 {
		t.Errorf("expected 2000 raw tokens, got %d", bat.RawTokens)
	}
	if bat.TzroTokens != 400 {
		t.Errorf("expected 400 tzro tokens, got %d", bat.TzroTokens)
	}
	if bat.AccuracyRaw != 0.5 {
		t.Errorf("expected AccRaw=0.5, got %f", bat.AccuracyRaw)
	}
	if bat.AccuracyTzro != 1.0 {
		t.Errorf("expected AccTzro=1.0, got %f", bat.AccuracyTzro)
	}
	// SR = 2.0 / 1.0 = 2.0
	if math.Abs(bat.SignalRetention-2.0) > 1e-6 {
		t.Errorf("expected SR=2.0, got %f", bat.SignalRetention)
	}
	// CR = 2000 / 400 = 5.0
	if math.Abs(bat.CompressionRatio-5.0) > 1e-6 {
		t.Errorf("expected CR=5.0, got %f", bat.CompressionRatio)
	}
	// SDM = 2.0 * 5.0 = 10.0
	if math.Abs(bat.SDM-10.0) > 1e-6 {
		t.Errorf("expected SDM=10.0, got %f", bat.SDM)
	}

	summary := AggregateSummary([]BatteryResult{bat})
	if summary.RawTokens != 2000 || summary.TzroTokens != 400 {
		t.Errorf("summary token aggregation error")
	}
	if math.Abs(summary.CompositeSDM-10.0) > 1e-6 {
		t.Errorf("expected composite SDM=10.0, got %f", summary.CompositeSDM)
	}
}
