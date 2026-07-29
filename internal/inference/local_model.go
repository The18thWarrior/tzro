package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"tzro/internal/config"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// thinkingContextKey is a private type for the thinking mode context key.
type thinkingContextKey struct{}

// ThinkingEnabledKey is a context key that callers set to opt-in to thinking
// mode for unconstrained inference calls. When present (with any non-nil value)
// and no GBNF schema is active, the local model will generate <think> reasoning
// tokens before producing output. Use context.WithValue(ctx, ThinkingEnabledKey, true).
var ThinkingEnabledKey = thinkingContextKey{}

// maxTokensContextKey is a private type for the generation cap context key.
type maxTokensContextKey struct{}

// MaxTokensKey is a context key that callers set to cap generation tokens per
// inference call (ADR-0043 Mechanism A). When present with an int value, the
// local model includes max_tokens in the completion request, preventing runaway
// generation. Use context.WithValue(ctx, MaxTokensKey, 2048).
var MaxTokensKey = maxTokensContextKey{}

// InferenceResult holds the model output along with token-level metrics from the server.
type InferenceResult struct {
	Content          string  `json:"content"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	DurationSeconds  float64 `json:"durationSeconds"`
	TokensPerSecond  float64 `json:"tokensPerSecond"`
}

type LocalModelManager struct {
	Publisher                  telemetry.EventPublisher
	ActivePort                 int    `json:"activePort"`
	ActivePID                  int    `json:"activePid"`
	Status                     string `json:"status"` // "Stopped" | "Starting" | "Active" | "Adopted"
	ManifestProgress           int    `json:"manifestProgress"`
	GGUFModelPath              string `json:"ggufModelPath"`
	Role                       string `json:"role"` // "router" | "worker" — identifies sidecar role for port/lock file naming
	checkpointFile             string
	isPreempted                bool
	cmd                        *exec.Cmd
	mutex                      sync.Mutex
	healthClient               *http.Client // Short timeout for /health, /slots control-plane calls
	inferenceClient            *http.Client // No fixed timeout — relies on ctx deadline for inference calls
	forceCloudFallback         map[string]bool
	consecutiveSpeedFail       map[string]int
	thermalCloudEscalationTime map[string]time.Time // taskID → when thermal cloud escalation was triggered
	fallbackMutex              sync.RWMutex
}

func (m *LocalModelManager) getPublisher() telemetry.EventPublisher {
	if m.Publisher != nil {
		return m.Publisher
	}
	return telemetry.Default
}

// resolveTzroPath delegates to config.ResolvePath — canonical TZRO_DIR resolution.
func resolveTzroPath(relPath string) string {
	return config.ResolvePath(relPath)
}

// GlobalWorkerModel is the primary sidecar — handles code generation, planning,
// complex reasoning, and long-form synthesis.
var GlobalWorkerModel = &LocalModelManager{
	Status:           "Stopped",
	ManifestProgress: 100,
	Role:             "worker",
	healthClient: &http.Client{
		Timeout: 3 * time.Second,
	},
	inferenceClient: &http.Client{
		// No fixed timeout — the caller's context.Context controls cancellation.
		// This prevents the 3-second timeout from killing inference calls that
		// legitimately take 10-600+ seconds for large planning outputs.
	},
	forceCloudFallback:         make(map[string]bool),
	consecutiveSpeedFail:       make(map[string]int),
	thermalCloudEscalationTime: make(map[string]time.Time),
}

// GlobalRouterModel is the lightweight routing sidecar — handles tool selection,
// Probe navigation, classification, and validator passes.
var GlobalRouterModel = &LocalModelManager{
	Status:           "Stopped",
	ManifestProgress: 100,
	Role:             "router",
	healthClient: &http.Client{
		Timeout: 3 * time.Second,
	},
	inferenceClient:            &http.Client{},
	forceCloudFallback:         make(map[string]bool),
	consecutiveSpeedFail:       make(map[string]int),
	thermalCloudEscalationTime: make(map[string]time.Time),
}

// GlobalLocalModel is a backward-compatibility alias for GlobalWorkerModel.
// Deprecated: Use GlobalWorkerModel or GlobalRouterModel directly.
var GlobalLocalModel = GlobalWorkerModel

// sidecarFilePrefix returns the filesystem prefix for this sidecar's lock/port files.
// Router: .llama-router  Worker: .llama-server (backward compatible)
func (m *LocalModelManager) sidecarFilePrefix() string {
	if m.Role == "router" {
		return ".llama-router"
	}
	return ".llama-server"
}

// Start launches the llama-server child process or adopts an existing running server socket
func (m *LocalModelManager) Start(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if flag.Lookup("test.v") != nil {
		return fmt.Errorf("local server execution disabled in test mode")
	}

	if m.Status == "Active" || m.Status == "Adopted" {
		return nil
	}

	cfg := config.Get()
	m.GGUFModelPath = cfg.GGUFModelPath
	if m.GGUFModelPath != "" && !filepath.IsAbs(m.GGUFModelPath) {
		m.GGUFModelPath = filepath.Join(config.GetModelsDir(), filepath.Base(m.GGUFModelPath))
	}
	m.Status = "Starting"

	// Acquire exclusive filesystem lock to prevent concurrent llama-server spawning
	// across processes (tzro-mcp and tzrod race on simultaneous IDE restart).
	lockPath := resolveTzroPath(filepath.Join(".tzro", m.sidecarFilePrefix()+".lock"))
	lockFile, lockErr := acquireFileLock(lockPath)
	if lockErr != nil {
		fmt.Fprintf(os.Stderr, "[Llama Sidecar] Warning: could not acquire startup lock: %v\n", lockErr)
	}
	defer releaseFileLock(lockFile)

	portFile := resolveTzroPath(filepath.Join(".tzro", m.sidecarFilePrefix()+".port"))

	// 1. Attempt Server Process Adoption across reloads.
	// After acquiring the flock, another process may have started the server
	// while we waited. The retry loop handles server boot time.
	if flag.Lookup("test.v") == nil {
		if data, err := os.ReadFile(portFile); err == nil {
			fields := strings.Split(strings.TrimSpace(string(data)), ":")
			if len(fields) == 2 {
				savedPort, _ := strconv.Atoi(fields[0])
				savedPID, _ := strconv.Atoi(fields[1])

				// Verify the process is still alive before retrying health probes
				processAlive := false
				if proc, err := os.FindProcess(savedPID); err == nil {
					if err := proc.Signal(syscall.Signal(0)); err == nil {
						processAlive = true
					}
				}

				if processAlive {
					// Health probe with retries — server may still be booting
					for attempt := range 6 {
						healthURL := fmt.Sprintf("http://localhost:%d/health", savedPort)
						req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
						if err != nil {
							break
						}
						resp, err := m.getHealthClient().Do(req)
						if err == nil && resp.StatusCode == http.StatusOK {
							resp.Body.Close()
							m.ActivePort = savedPort
							m.ActivePID = savedPID
							m.Status = "Adopted"
							fmt.Fprintf(os.Stderr, "[Llama Sidecar] Adopted existing running process on Port %d (PID %d)\n", savedPort, savedPID)
							return nil
						}
						if attempt < 5 {
							fmt.Fprintf(os.Stderr, "[Llama Sidecar] Port file found (port %d) but server not healthy yet, retrying (%d/5)...\n", savedPort, attempt+1)
							time.Sleep(500 * time.Millisecond)
						}
					}
				}
				// Process dead or health never passed — stale port file
				_ = os.Remove(portFile)
			}
		}
	}

	// 2. Allocate a random free port from the OS
	port, err := allocateRandomPort()
	if err != nil {
		m.Status = "Stopped"
		return fmt.Errorf("failed to allocate a free port for llama-server: %w", err)
	}
	m.ActivePort = port

	// 3. Thread scheduling: Pin threads strictly to Performance (P) cores count
	pCores := m.getPerformanceCoresCount()
	fmt.Fprintf(os.Stderr, "[Llama Sidecar] Thread scheduler count pinned to %d performance CPU cores.\n", pCores)

	// 4. GPU layer offloading: platform-aware detection
	gpuLayers := m.getGPULayerCount()
	fmt.Fprintf(os.Stderr, "[Llama Sidecar] GPU offload layers: %d\n", gpuLayers)

	// 5. KV cache quantization: mode-dependent (q4_0 cooperative, q8_0 local)
	kvCacheType := "q4_0"
	if cfg.ModelMode == "local" {
		kvCacheType = "q8_0"
	}
	fmt.Fprintf(os.Stderr, "[Llama Sidecar] KV cache quantization: %s (mode: %s)\n", kvCacheType, cfg.ModelMode)

	// 6. Slot save path for KV cache preemption save/restore
	slotSavePath := filepath.Join(config.GetModelsDir(), "kv-cache")
	_ = os.MkdirAll(slotSavePath, 0755)

	// 7. Spawn child process
	// Prefer the bundled llama-server in ~/.tzro/bin (installed by install.sh with
	// the pinned version) over whatever is in PATH. This ensures consistent behavior
	// between fresh installs and dev environments.
	llamaBinary := ""
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		bundledPath := filepath.Join(home, ".tzro", "bin", "llama-server")
		if _, statErr := os.Stat(bundledPath); statErr == nil {
			llamaBinary = bundledPath
		}
	}
	if llamaBinary == "" {
		pathBinary, lookErr := exec.LookPath("llama-server")
		if lookErr != nil {
			m.Status = "Stopped"
			return fmt.Errorf("llama-server binary not found in ~/.tzro/bin or PATH; please install llama.cpp server: %w", lookErr)
		}
		llamaBinary = pathBinary
	}

	// Pre-flight check: verify the GGUF model file exists on disk
	if _, err := os.Stat(m.GGUFModelPath); os.IsNotExist(err) {
		m.Status = "Stopped"
		return fmt.Errorf("GGUF model file not found at %s — download a model from the Settings panel", m.GGUFModelPath)
	}

	// 8. Resolve MTP draft model path — dynamically check catalog for CompanionMTP
	modelsDir := config.GetModelsDir()
	activeModelDir := filepath.Dir(m.GGUFModelPath)
	useMTP := false
	mtpDraftModelPath := ""

	// Look up the active model in the catalog by filename to find its MTP companion
	activeFilename := filepath.Base(m.GGUFModelPath)
	catalogEntry := FindModelByFilename(activeFilename)
	if catalogEntry != nil && catalogEntry.CompanionMTP != nil {
		entry := catalogEntry
		// Check candidate paths: configured modelsDir first, then the active model's directory
		candidatePaths := []string{
			filepath.Join(modelsDir, entry.CompanionMTP.Filename),
		}
		if activeModelDir != modelsDir {
			candidatePaths = append(candidatePaths, filepath.Join(activeModelDir, entry.CompanionMTP.Filename))
		}
		for _, candidatePath := range candidatePaths {
			if _, err := os.Stat(candidatePath); err == nil {
				useMTP = true
				mtpDraftModelPath = candidatePath
				fmt.Fprintf(os.Stderr, "[Llama Sidecar] MTP draft model found: %s\n", candidatePath)
				break
			}
		}
		if !useMTP {
			fmt.Fprintf(os.Stderr, "[Llama Sidecar] MTP draft model listed in catalog but not found in %s or %s\n", modelsDir, activeModelDir)
		}
	}

	if !useMTP {
		fmt.Fprintln(os.Stderr, "[Llama Sidecar] No MTP draft model available, falling back to ngram-simple speculative decoding")
	}

	// Optimized launch args (12 resolved decisions from sidecar optimization session)
	parallelSlots := getWorkerParallelSlots(getSystemMemoryGB())
	args := []string{
		"-m", m.GGUFModelPath,
		"--port", strconv.Itoa(m.ActivePort),
		"--threads", strconv.Itoa(pCores),
		"--parallel", strconv.Itoa(parallelSlots),
		"--jinja",
		"--n-gpu-layers", strconv.Itoa(gpuLayers), // Q1: platform-aware GPU offload
		"--ctx-size", strconv.Itoa(config.GetContextSize()), // Configurable context window (default 64K)
		"--cache-type-k", kvCacheType, // Q3: mode-dependent KV cache quantization
		"--cache-type-v", kvCacheType, // Q3: mode-dependent KV cache quantization
		"-fa", "auto", // Q4: flash attention (auto-detect)
		"--cache-reuse", strconv.Itoa(config.GetCacheReuseTokens()), // ADR-0056: append-only probe context; 0 = unlimited prefix matching for full KV cache reuse
		"--n-predict", "16384", // Q9: max tokens per generation
		"--slot-save-path", slotSavePath, // Q8: enable /slots save/restore API for preemption
		"--cache-ram", "2048", // Limit maximum prompt cache host memory to 2GB to resolve memory pressure
		"-b", "2048", // Prompt processing batch size (up from default 512)
		"-ub", "512", // Micro-batch size for prompt processing
	}

	if useMTP {
		// MTP speculative decoding: 4-layer drafter shares KV cache with target model
		args = append(args,
			"--spec-type", "draft-mtp",
			"--spec-draft-model", mtpDraftModelPath,
			"--spec-draft-n-max", "4", // MTP draft heads predict 4 tokens ahead
		)
	} else {
		// Fallback: n-gram speculative decoding (no draft model needed)
		args = append(args,
			"--spec-type", "ngram-simple",
			"--spec-draft-n-max", "48", // Raised from default 16 for JSON verbatim matches
		)
	}

	// 9. Multimodal projector for vision support (PDF OCR, image analysis)
	// Only load mmproj if it's verified compatible with the active model.
	// The auto-detect glob in GetMMProjModelPath picks up any *mmproj*.gguf file,
	// which crashes llama-server when embedding dimensions mismatch (e.g., loading
	// a Qwen mmproj with an LFM model). We resolve this by checking the catalog:
	// - Catalog model with CompanionMMProj: use the catalog's mmproj filename
	// - Non-catalog model (custom download): only use explicitly configured mmproj
	mmProjPath := ""
	if catalogEntry != nil && catalogEntry.CompanionMMProj != nil {
		// Model is in catalog with a known-compatible mmproj — check if it exists on disk
		candidateMmproj := []string{
			filepath.Join(modelsDir, catalogEntry.CompanionMMProj.Filename),
		}
		if activeModelDir != modelsDir {
			candidateMmproj = append(candidateMmproj, filepath.Join(activeModelDir, catalogEntry.CompanionMMProj.Filename))
		}
		for _, p := range candidateMmproj {
			if _, err := os.Stat(p); err == nil {
				mmProjPath = p
				break
			}
		}
		if mmProjPath == "" {
			fmt.Fprintf(os.Stderr, "[Llama Sidecar] Catalog mmproj '%s' not found on disk, vision disabled\n", catalogEntry.CompanionMMProj.Filename)
		}
	} else {
		// Model has no catalog mmproj — only use explicitly configured mmproj (never auto-detect)
		configMutex := config.GetExplicitMMProjPath()
		if configMutex != "" {
			if _, err := os.Stat(configMutex); err == nil {
				mmProjPath = configMutex
			}
		}
		if mmProjPath == "" {
			if catalogEntry != nil {
				fmt.Fprintf(os.Stderr, "[Llama Sidecar] No vision projector configured for %s, vision disabled\n", catalogEntry.DisplayName)
			} else {
				fmt.Fprintf(os.Stderr, "[Llama Sidecar] Non-catalog model detected, skipping auto-detected mmproj to avoid architecture mismatch\n")
			}
		}
	}
	if mmProjPath != "" {
		args = append(args, "--mmproj", mmProjPath)
		fmt.Fprintf(os.Stderr, "[Llama Sidecar] Vision projector loaded: %s\n", mmProjPath)
	}

	// Router sidecar overrides: memory-gated context, no speculative decoding, no vision
	if m.Role == "router" {
		// Memory-gated router context: 64K on ≥16GB (eliminates probe HTTP 400
		// from analyze prompts exceeding 16K), 16K on <16GB to conserve memory.
		// Router model (1B) natively supports 131K — 64K is well within bounds.
		routerCtxSize := getRouterContextSize(getSystemMemoryGB())
		for i, a := range args {
			if a == "--ctx-size" && i+1 < len(args) {
				args[i+1] = strconv.Itoa(routerCtxSize)
			}
			if a == "--n-predict" && i+1 < len(args) {
				args[i+1] = "4096" // Router outputs: compaction can produce longer reasoning summaries
			}
			// cache-reuse is already set from config via config.GetCacheReuseTokens()
			// (default 0 = unlimited), no override needed for router — same value
			// works for both sidecars since append-only context benefits both.
		}
		// Remove vision projector and slot-save-path for router; re-enable spec decoding with ngram-simple.
		var cleanArgs []string
		skip := false
		for _, a := range args {
			if a == "--slot-save-path" || a == "--mmproj" {
				skip = true
				continue
			}
			if skip {
				skip = false
				continue
			}
			cleanArgs = append(cleanArgs, a)
		}
		// Override router speculative decoding to lightweight ngram-simple
		// (rather than stripping it entirely). Provides ~20-30% faster
		// generation for structured/repetitive outputs (ACTION tags, JSON).
		for i, a := range cleanArgs {
			if a == "--spec-type" && i+1 < len(cleanArgs) {
				cleanArgs[i+1] = "ngram-simple"
			}
			if a == "--spec-draft-n-max" && i+1 < len(cleanArgs) {
				cleanArgs[i+1] = "16"
			}
			if a == "--spec-draft-model" {
				// Remove draft model arg (ngram-simple doesn't need it)
				cleanArgs[i] = ""
				if i+1 < len(cleanArgs) {
					cleanArgs[i+1] = ""
				}
			}
		}
		// Filter empty strings from draft model removal
		var filteredArgs []string
		for _, a := range cleanArgs {
			if a != "" {
				filteredArgs = append(filteredArgs, a)
			}
		}
		args = filteredArgs
		fmt.Fprintf(os.Stderr, "[Llama Router] Starting with ctx=16384, cache-reuse=%d (routing mode, ngram-simple speculative decoding)\n", config.GetCacheReuseTokens())
	}

	m.cmd = exec.CommandContext(context.Background(), llamaBinary, args...)

	// Create logs folder
	logsDir := resolveTzroPath(filepath.Join(".tzro", "logs"))
	_ = os.MkdirAll(logsDir, 0755)
	logFile, err := os.Create(filepath.Join(logsDir, "llama-server.log"))
	if err == nil {
		m.cmd.Stdout = logFile
		m.cmd.Stderr = logFile
	}

	if err := m.cmd.Start(); err != nil {
		m.Status = "Stopped"
		return fmt.Errorf("failed to start llama-server process: %w", err)
	}

	m.ActivePID = m.cmd.Process.Pid
	m.Status = "Active"

	// Write Port/PID file
	_ = os.WriteFile(portFile, []byte(fmt.Sprintf("%d:%d", m.ActivePort, m.ActivePID)), 0644)
	fmt.Fprintf(os.Stderr, "[Llama Sidecar] Launched llama-server child process on Port %d (PID %d)\n", m.ActivePort, m.ActivePID)
	return nil
}

// Stop terminates the running llama-server process gracefully
func (m *LocalModelManager) Stop() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	portFile := resolveTzroPath(filepath.Join(".tzro", m.sidecarFilePrefix()+".port"))
	_ = os.Remove(portFile)

	if m.Status == "Stopped" {
		return nil
	}

	m.Status = "Stopped"
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Signal(os.Interrupt)
		time.Sleep(500 * time.Millisecond)
		_ = m.cmd.Process.Kill()
	} else if m.ActivePID > 0 {
		// Kill adopted process
		proc, err := os.FindProcess(m.ActivePID)
		if err == nil {
			_ = proc.Kill()
		}
	}

	fmt.Fprintln(os.Stderr, "[Llama Sidecar] Terminated persistent local server process.")
	return nil
}

// GetStatusInfo returns thread-safe status data for the manager
func (m *LocalModelManager) GetStatusInfo() (string, int, int, int, string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.Status, m.ActivePort, m.ActivePID, m.ManifestProgress, m.GGUFModelPath
}

// IsVisionAvailable returns true if the multimodal projector is loaded
// and the backend is active — meaning image content parts will be processed
// by the local model (e.g., for PDF OCR via vision).
func (m *LocalModelManager) IsVisionAvailable() bool {
	m.mutex.Lock()
	status := m.Status
	m.mutex.Unlock()
	return (status == "Active" || status == "Adopted") &&
		config.GetMMProjModelPath() != ""
}

func (m *LocalModelManager) getHealthClient() *http.Client {
	if m.healthClient != nil {
		return m.healthClient
	}
	return http.DefaultClient
}

func (m *LocalModelManager) getInferenceClient() *http.Client {
	if m.inferenceClient != nil {
		return m.inferenceClient
	}
	return http.DefaultClient
}

// SidecarHealth represents the result of an HTTP health probe against the sidecar.
type SidecarHealth int

const (
	// SidecarHealthReady — /health returned 200, server is up and has available slots.
	SidecarHealthReady SidecarHealth = iota
	// SidecarHealthBusy — /health returned 503 (no slot available), server is alive
	// but all inference slots are occupied by another request.
	SidecarHealthBusy
	// SidecarHealthDead — connection refused or other fatal error. Process is not running.
	SidecarHealthDead
)

// ProbeHealth checks the actual HTTP /health endpoint of the sidecar process,
// independent of the cached m.Status field. This catches the case where m.Status
// was pessimistically set to "Stopped" by a transient inference error (timeout,
// context cancellation) while the sidecar process is still running and healthy.
//
// The llama.cpp /health endpoint returns:
//   - 200 OK when the server is healthy and has available slots
//   - 503 Service Unavailable when the server is alive but all slots are occupied
//   - Connection refused when the process is not running
func (m *LocalModelManager) ProbeHealth(ctx context.Context) SidecarHealth {
	m.mutex.Lock()
	port := m.ActivePort
	m.mutex.Unlock()

	if port == 0 {
		return SidecarHealthDead
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	healthURL := fmt.Sprintf("http://localhost:%d/health", port)
	req, err := http.NewRequestWithContext(probeCtx, "GET", healthURL, nil)
	if err != nil {
		return SidecarHealthDead
	}

	resp, err := m.getHealthClient().Do(req)
	if err != nil {
		return SidecarHealthDead
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return SidecarHealthReady
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		// 503 means the server is alive but all slots are occupied
		return SidecarHealthBusy
	}
	return SidecarHealthDead
}

// EraseSlot clears prompt context for a specific slot to prevent memory leakage
func (m *LocalModelManager) EraseSlot(ctx context.Context, slotID int) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.Status != "Active" && m.Status != "Adopted" {
		return nil
	}

	fmt.Fprintf(os.Stderr, "[Llama Sidecar GC] Erasing slot %d context...\n", slotID)
	eraseURL := fmt.Sprintf("http://localhost:%d/slots/%d?action=erase", m.ActivePort, slotID)
	req, err := http.NewRequestWithContext(ctx, "POST", eraseURL, nil)
	if err != nil {
		return err
	}

	resp, err := m.getHealthClient().Do(req)
	if err != nil {
		return fmt.Errorf("active slot erasure failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("erase API returned status: %d", resp.StatusCode)
	}
	return nil
}

// TriggerGC (Tier 1 Active Slot Erasure) clears slot token context post-task boundary
func (m *LocalModelManager) TriggerGC(ctx context.Context) error {
	_ = m.EraseSlot(ctx, 0)
	_ = m.EraseSlot(ctx, 1)
	return nil
}

func (m *LocalModelManager) getProcessRSS(pid int) (int64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	clean := strings.TrimSpace(string(out))
	kb, err := strconv.ParseInt(clean, 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

// CheckAndTriggerTier2GC evaluates Resident Set Size (RSS) memory after a task completes,
// and gracefully recycles the local inference sidecar if memory limits are exceeded (e.g., 12GB).
func (m *LocalModelManager) CheckAndTriggerTier2GC(ctx context.Context) {
	m.mutex.Lock()
	pid := m.ActivePID
	status := m.Status
	m.mutex.Unlock()

	if (status != "Active" && status != "Adopted") || pid <= 0 {
		return
	}

	if rss, err := m.getProcessRSS(pid); err == nil {
		// 8GB threshold limit = 8 * 1024 * 1024 * 1024 bytes (adjusted for E4B model + 2GB cache)
		const threshold = 8 * 1024 * 1024 * 1024
		fmt.Fprintf(os.Stderr, "[Llama Sidecar GC] Current sidecar RSS memory usage: %dMB (Threshold: 8192MB)\n", rss/(1024*1024))

		if rss > threshold {
			fmt.Fprintln(os.Stderr, "[Llama Sidecar GC] RSS threshold exceeded. Triggering Tier 2 Graceful Process Recycling...")

			m.getPublisher().PublishEvent("sidecar_recycling", "system", strconv.Itoa(pid), fmt.Sprintf("RSS memory (%dMB) exceeded threshold; recycling sidecar process", rss/(1024*1024)))

			// Stop the server sidecar gracefully
			_ = m.Stop()

			// Start a fresh one in the background asynchronously
			go func() {
				time.Sleep(1 * time.Second)
				bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := m.Start(bgCtx); err != nil {
					fmt.Fprintf(os.Stderr, "[Llama Sidecar GC Error] Asynchronous process restart failed: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "[Llama Sidecar GC] Successfully completed Tier 2 sidecar process recycling!")
				}
			}()
		}
	} else {
		fmt.Fprintf(os.Stderr, "[Llama Sidecar GC Warning] Failed to check RSS memory usage: %v\n", err)
	}
}

// PreemptForChat (KV Cache Preemption) saves active background task context to slot_0.bin,
// clears the slot, and sets a restoration handler to guarantee sub-second interactive chat.
func (m *LocalModelManager) PreemptForChat(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.Status != "Active" && m.Status != "Adopted" {
		return nil
	}

	m.isPreempted = true

	fmt.Fprintln(os.Stderr, "[Llama Sidecar Preemption] POSTing save slot attention state...")
	m.getPublisher().PublishEvent("preemption_save", "system", "slot_0", "Saving background task slot attention buffers")

	saveURL := fmt.Sprintf("http://localhost:%d/slots/0/save", m.ActivePort)
	saveBody := bytes.NewReader([]byte(`{"filename": "slot_0.bin"}`))
	req, err := http.NewRequestWithContext(ctx, "POST", saveURL, saveBody)
	if err != nil {
		return fmt.Errorf("failed to create slot save request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.getHealthClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to call slot save endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slot save endpoint returned status: %s", resp.Status)
	}

	return nil
}

// RestoreAfterChat reloads saved task KV cache back into the slot
func (m *LocalModelManager) RestoreAfterChat(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.isPreempted {
		return nil
	}

	fmt.Fprintln(os.Stderr, "[Llama Sidecar Preemption] Restoring saved slot context state...")
	m.getPublisher().PublishEvent("preemption_restore", "system", "slot_0", "Restoring background task slot attention buffers")

	restoreURL := fmt.Sprintf("http://localhost:%d/slots/0/restore", m.ActivePort)
	restoreBody := bytes.NewReader([]byte(`{"filename": "slot_0.bin"}`))
	req, err := http.NewRequestWithContext(ctx, "POST", restoreURL, restoreBody)
	if err != nil {
		return fmt.Errorf("failed to create slot restore request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.getHealthClient().Do(req)
	if err != nil {
		return fmt.Errorf("failed to call slot restore endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slot restore endpoint returned status: %s", resp.Status)
	}

	m.isPreempted = false
	return nil
}

// getPerformanceCoresCount returns count of Performance P-Cores CGO-free on Mac, or half physical cores
func (m *LocalModelManager) getPerformanceCoresCount() int {
	cfg := config.Get()

	// User override takes absolute precedence
	if cfg.ThreadCount != nil && *cfg.ThreadCount > 0 {
		return *cfg.ThreadCount
	}

	logicalCPUs := runtime.NumCPU()

	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		// Apple Silicon: query actual P-core count, then reserve headroom
		// to reduce sustained thermal pressure on the shared CPU/GPU die.
		out, err := exec.Command("sysctl", "-n", "hw.perflevel0.logicalcpu").Output()
		if err == nil {
			if count, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && count > 0 {
				// Reserve 2 P-cores for OS + background tasks.
				// On small chips (M1/M2 base with 4 P-cores), reserve only 1.
				reserve := 2
				if count <= 4 {
					reserve = 1
				}
				threads := count - reserve
				if threads < 2 {
					threads = 2
				}
				return threads
			}
		}
	}

	if runtime.GOOS == "darwin" {
		// Intel Mac: all cores are symmetric, use physical core count (not HT logical).
		out, err := exec.Command("sysctl", "-n", "hw.physicalcpu").Output()
		if err == nil {
			if count, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && count > 0 {
				return count
			}
		}
	}

	// Windows/Linux: physical core estimate (logical / 2 for hyperthreading)
	pCores := logicalCPUs / 2
	if pCores <= 0 {
		return 1
	}

	// When GPU offload is active, CPU threads are mainly for prompt processing.
	// Cap at min(pCores, 8) to avoid thermal waste on the CPU side.
	if cfg.GPULayers != nil && *cfg.GPULayers != 0 {
		if pCores > 8 {
			pCores = 8
		}
	}

	return pCores
}

// getGPULayerCount returns the number of model layers to offload to GPU.
// Platform-aware defaults with user override via config.GPULayers:
//   - Apple Silicon (darwin/arm64): -1 (all layers on Metal GPU via unified memory)
//   - Intel Mac (darwin/amd64): 0 (CPU-only; users with AMD GPUs can override)
//   - Windows/Linux: 0 (CPU-only; users with NVIDIA/AMD GPUs can override)
func (m *LocalModelManager) getGPULayerCount() int {
	cfg := config.Get()

	// User override takes absolute precedence
	if cfg.GPULayers != nil {
		return *cfg.GPULayers
	}

	// Platform auto-detection
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return -1 // Full Metal offload on Apple Silicon — most power-efficient path
	}

	// Intel Mac, Windows, Linux: CPU-only by default.
	// Users with discrete GPUs can set "gpuLayers": -1 in config.
	return 0
}

// getSystemMemoryGB returns the total system memory in gigabytes.
// Uses sysctl on macOS, /proc/meminfo on Linux, runtime fallback elsewhere.
func getSystemMemoryGB() int {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			if bytes, parseErr := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); parseErr == nil {
				return int(bytes / (1024 * 1024 * 1024))
			}
		}
	}

	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if kb, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
							return int(kb / (1024 * 1024))
						}
					}
				}
			}
		}
	}

	// Fallback: assume 16GB (conservative default)
	return 16
}

// getWorkerParallelSlots returns the number of parallel inference slots
// for the worker sidecar, based on system memory.
// ≥24GB → 2 slots (enables DAG-level concurrency)
// <24GB → 1 slot (baseline, avoids memory pressure)
func getWorkerParallelSlots(memoryGB int) int {
	if memoryGB >= 24 {
		return 2
	}
	return 1
}

// getRouterContextSize returns the context window size for the router sidecar,
// based on system memory. The 1B router model supports 131K context natively.
// ≥16GB → 65536 (matches worker context, eliminates probe HTTP 400 from
//         analyze system prompts exceeding the 16K limit with CSV tool output)
// <16GB → 16384 (preserve current behavior, lower memory footprint)
func getRouterContextSize(memoryGB int) int {
	if memoryGB >= 16 {
		return 65536
	}
	return 16384
}

// allocateRandomPort asks the OS for a random free port by briefly binding to :0,
// then immediately releasing it for llama-server to use.
func allocateRandomPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port, nil
}

// acquireFileLock acquires an exclusive flock on the given path.
// Returns the open file (caller must defer releaseFileLock) or nil on error.
func acquireFileLock(path string) (*os.File, error) {
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}
	return f, nil
}

// releaseFileLock releases the flock and closes the file. Nil-safe.
func releaseFileLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// TokenizeContent calls the sidecar's /tokenize endpoint to get an exact token
// count for the given content. Returns the number of tokens. Used by the probe
// pre-flight check to detect context overflow before sending inference requests.
// The llama.cpp server natively exposes /tokenize.
func (m *LocalModelManager) TokenizeContent(content string) (int, error) {
	if m.Status != "Active" || m.ActivePort == 0 {
		// Fallback to heuristic: ~4 chars per token
		return len(content) / 4, nil
	}

	reqBody := map[string]interface{}{
		"content": content,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("http://localhost:%d/tokenize", m.ActivePort)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return len(content) / 4, nil // Fallback
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.getInferenceClient().Do(req)
	if err != nil {
		return len(content) / 4, nil // Fallback
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return len(content) / 4, nil // Fallback
	}

	var result struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return len(content) / 4, nil // Fallback
	}

	return len(result.Tokens), nil
}

// GetActiveContextSize returns the n_ctx the sidecar was launched with.
// Used by the probe pre-flight check to compare against tokenized prompt size.
func (m *LocalModelManager) GetActiveContextSize() int {
	if m.Role == "router" {
		return getRouterContextSize(getSystemMemoryGB())
	}
	return config.GetContextSize()
}

// CallLocalModel handles the local structured JSON inference call.
// Phase-conditional thinking: enables <think> reasoning when no GBNF schema is
// active (free-form passes), disables it during grammar-constrained output.
// Returns an InferenceResult with content and accurate token-level metrics from the server's usage object.
func (m *LocalModelManager) CallLocalModel(ctx context.Context, messages []InferenceMessage, gbnfSchema string) (*InferenceResult, error) {
	// Build the completion request with optimized sampling parameters
	type CompletionRequest struct {
		Model              string                   `json:"model"`
		Messages           []map[string]interface{} `json:"messages"`
		Temperature        float64                  `json:"temperature"`
		MinP               float64                  `json:"min_p"`
		MaxTokens          *int                     `json:"max_tokens,omitempty"`
		ResponseFormat     map[string]interface{}   `json:"response_format,omitempty"`
		ChatTemplateKwargs map[string]interface{}   `json:"chat_template_kwargs,omitempty"`
	}

	// Phase-conditional thinking: enabled ONLY when the caller explicitly opts in
	// via context key AND no GBNF grammar is active. This prevents thinking tokens
	// from consuming the entire generation budget in large unconstrained calls
	// (e.g., the planner) where gbnfSchema == "" but thinking is not beneficial.
	enableThinking := gbnfSchema == "" && ctx.Value(ThinkingEnabledKey) != nil
	templateKwargs := map[string]interface{}{
		"enable_thinking": enableThinking,
	}
	if enableThinking {
		budget := config.Get().ThinkingBudget
		if budget <= 0 {
			budget = 750 // default: cap reasoning tokens to prevent throughput collapse
		}
		templateKwargs["thinking_budget"] = budget
	}

	reqBody := CompletionRequest{
		Model:              "Agents-A1-4B",
		Messages:           MessagesToMaps(messages),
		Temperature:        1.0, // Q7: required for min_p to function; GBNF constrains output safety
		MinP:               0.1, // Q7: dynamic token pruning — prunes tokens <10% of top token probability
		ChatTemplateKwargs: templateKwargs,
	}

	// ADR-0043 Mechanism A: Generation cap via context key (default to 2048 to prevent runaway loops)
	maxTok := 2048
	if overrideTok, ok := ctx.Value(MaxTokensKey).(int); ok && overrideTok > 0 {
		maxTok = overrideTok
	}
	reqBody.MaxTokens = &maxTok

	if gbnfSchema != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(gbnfSchema), &schemaObj) == nil {
			reqBody.ResponseFormat = map[string]interface{}{
				"type":   "json_object",
				"schema": schemaObj,
			}
		}
	}

	// Standard OpenAI compatible request dispatching
	bodyBytes, _ := json.Marshal(reqBody)
	mcpURL := fmt.Sprintf("http://localhost:%d/v1/chat/completions", m.ActivePort)

	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer local-dummy-key")

	// Q6: Per-call TriggerGC removed — --cache-reuse 256 handles prefix matching,
	// and GBNF constraint prevents context pollution between calls.
	// Post-task GC remains in the observer's idle/boundary cleanup.

	// Performance tracking metrics
	startTime := time.Now()

	// Q10: Use inferenceClient (no fixed timeout) instead of healthClient (3s timeout)
	resp, err := m.getInferenceClient().Do(req)
	if err != nil {
		// Don't blindly set Status to "Stopped" — the sidecar process may still
		// be alive but transiently unreachable (busy slot, connection reset, etc.).
		// Probe health with a fresh context before deciding the final status.
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		probeResult := m.ProbeHealth(probeCtx)
		probeCancel()
		if probeResult == SidecarHealthDead {
			m.mutex.Lock()
			m.Status = "Stopped"
			m.mutex.Unlock()
		} else {
			fmt.Fprintf(os.Stderr, "[Inference] HTTP request failed but sidecar health probe returned %v. Preserving status.\n", probeResult)
		}
		return nil, fmt.Errorf("local model HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "[Llama Sidecar] HTTP %d response body: %s\n", resp.StatusCode, string(errBody))
		return nil, fmt.Errorf("local model HTTP server returned status %s", resp.Status)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode local model HTTP response JSON: %w", err)
	}

	// Q12: Parse accurate token counts from the server's usage object
	duration := time.Since(startTime).Seconds()
	promptTokens := 0
	completionTokens := 0
	if usage, ok := result["usage"].(map[string]interface{}); ok {
		if pt, ok := usage["prompt_tokens"].(float64); ok {
			promptTokens = int(pt)
		}
		if ct, ok := usage["completion_tokens"].(float64); ok {
			completionTokens = int(ct)
		}
	}

	speed := 0.0
	if duration > 0 && completionTokens > 0 {
		speed = float64(completionTokens) / duration
	}

	// Parse completions fields
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					// Safety: strip residual <think>...</think> tags if the serving
					// backend didn't fully consume them (belt-and-suspenders).
					content = StripThinkTags(content)
					fmt.Fprintf(os.Stderr, "[Llama Sidecar Metrics] Prompt tokens: %d, Generated %d tokens in %.2fs (Speed: %.1f t/s)\n", promptTokens, completionTokens, duration, speed)
					RecordGlobalMetrics(promptTokens, completionTokens, duration)
					if tracker, ok := GetTokenTracker(ctx); ok {
						tracker.Record(false, promptTokens, completionTokens, duration, speed)
					}
					res := &InferenceResult{
						Content:          content,
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						DurationSeconds:  duration,
						TokensPerSecond:  speed,
					}
					// Publish completion event
					m.getPublisher().PublishStream(stream.StreamChunk{
						Source:  "classifier",
						Type:    "done",
						Content: res.Content,
						Usage: stream.UsageInfo{
							PromptTokens:     res.PromptTokens,
							CompletionTokens: res.CompletionTokens,
						},
					})
					return res, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("invalid or empty response choice returned from local model")
}

// StripThinkTags removes <think>...</think> and <thinking>...</thinking>
// blocks from model output. When thinking mode is enabled, the serving backend
// should strip these in the content field, but some backends (especially
// llama.cpp with certain chat templates) may leak them through. Some models
// also emit <thinking> (Claude-style) instead of <think> — both must be
// stripped to prevent interference with downstream tag extraction (e.g.,
// <ACTION>, <SYNTHESIZE_READY> in probe Thought Chains).
// Exported for use by the executor package (probe.go).
func StripThinkTags(content string) string {
	// Fast path: no think tags present (catches both <think> and <thinking>)
	if !strings.Contains(content, "<think>") && !strings.Contains(content, "<thinking>") {
		return content
	}
	// Strip both variants — order matters: <thinking> first (longer tag),
	// otherwise <think> would match the prefix of <thinking>.
	content = stripTagPair(content, "<thinking>", "</thinking>")
	content = stripTagPair(content, "<think>", "</think>")
	return strings.TrimSpace(content)
}

// stripTagPair removes all occurrences of open...close tag pairs from content.
// If an unclosed open tag is found, strips from the open tag to the end.
func stripTagPair(content, openTag, closeTag string) string {
	for {
		start := strings.Index(content, openTag)
		if start == -1 {
			break
		}
		end := strings.Index(content[start:], closeTag)
		if end == -1 {
			// Unclosed tag — strip from open tag to end
			content = content[:start]
			break
		}
		content = content[:start] + content[start+end+len(closeTag):]
	}
	return content
}

type StreamMeta struct {
	StreamID string
	Source   string
	TaskID   string
	NodeID   string
}

func (m *LocalModelManager) CallLocalModelStream(ctx context.Context, messages []InferenceMessage, gbnfSchema string, meta StreamMeta) (*InferenceResult, error) {
	if meta.Source == "chat" {
		_ = m.PreemptForChat(ctx)
		defer func() {
			_ = m.RestoreAfterChat(ctx)
		}()
	}

	type StreamOptionsStruct struct {
		IncludeUsage bool `json:"include_usage"`
	}

	// Build the completion request with optimized sampling parameters
	type CompletionRequest struct {
		Model              string                   `json:"model"`
		Messages           []map[string]interface{} `json:"messages"`
		Temperature        float64                  `json:"temperature"`
		MinP               float64                  `json:"min_p"`
		MaxTokens          *int                     `json:"max_tokens,omitempty"`
		Stream             bool                     `json:"stream"`
		StreamOptions      *StreamOptionsStruct     `json:"stream_options,omitempty"`
		ResponseFormat     map[string]interface{}   `json:"response_format,omitempty"`
		ChatTemplateKwargs map[string]interface{}   `json:"chat_template_kwargs,omitempty"`
	}

	// Phase-conditional thinking (same logic as CallLocalModel)
	enableThinking := gbnfSchema == "" && ctx.Value(ThinkingEnabledKey) != nil
	templateKwargs := map[string]interface{}{
		"enable_thinking": enableThinking,
	}
	if enableThinking {
		budget := config.Get().ThinkingBudget
		if budget <= 0 {
			budget = 750
		}
		templateKwargs["thinking_budget"] = budget
	}

	reqBody := CompletionRequest{
		Model:       "Agents-A1-4B",
		Messages:    MessagesToMaps(messages),
		Temperature: 1.0,
		MinP:        0.1,
		Stream:      true,
		StreamOptions: &StreamOptionsStruct{
			IncludeUsage: true,
		},
		ChatTemplateKwargs: templateKwargs,
	}

	// ADR-0043 Mechanism A: Generation cap via context key (default to 2048 to prevent runaway loops)
	maxTok := 2048
	if overrideTok, ok := ctx.Value(MaxTokensKey).(int); ok && overrideTok > 0 {
		maxTok = overrideTok
	}
	reqBody.MaxTokens = &maxTok

	if gbnfSchema != "" {
		var schemaObj map[string]interface{}
		if json.Unmarshal([]byte(gbnfSchema), &schemaObj) == nil {
			reqBody.ResponseFormat = map[string]interface{}{
				"type":   "json_object",
				"schema": schemaObj,
			}
		}
	}

	bodyBytes, _ := json.Marshal(reqBody)
	mcpURL := fmt.Sprintf("http://localhost:%d/v1/chat/completions", m.ActivePort)

	req, err := http.NewRequestWithContext(ctx, "POST", mcpURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer local-dummy-key")

	startTime := time.Now()
	resp, err := m.getInferenceClient().Do(req)
	if err != nil {
		// Don't blindly set Status to "Stopped" — the sidecar process may still
		// be alive but transiently unreachable (busy slot, connection reset, etc.).
		// Probe health with a fresh context before deciding the final status.
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		probeResult := m.ProbeHealth(probeCtx)
		probeCancel()
		if probeResult == SidecarHealthDead {
			m.mutex.Lock()
			m.Status = "Stopped"
			m.mutex.Unlock()
		} else {
			fmt.Fprintf(os.Stderr, "[Inference] Stream HTTP request failed but sidecar health probe returned %v. Preserving status.\n", probeResult)
		}
		return nil, fmt.Errorf("local model stream HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local model stream HTTP server returned status %s", resp.Status)
	}

	reader := bufio.NewReader(resp.Body)
	var accumulatedContent strings.Builder
	promptTokens := 0
	completionTokens := 0

	// ADR-0060: Extract GenerationGuard from context (opt-in)
	guard := GetGenerationGuard(ctx)
	generationAborted := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read stream line: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")
		if dataStr == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			contentDelta := chunk.Choices[0].Delta.Content
			if contentDelta != "" {
				accumulatedContent.WriteString(contentDelta)
				m.getPublisher().PublishStream(stream.StreamChunk{
					StreamID: meta.StreamID,
					Source:   meta.Source,
					TaskID:   meta.TaskID,
					NodeID:   meta.NodeID,
					Type:     "token",
					Content:  contentDelta,
				})

				// ADR-0060: Check GenerationGuard on newlines
				if guard != nil && strings.Contains(contentDelta, "\n") {
					if guard.OnChunk(accumulatedContent.String()) == GuardAbort {
						fmt.Fprintf(os.Stderr, "[GenerationGuard] Degenerate output detected — aborting generation (accumulated %d chars)\n",
							accumulatedContent.Len())
						accumulatedContent.WriteString(GenerationAbortedMarker)
						generationAborted = true
						break
					}
				}
			}
		}

		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			completionTokens = chunk.Usage.CompletionTokens
		}
	}

	// If generation was aborted, close the response body to cancel the stream
	if generationAborted {
		resp.Body.Close()
	}

	duration := time.Since(startTime).Seconds()
	speed := 0.0
	if duration > 0 && completionTokens > 0 {
		speed = float64(completionTokens) / duration
	}

	resContent := accumulatedContent.String()

	m.getPublisher().PublishStream(stream.StreamChunk{
		StreamID: meta.StreamID,
		Source:   meta.Source,
		TaskID:   meta.TaskID,
		NodeID:   meta.NodeID,
		Type:     "done",
		Content:  resContent,
		Usage: stream.UsageInfo{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
		},
	})

	fmt.Fprintf(os.Stderr, "[Llama Sidecar Stream Metrics] Prompt tokens: %d, Generated %d tokens in %.2fs (Speed: %.1f t/s)\n", promptTokens, completionTokens, duration, speed)
	RecordGlobalMetrics(promptTokens, completionTokens, duration)

	if tracker, ok := GetTokenTracker(ctx); ok {
		tracker.Record(false, promptTokens, completionTokens, duration, speed)
	}

	return &InferenceResult{
		Content:          resContent,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		DurationSeconds:  duration,
		TokensPerSecond:  speed,
	}, nil
}
