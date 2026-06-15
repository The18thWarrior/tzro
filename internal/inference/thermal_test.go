package inference

import (
	"fmt"
	"testing"
	"time"
)

// Slice 1: Parse macOS pmset output → ThermalState mapping
func TestParseDarwinThermalOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   ThermalState
	}{
		{"nominal at 100", "CPU_Speed_Limit = 100\n", ThermalNominal},
		{"fair at 85", "CPU_Speed_Limit = 85\n", ThermalFair},
		{"fair at boundary 80", "CPU_Speed_Limit = 80\n", ThermalFair},
		{"serious at 65", "CPU_Speed_Limit = 65\n", ThermalSerious},
		{"serious at boundary 50", "CPU_Speed_Limit = 50\n", ThermalSerious},
		{"critical at 40", "CPU_Speed_Limit = 40\n", ThermalCritical},
		{"critical at 0", "CPU_Speed_Limit = 0\n", ThermalCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDarwinThermalOutput(tt.output)
			if got != tt.want {
				t.Errorf("parseDarwinThermalOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// Slice 2: Parse Linux sysfs output → millidegree conversion + thresholds
func TestParseLinuxThermalOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   ThermalState
	}{
		{"nominal at 55C", "55000\n", ThermalNominal},
		{"nominal at 69C", "69999\n", ThermalNominal},
		{"fair at 70C", "70000\n", ThermalFair},
		{"fair at 75C", "75000\n", ThermalFair},
		{"serious at 80C", "80000\n", ThermalSerious},
		{"serious at 85C", "85000\n", ThermalSerious},
		{"critical at 91C", "91000\n", ThermalCritical},
		{"critical at 100C", "100000\n", ThermalCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLinuxThermalOutput(tt.output)
			if got != tt.want {
				t.Errorf("parseLinuxThermalOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// Slice 3: Parse Windows WMI output → tenths-of-Kelvin conversion + thresholds
func TestParseWindowsThermalOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   ThermalState
	}{
		// 60°C = (60+273.15)*10 = 3331.5 → 3332
		{"nominal at 60C", "3332\n", ThermalNominal},
		// 70°C = 3432
		{"fair at 70C", "3432\n", ThermalFair},
		// 80°C = 3532
		{"serious at 80C", "3532\n", ThermalSerious},
		// 91°C = 3642
		{"critical at 91C", "3642\n", ThermalCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWindowsThermalOutput(tt.output)
			if got != tt.want {
				t.Errorf("parseWindowsThermalOutput(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// Slice 4: Graceful degradation — unparseable/empty output returns nominal
func TestThermalGracefulDegradation(t *testing.T) {
	// All parsers should return nominal for garbage input
	if got := parseDarwinThermalOutput(""); got != ThermalNominal {
		t.Errorf("darwin empty: got %q, want nominal", got)
	}
	if got := parseDarwinThermalOutput("garbage output\n"); got != ThermalNominal {
		t.Errorf("darwin garbage: got %q, want nominal", got)
	}
	if got := parseLinuxThermalOutput("not-a-number\n"); got != ThermalNominal {
		t.Errorf("linux garbage: got %q, want nominal", got)
	}
	if got := parseLinuxThermalOutput(""); got != ThermalNominal {
		t.Errorf("linux empty: got %q, want nominal", got)
	}
	if got := parseWindowsThermalOutput(""); got != ThermalNominal {
		t.Errorf("windows empty: got %q, want nominal", got)
	}
	if got := parseWindowsThermalOutput("xyz\n"); got != ThermalNominal {
		t.Errorf("windows garbage: got %q, want nominal", got)
	}

	// ReadThermalState with a failing command runner should return nominal
	origRunner := thermalCommandRunner
	defer func() { thermalCommandRunner = origRunner }()
	thermalCommandRunner = func(name string, args ...string) (string, error) {
		return "", fmt.Errorf("command not found")
	}
	if got := ReadThermalState(); got != ThermalNominal {
		t.Errorf("ReadThermalState with failed runner: got %q, want nominal", got)
	}
}

// Slice 5: Cooldown pause loop — mock declining states, verify retry behavior
func TestThermalCooldownPauseLoop(t *testing.T) {
	// Simulate: serious → serious → fair (recovers on 3rd sample)
	callCount := 0
	origReader := thermalStateReader
	defer func() { thermalStateReader = origReader }()
	thermalStateReader = func() ThermalState {
		callCount++
		if callCount <= 2 {
			return ThermalSerious
		}
		return ThermalFair
	}

	// Use a short cooldown for fast tests
	origSleep := thermalSleepFunc
	defer func() { thermalSleepFunc = origSleep }()
	sleepCalls := 0
	thermalSleepFunc = func(d time.Duration) {
		sleepCalls++
	}

	mgr := &LocalModelManager{}
	mgr.initMaps()

	proceed, escalate := CheckThermalPressure("task-1", "node-1", mgr)
	if !proceed {
		t.Error("expected proceed=true after recovery")
	}
	if escalate {
		t.Error("expected escalate=false after recovery")
	}
	if sleepCalls != 2 {
		t.Errorf("expected 2 cooldown sleeps, got %d", sleepCalls)
	}
}

// Slice 6: Cloud escalation — serious never recovers → escalate to cloud
func TestThermalCloudEscalation(t *testing.T) {
	origReader := thermalStateReader
	defer func() { thermalStateReader = origReader }()
	thermalStateReader = func() ThermalState {
		return ThermalSerious // never recovers
	}

	origSleep := thermalSleepFunc
	defer func() { thermalSleepFunc = origSleep }()
	thermalSleepFunc = func(d time.Duration) {} // no-op

	mgr := &LocalModelManager{}
	mgr.initMaps()

	proceed, escalate := CheckThermalPressure("task-2", "node-1", mgr)
	if proceed {
		t.Error("expected proceed=false when thermal never recovers")
	}
	if !escalate {
		t.Error("expected escalate=true when serious exhausts retries")
	}

	// Verify escalation time was recorded
	mgr.fallbackMutex.RLock()
	_, hasEscalation := mgr.thermalCloudEscalationTime["task-2"]
	mgr.fallbackMutex.RUnlock()
	if !hasEscalation {
		t.Error("expected thermalCloudEscalationTime to be set for task-2")
	}
}

// Slice 6b: Critical state immediately escalates (no cooldown loop)
func TestThermalCriticalImmediateEscalation(t *testing.T) {
	origReader := thermalStateReader
	defer func() { thermalStateReader = origReader }()
	thermalStateReader = func() ThermalState {
		return ThermalCritical
	}

	origSleep := thermalSleepFunc
	defer func() { thermalSleepFunc = origSleep }()
	sleepCalls := 0
	thermalSleepFunc = func(d time.Duration) {
		sleepCalls++
	}

	mgr := &LocalModelManager{}
	mgr.initMaps()

	proceed, escalate := CheckThermalPressure("task-3", "node-1", mgr)
	if proceed {
		t.Error("expected proceed=false for critical thermal state")
	}
	if !escalate {
		t.Error("expected escalate=true for critical thermal state")
	}
	if sleepCalls != 0 {
		t.Errorf("expected no sleep for critical (immediate escalation), got %d sleeps", sleepCalls)
	}
}

// Slice 7: Escalation recovery — after cooldown period, thermal flag resets
func TestThermalEscalationRecovery(t *testing.T) {
	mgr := &LocalModelManager{}
	mgr.initMaps()

	// Simulate an escalation that happened 6 minutes ago (> 5 min default cooldown)
	mgr.fallbackMutex.Lock()
	mgr.thermalCloudEscalationTime["task-4"] = time.Now().Add(-6 * time.Minute)
	mgr.fallbackMutex.Unlock()

	// Now read with nominal thermal state
	origReader := thermalStateReader
	defer func() { thermalStateReader = origReader }()
	thermalStateReader = func() ThermalState {
		return ThermalNominal
	}

	proceed, escalate := CheckThermalPressure("task-4", "node-1", mgr)
	if !proceed {
		t.Error("expected proceed=true after thermal recovery period")
	}
	if escalate {
		t.Error("expected escalate=false after thermal recovery period")
	}

	// Verify escalation was cleaned up
	mgr.fallbackMutex.RLock()
	_, stillEscalated := mgr.thermalCloudEscalationTime["task-4"]
	mgr.fallbackMutex.RUnlock()
	if stillEscalated {
		t.Error("expected thermalCloudEscalationTime to be cleared after recovery")
	}
}

// Slice 7b: Escalation NOT recovered if still within cooldown period
func TestThermalEscalationNotRecoveredYet(t *testing.T) {
	mgr := &LocalModelManager{}
	mgr.initMaps()

	// Simulate an escalation that happened 2 minutes ago (< 5 min default cooldown)
	mgr.fallbackMutex.Lock()
	mgr.thermalCloudEscalationTime["task-5"] = time.Now().Add(-2 * time.Minute)
	mgr.fallbackMutex.Unlock()

	origReader := thermalStateReader
	defer func() { thermalStateReader = origReader }()
	thermalStateReader = func() ThermalState {
		return ThermalNominal
	}

	proceed, escalate := CheckThermalPressure("task-5", "node-1", mgr)
	if proceed {
		t.Error("expected proceed=false while still in thermal cooldown period")
	}
	if !escalate {
		t.Error("expected escalate=true while still in thermal cooldown period")
	}
}
