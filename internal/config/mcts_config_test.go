package config

import "testing"

func TestConfig_MCTSMaxDepthDefault(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSMaxDepth
	GlobalConfig.MCTSMaxDepth = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSMaxDepth = saved
		configMutex.Unlock()
	}()

	got := GetMCTSMaxDepth()
	if got != 3 {
		t.Errorf("expected default 3, got %d", got)
	}
}

func TestConfig_MCTSMaxDepthExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSMaxDepth
	GlobalConfig.MCTSMaxDepth = 5
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSMaxDepth = saved
		configMutex.Unlock()
	}()

	got := GetMCTSMaxDepth()
	if got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestConfig_MCTSMaxSimulationsDefault(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSMaxSimulations
	GlobalConfig.MCTSMaxSimulations = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSMaxSimulations = saved
		configMutex.Unlock()
	}()

	got := GetMCTSMaxSimulations()
	if got != 3 {
		t.Errorf("expected default 3, got %d", got)
	}
}

func TestConfig_MCTSMaxSimulationsExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSMaxSimulations
	GlobalConfig.MCTSMaxSimulations = 5
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSMaxSimulations = saved
		configMutex.Unlock()
	}()

	got := GetMCTSMaxSimulations()
	if got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestConfig_MCTSSpeculationCeilDefault(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSSpeculationCeil
	GlobalConfig.MCTSSpeculationCeil = 0
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSSpeculationCeil = saved
		configMutex.Unlock()
	}()

	got := GetMCTSSpeculationCeil()
	if got != 2 {
		t.Errorf("expected default 2 (L2-Suggest), got %d", got)
	}
}

func TestConfig_MCTSSpeculationCeilExplicit(t *testing.T) {
	configMutex.Lock()
	saved := GlobalConfig.MCTSSpeculationCeil
	GlobalConfig.MCTSSpeculationCeil = 3
	configMutex.Unlock()
	defer func() {
		configMutex.Lock()
		GlobalConfig.MCTSSpeculationCeil = saved
		configMutex.Unlock()
	}()

	got := GetMCTSSpeculationCeil()
	if got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}
