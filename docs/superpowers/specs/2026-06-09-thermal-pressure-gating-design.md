# Thermal Pressure Gating

## Problem

tzro's local inference sidecar (llama-server) drives sustained GPU/CPU load during DAG execution. On consumer hardware — particularly laptops — this causes significant heat buildup. The existing speed-floor mechanism detects throughput degradation *after* thermal throttling has already occurred, but does not proactively manage the user's hardware temperature.

The goal is hardware stewardship: detect elevated thermal pressure from the OS and gate inference execution before the machine reaches damaging or uncomfortable temperatures, even if throughput hasn't degraded yet.

## Scope

- Cross-platform: macOS (Apple Silicon + Intel), Windows, Linux
- CGO-free: all platform-specific reads use `exec.Command` or file reads, matching the existing `sysctl` pattern in `getPerformanceCoresCount()`
- On-demand pre-flight check: no background polling goroutine — sample thermal state synchronously before each inference call in `ExecuteStructured`
- Tiered response: cooldown pause for moderate pressure, cloud escalation for critical pressure

## Unified Thermal State Model

Normalize each platform's native thermal signal into a 4-level enum:

| Level | Meaning | Routing Action |
|---|---|---|
| `nominal` | Normal operating range | None |
| `fair` | Elevated, OS beginning to manage | Emit telemetry event (observability only) |
| `serious` | Active throttling imminent or underway | Cooldown pause before inference |
| `critical` | Sustained high thermal pressure | Immediate cloud escalation |

### Threshold Mapping

The enum is derived from platform-specific readings:

**macOS** — `pmset -g therm`

Parses `CPU_Speed_Limit` from output (integer 0–100, where 100 = no throttling). This command requires no root privileges.

| CPU_Speed_Limit | Thermal Level |
|---|---|
| 100 | `nominal` |
| 80–99 | `fair` |
| 50–79 | `serious` |
| < 50 | `critical` |

**Windows** — `powershell -Command "Get-CimInstance -Namespace root/WMI -ClassName MSAcpi_ThermalZoneTemperature | Select-Object -ExpandProperty CurrentTemperature"`

Returns temperature in tenths of Kelvin. Convert: `°C = (value / 10) - 273.15`.

| Temperature (°C) | Thermal Level |
|---|---|
| < 70 | `nominal` |
| 70–80 | `fair` |
| 80–90 | `serious` |
| > 90 | `critical` |

Fallback if WMI thermal zones are not populated (common on some hardware): return `nominal` and log a warning. The speed floor remains as the secondary safety net.

**Linux** — Read `/sys/class/thermal/thermal_zone0/temp`

Returns millidegrees Celsius (integer). Convert: `°C = value / 1000`.

Same °C threshold mapping as Windows.

Fallback: if the file does not exist, try `thermal_zone1` through `thermal_zone3`. If none exist, return `nominal` with a warning.

## Integration Point

### Pre-flight Check in ExecuteStructured

Insert the thermal check in `routing.go` inside `ExecuteStructured`, after determining the model mode but before dispatching to local inference. The check only applies when the routing decision is local or cooperative-local (not cloud-only).

```
ExecuteStructured(ctx, req)
  │
  ├─ cloud-only mode → skip thermal check, dispatch to cloud
  │
  ├─ local / cooperative mode
  │     │
  │     ├─ PRE-FLIGHT: CheckThermalPressure()
  │     │     ├─ nominal/fair → proceed
  │     │     ├─ serious → cooldown loop (pause, re-sample, retry)
  │     │     └─ critical → set forceCloudFallback, dispatch to cloud
  │     │
  │     ├─ existing: start/wait for backend if stopped
  │     ├─ existing: call local model
  │     └─ existing: post-call speed floor check
  │
  └─ fallback heuristics / cloud
```

### Tiered Response Behavior

**`serious` — Cooldown Pause Loop:**

1. Log telemetry event: `thermal_pressure_pause`
2. Sleep for `ThermalCooldownSeconds` (configurable, default 30)
3. Re-sample thermal state
4. If recovered to `nominal` or `fair` → proceed with local inference
5. If still `serious` → repeat (max 2 retries, total max ~90s of waiting)
6. If still `serious` after retries → escalate to `critical` behavior

**`critical` — Cloud Escalation:**

