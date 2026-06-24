package memory

import (
	"testing"
)

func TestInferenceMetricsPersistence(t *testing.T) {
	DB.SetDBPathForTesting(":memory:")
	err := DB.Init()
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer DB.Close()

	// Initially, TPS should be 0.0 (no samples)
	tps := DB.GetAverageTPS(3600)
	if tps != 0.0 {
		t.Errorf("Expected 0.0 TPS with no samples, got %.2f", tps)
	}

	// Record some inference samples
	err = DB.RecordInferenceSample(100, 50, 2.0) // 50 tokens / 2.0s = 25 t/s
	if err != nil {
		t.Fatalf("RecordInferenceSample failed: %v", err)
	}

	err = DB.RecordInferenceSample(200, 100, 5.0) // 100 tokens / 5.0s = 20 t/s
	if err != nil {
		t.Fatalf("RecordInferenceSample failed: %v", err)
	}

	// Aggregate: 150 completion tokens / 7.0s ≈ 21.43 t/s
	tps = DB.GetAverageTPS(3600)
	if tps < 21.0 || tps > 22.0 {
		t.Errorf("Expected ~21.43 TPS, got %.2f", tps)
	}
}

func TestCacheEventsPersistence(t *testing.T) {
	DB.SetDBPathForTesting(":memory:")
	err := DB.Init()
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer DB.Close()

	// Initially, cache hit rate should be 1.0 (no events = default)
	rate := DB.GetDBCacheHitRate(3600)
	if rate != 1.0 {
		t.Errorf("Expected 1.0 default cache hit rate, got %.2f", rate)
	}

	// Record 3 hits and 1 miss → 75% hit rate
	_ = DB.RecordCacheEvent(true)
	_ = DB.RecordCacheEvent(true)
	_ = DB.RecordCacheEvent(true)
	_ = DB.RecordCacheEvent(false)

	rate = DB.GetDBCacheHitRate(3600)
	if rate < 0.74 || rate > 0.76 {
		t.Errorf("Expected 0.75 cache hit rate, got %.2f", rate)
	}
}
