package inference

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"tzro/internal/config"
)

// thermalCommandRunner is the function used to execute platform commands for thermal reads.
// Tests inject a mock; production uses the default which shells out via exec.Command.
var thermalCommandRunner = defaultCommandRunner

func defaultCommandRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ThermalState represents the normalized thermal pressure level of the host machine.
type ThermalState string

const (
	ThermalNominal  ThermalState = "nominal"
	ThermalFair     ThermalState = "fair"
	ThermalSerious  ThermalState = "serious"
	ThermalCritical ThermalState = "critical"
)

// parseDarwinThermalOutput parses `pmset -g therm` output and maps CPU_Speed_Limit
// (integer 0–100, where 100 = no throttling) to a ThermalState.
func parseDarwinThermalOutput(output string) ThermalState {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CPU_Speed_Limit") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				return ThermalNominal
			}
			val, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return ThermalNominal
			}
			switch {
			case val >= 100:
				return ThermalNominal
			case val >= 80:
				return ThermalFair
			case val >= 50:
				return ThermalSerious
			default:
				return ThermalCritical
			}
		}
	}
	return ThermalNominal
}

// temperatureCelsiusToState maps a temperature in °C to a ThermalState.
// Used by both Linux and Windows parsers which share the same thresholds.
func temperatureCelsiusToState(celsius float64) ThermalState {
	switch {
	case celsius > 90:
		return ThermalCritical
	case celsius >= 80:
		return ThermalSerious
	case celsius >= 70:
		return ThermalFair
	default:
		return ThermalNominal
	}
}

// parseLinuxThermalOutput parses sysfs thermal zone output (millidegrees Celsius).
// Input: integer string like "72000\n" → 72.0°C.
func parseLinuxThermalOutput(output string) ThermalState {
	val, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return ThermalNominal
	}
	celsius := float64(val) / 1000.0
	return temperatureCelsiusToState(celsius)
}

// parseWindowsThermalOutput parses WMI CurrentTemperature (tenths of Kelvin).
// Input: integer string like "3432\n" → (3432/10) - 273.15 = 70.05°C.
func parseWindowsThermalOutput(output string) ThermalState {
	val, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return ThermalNominal
	}
	celsius := (float64(val) / 10.0) - 273.15
	return temperatureCelsiusToState(celsius)
}

// thermalReadUnavailableLogged ensures the "thermal_read_unavailable" warning
// is emitted only once per process lifetime, not per-call.
var thermalReadUnavailableLogged bool

// ReadThermalState reads the host machine's thermal pressure using platform-specific
// commands and normalizes the result to a ThermalState. If the reading fails,
// returns ThermalNominal (graceful degradation — the speed floor remains as backup).
func ReadThermalState() ThermalState {
	switch runtime.GOOS {
	case "darwin":
		output, err := thermalCommandRunner("pmset", "-g", "therm")
		if err != nil {
			logThermalUnavailable()
			return ThermalNominal
		}
		return parseDarwinThermalOutput(output)

	case "linux":
		// Try thermal_zone0 through thermal_zone3
		for i := 0; i <= 3; i++ {
			path := fmt.Sprintf("/sys/class/thermal/thermal_zone%d/temp", i)
			data, err := os.ReadFile(path)
			if err == nil {
				return parseLinuxThermalOutput(string(data))
			}
		}
		logThermalUnavailable()
		return ThermalNominal

	case "windows":
		output, err := thermalCommandRunner("powershell", "-Command",
			"Get-CimInstance -Namespace root/WMI -ClassName MSAcpi_ThermalZoneTemperature | Select-Object -ExpandProperty CurrentTemperature")
		if err != nil {
			logThermalUnavailable()
			return ThermalNominal
		}
		return parseWindowsThermalOutput(output)

	default:
		logThermalUnavailable()
		return ThermalNominal
	}
}