1. Log telemetry event: `thermal_pressure_fallback`
2. Set `forceCloudFallback[taskID] = true` (reuses existing mechanism from speed-floor)
3. Record `thermalCloudEscalationTime[taskID] = time.Now()`
4. Dispatch to cloud for this call
5. Subsequent calls for the same taskID check: if `time.Since(escalationTime) > ThermalCloudCooldownMinutes` → reset fallback flag, allow local retry

**Important**: Thermal escalation uses a **separate** map (`thermalCloudEscalationTime`) rather than sharing the speed-floor's `forceCloudFallback` map. This is because:

- Speed-floor escalation is **permanent** for the task (once speed degrades, it stays on cloud)
- Thermal escalation is **transient** (the machine cools down, local inference should resume)

If both mechanisms wrote to the same `forceCloudFallback` map, a thermal recovery reset could incorrectly clear a speed-floor escalation.

### Recovery

The pre-flight thermal check in `ExecuteStructured` checks `thermalCloudEscalationTime[taskID]` before proceeding. If `time.Since(escalationTime) > ThermalCloudCooldownMinutes`, the thermal entry is deleted and local execution is allowed. The speed-floor's `forceCloudFallback` is checked independently and is unaffected by thermal recovery.

## New File

### `internal/inference/thermal.go`

Contains:

- `ThermalState` type (string enum: `"nominal"`, `"fair"`, `"serious"`, `"critical"`)
- `ReadThermalState() ThermalState` — dispatches to platform-specific reader based on `runtime.GOOS`
- `readThermalStateDarwin() ThermalState` — parses `pmset -g therm`
- `readThermalStateWindows() ThermalState` — calls PowerShell WMI query
- `readThermalStateLinux() ThermalState` — reads sysfs thermal zones
- `CheckThermalPressure(taskID string, m *LocalModelManager) (proceed bool, escalateToCloud bool)` — implements the tiered cooldown/escalation logic

### `internal/inference/thermal_test.go`

Unit tests with mock command output for each platform's parser:

- Parse `pmset -g therm` output at various throttle levels
- Parse WMI temperature output and verify °C conversion
- Parse sysfs millidegree values
- Verify cooldown retry logic (mock `ReadThermalState` to return declining states)
- Verify cloud escalation and recovery timing

## Config Additions

In `internal/config/config.go`, add to `Config` struct:

```go
ThermalCooldownSeconds      int `json:"thermalCooldownSeconds"`      // default 30
ThermalCloudCooldownMinutes int `json:"thermalCloudCooldownMinutes"` // default 5
```

Defaults set in `defaultConfig()`. Loaded and applied in `LoadFromJSON()` and `LoadFromFile()`.

## LocalModelManager Additions

In `local_model.go`, add to `LocalModelManager` struct:

```go
thermalCloudEscalationTime map[string]time.Time // taskID → when cloud escalation was triggered
```

Initialize in `initMaps()`. Read/write protected by the existing `fallbackMutex`.

## Telemetry Events

| Event | Scope | Detail |
|---|---|---|
| `thermal_pressure_fair` | taskID, nodeID | "Thermal pressure elevated: CPU_Speed_Limit=85" |
| `thermal_pressure_pause` | taskID, nodeID | "Thermal pressure serious: pausing 30s before inference" |
| `thermal_pressure_fallback` | taskID, nodeID | "Thermal pressure critical: escalating to cloud for 5m cooldown" |
| `thermal_pressure_recovered` | taskID, nodeID | "Thermal pressure recovered after cooldown pause" |
| `thermal_read_unavailable` | system | "Thermal reading unavailable on this platform; relying on speed floor" |

## Graceful Degradation

If the platform thermal reading fails (command not found, WMI not populated, sysfs missing), the system:

1. Logs `thermal_read_unavailable` once (not per-call)
2. Returns `nominal` — assumes no thermal pressure
3. The existing speed-floor mechanism remains as the secondary safety net

This ensures the feature is additive — it never blocks inference on platforms where temperature data isn't available.

## Non-Goals

- **GPU-specific temperature** (e.g. `nvidia-smi`): out of scope for v1. The OS-level thermal state captures system-wide pressure including GPU contribution. GPU-specific readings can be added later as an optional enhancement.
- **User-configurable temperature thresholds**: the °C/percentage thresholds are hardcoded for v1. If users request tuning, expose them in config later.
- **Background polling**: intentionally excluded. The on-demand model keeps the implementation simple and avoids a persistent goroutine.
