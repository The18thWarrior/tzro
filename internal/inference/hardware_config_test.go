package inference

import (
	"testing"

	"tzro/internal/config"
)

func intPtr(v int) *int { return &v }

func TestGetGPULayerCount_ConfigOverride(t *testing.T) {
	mgr := &LocalModelManager{}

	// Save original values and restore after test
	origGPU := config.GlobalConfig.GPULayers
	defer func() { config.GlobalConfig.GPULayers = origGPU }()

	tests := []struct {
		name     string
		layers   *int
		expected int
	}{
		{"explicit -1 forces all layers", intPtr(-1), -1},
		{"explicit 0 forces CPU-only", intPtr(0), 0},
		{"explicit 20 forces 20 layers", intPtr(20), 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.GlobalConfig.GPULayers = tt.layers
			got := mgr.getGPULayerCount()
			if got != tt.expected {
				t.Errorf("getGPULayerCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestGetGPULayerCount_NilUsesDefault(t *testing.T) {
	mgr := &LocalModelManager{}

	origGPU := config.GlobalConfig.GPULayers
	defer func() { config.GlobalConfig.GPULayers = origGPU }()

	config.GlobalConfig.GPULayers = nil
	got := mgr.getGPULayerCount()

	// On Apple Silicon this should be -1, on others 0.
	// Just verify it returns a valid value without panicking.
	if got < -1 {
		t.Errorf("getGPULayerCount() with nil config returned invalid value: %d", got)
	}
}

func TestGetPerformanceCoresCount_ConfigOverride(t *testing.T) {
	mgr := &LocalModelManager{}

	origThreads := config.GlobalConfig.ThreadCount
	origGPU := config.GlobalConfig.GPULayers
	defer func() {
		config.GlobalConfig.ThreadCount = origThreads
		config.GlobalConfig.GPULayers = origGPU
	}()

	tests := []struct {
		name     string
		threads  *int
		expected int
	}{
		{"explicit 4 threads", intPtr(4), 4},
		{"explicit 2 threads", intPtr(2), 2},
		{"explicit 16 threads", intPtr(16), 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.GlobalConfig.ThreadCount = tt.threads
			config.GlobalConfig.GPULayers = nil

			got := mgr.getPerformanceCoresCount()
			if got != tt.expected {
				t.Errorf("getPerformanceCoresCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestGetPerformanceCoresCount_ZeroOverrideIgnored(t *testing.T) {
	mgr := &LocalModelManager{}

	origThreads := config.GlobalConfig.ThreadCount
	defer func() { config.GlobalConfig.ThreadCount = origThreads }()

	// Zero thread override should fall through to platform auto-detect
	config.GlobalConfig.ThreadCount = intPtr(0)

	got := mgr.getPerformanceCoresCount()
	if got <= 0 {
		t.Errorf("getPerformanceCoresCount() with 0 override should fall through to platform auto, got %d", got)
	}
}

func TestGetPerformanceCoresCount_NilUsesDefault(t *testing.T) {
	mgr := &LocalModelManager{}

	origThreads := config.GlobalConfig.ThreadCount
	origGPU := config.GlobalConfig.GPULayers
	defer func() {
		config.GlobalConfig.ThreadCount = origThreads
		config.GlobalConfig.GPULayers = origGPU
	}()

	config.GlobalConfig.ThreadCount = nil
	config.GlobalConfig.GPULayers = nil

	got := mgr.getPerformanceCoresCount()
	// Should return a positive value based on platform detection
	if got <= 0 {
		t.Errorf("getPerformanceCoresCount() with nil config returned non-positive value: %d", got)
	}
}

// --- Slice 7: getSystemMemoryGB returns correct value ---

func TestGetSystemMemoryGB_ReturnsPositiveValue(t *testing.T) {
	gb := getSystemMemoryGB()
	if gb <= 0 {
		t.Errorf("getSystemMemoryGB() returned %d, expected positive value", gb)
	}
	// Sanity: should be at least 4GB for any modern dev machine
	if gb < 4 {
		t.Errorf("getSystemMemoryGB() returned %d GB, suspiciously low", gb)
	}
}

// --- Slice 8 & 10: getWorkerParallelSlots returns correct value ---

func TestGetWorkerParallelSlots(t *testing.T) {
	tests := []struct {
		name     string
		memoryGB int
		expected int
	}{
		{"8GB system gets 1 slot", 8, 1},
		{"16GB system gets 1 slot", 16, 1},
		{"23GB system gets 1 slot", 23, 1},
		{"24GB system gets 2 slots", 24, 2},
		{"32GB system gets 2 slots", 32, 2},
		{"64GB system gets 2 slots", 64, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getWorkerParallelSlots(tt.memoryGB)
			if got != tt.expected {
				t.Errorf("getWorkerParallelSlots(%d) = %d, want %d", tt.memoryGB, got, tt.expected)
			}
		})
	}
}

// --- getRouterContextSize: memory-gated router context window ---

func TestGetRouterContextSize(t *testing.T) {
	tests := []struct {
		name     string
		memoryGB int
		expected int
	}{
		{"8GB system gets 16K context", 8, 16384},
		{"15GB system gets 16K context", 15, 16384},
		{"16GB system gets 64K context", 16, 65536},
		{"24GB system gets 64K context", 24, 65536},
		{"32GB system gets 64K context", 32, 65536},
		{"64GB system gets 64K context", 64, 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getRouterContextSize(tt.memoryGB)
			if got != tt.expected {
				t.Errorf("getRouterContextSize(%d) = %d, want %d", tt.memoryGB, got, tt.expected)
			}
		})
	}
}

// --- getAbsoluteRSSThreshold: memory-proportional RSS safety net ---

func TestGetAbsoluteRSSThreshold(t *testing.T) {
	const GB = 1024 * 1024 * 1024
	tests := []struct {
		name     string
		memoryGB int
		expected int64
	}{
		{"8GB system gets 3GB floor", 8, 3 * GB},
		{"12GB system gets 3GB floor", 12, 3 * GB},
		{"16GB system gets 4GB (25%)", 16, 4 * GB},
		{"24GB system gets 6GB (25%)", 24, 6 * GB},
		{"32GB system gets 8GB (25%)", 32, 8 * GB},
		{"64GB system gets 16GB (25%)", 64, 16 * GB},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getAbsoluteRSSThreshold(tt.memoryGB)
			if got != tt.expected {
				t.Errorf("getAbsoluteRSSThreshold(%d) = %d, want %d", tt.memoryGB, got, tt.expected)
			}
		})
	}
}