func logThermalUnavailable() {
	if !thermalReadUnavailableLogged {
		thermalReadUnavailableLogged = true
		fmt.Fprintln(os.Stderr, "[Thermal] Thermal reading unavailable on this platform; relying on speed floor")
	}
}

// thermalStateReader is the function used to read thermal state.
// Tests inject a mock; production uses ReadThermalState.
var thermalStateReader = ReadThermalState

// thermalSleepFunc is the function used for cooldown pauses.
// Tests inject a no-op; production uses time.Sleep.
var thermalSleepFunc = time.Sleep

// CheckThermalPressure implements the pre-flight thermal gating check.
// Returns (proceed, escalateToCloud):
//   - (true, false): safe to proceed with local inference
//   - (false, true): escalate to cloud — either critical state or exhausted retries
func CheckThermalPressure(taskID, nodeID string, m *LocalModelManager) (proceed bool, escalateToCloud bool) {
	m.initMaps()

	// Check if this task is already in thermal cloud escalation
	m.fallbackMutex.RLock()
	escalationTime, hasEscalation := m.thermalCloudEscalationTime[taskID]
	m.fallbackMutex.RUnlock()

	if hasEscalation {
		cooldownMinutes := config.GetThermalCloudCooldownMinutes()
		if time.Since(escalationTime) > time.Duration(cooldownMinutes)*time.Minute {
			// Recovery: cooldown period elapsed, allow local retry
			m.fallbackMutex.Lock()
			delete(m.thermalCloudEscalationTime, taskID)
			m.fallbackMutex.Unlock()
			fmt.Fprintf(os.Stderr, "[Thermal] Task %s: thermal escalation recovered after %dm cooldown\n", taskID, cooldownMinutes)
			m.getPublisher().PublishEvent("thermal_pressure_recovered", taskID, nodeID, "Thermal pressure recovered after cloud cooldown period")
			// Fall through to re-sample current state
		} else {
			// Still in cooldown period — stay on cloud
			return false, true
		}
	}

	state := thermalStateReader()

	switch state {
	case ThermalNominal:
		return true, false

	case ThermalFair:
		// Observability only — proceed with local inference
		m.getPublisher().PublishEvent("thermal_pressure_fair", taskID, nodeID, fmt.Sprintf("Thermal pressure elevated: state=%s", state))
		return true, false

	case ThermalSerious:
		// Cooldown pause loop: max 2 retries (3 samples total including the first)
		const maxRetries = 2
		for retry := 0; retry < maxRetries; retry++ {
			m.getPublisher().PublishEvent("thermal_pressure_pause", taskID, nodeID,
				fmt.Sprintf("Thermal pressure serious: pausing %ds before inference (retry %d/%d)",
					config.GetThermalCooldownSeconds(), retry+1, maxRetries))
			thermalSleepFunc(time.Duration(config.GetThermalCooldownSeconds()) * time.Second)
			state = thermalStateReader()
			if state == ThermalNominal || state == ThermalFair {
				m.getPublisher().PublishEvent("thermal_pressure_recovered", taskID, nodeID, "Thermal pressure recovered after cooldown pause")
				return true, false
			}
		}
		// Exhausted retries — escalate to critical behavior
		return escalateThermalToCloud(taskID, nodeID, m)

	case ThermalCritical:
		return escalateThermalToCloud(taskID, nodeID, m)

	default:
		return true, false
	}
}

// escalateThermalToCloud records the thermal escalation and returns (false, true).
func escalateThermalToCloud(taskID, nodeID string, m *LocalModelManager) (bool, bool) {
	m.fallbackMutex.Lock()
	m.thermalCloudEscalationTime[taskID] = time.Now()
	m.fallbackMutex.Unlock()

	cooldownMinutes := config.GetThermalCloudCooldownMinutes()
	m.getPublisher().PublishEvent("thermal_pressure_fallback", taskID, nodeID,
		fmt.Sprintf("Thermal pressure critical: escalating to cloud for %dm cooldown", cooldownMinutes))

	return false, true
}
