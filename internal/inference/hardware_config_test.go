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
