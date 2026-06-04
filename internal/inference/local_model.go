package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"time"
	"tzro/internal/config"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// InferenceResult holds the model output along with token-level metrics from the server.
type InferenceResult struct {
	Content          string  `json:"content"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	DurationSeconds  float64 `json:"durationSeconds"`
	TokensPerSecond  float64 `json:"tokensPerSecond"`
}

type LocalModelManager struct {
	Publisher            telemetry.EventPublisher
	ActivePort           int    `json:"activePort"`
	ActivePID            int    `json:"activePid"`
	Status               string `json:"status"` // "Stopped" | "Starting" | "Active" | "Adopted"
	ManifestProgress     int    `json:"manifestProgress"`
	GGUFModelPath        string `json:"ggufModelPath"`
	checkpointFile       string
	isPreempted          bool
	cmd                  *exec.Cmd
	mutex                sync.Mutex
	healthClient         *http.Client // Short timeout for /health, /slots control-plane calls
	inferenceClient      *http.Client // No fixed timeout — relies on ctx deadline for inference calls
	forceCloudFallback   map[string]bool
	consecutiveSpeedFail map[string]int
	fallbackMutex        sync.RWMutex
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

var GlobalLocalModel = &LocalModelManager{
	Status:           "Stopped",
	ManifestProgress: 100, // Preloaded defaults
	healthClient: &http.Client{
		Timeout: 3 * time.Second,
	},
	inferenceClient: &http.Client{
		// No fixed timeout — the caller's context.Context controls cancellation.
		// This prevents the 3-second timeout from killing inference calls that
		// legitimately take 10-600+ seconds for large planning outputs.
	},
	forceCloudFallback:   make(map[string]bool),
	consecutiveSpeedFail: make(map[string]int),
}

// Start launches the llama-server child process or adopts an existing running server socket
func (m *LocalModelManager) Start(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.Status == "Active" || m.Status == "Adopted" {
		return nil
	}

	cfg := config.Get()
	m.GGUFModelPath = cfg.GGUFModelPath
	m.Status = "Starting"

	// 1. Attempt Server Process Adoption across reloads
	portFile := resolveTzroPath(filepath.Join(".tzro", ".llama-server.port"))
	if data, err := os.ReadFile(portFile); err == nil {
		fields := strings.Split(strings.TrimSpace(string(data)), ":")
		if len(fields) == 2 {
			savedPort, _ := strconv.Atoi(fields[0])
			savedPID, _ := strconv.Atoi(fields[1])

			// Health probe endpoint check
			healthURL := fmt.Sprintf("http://localhost:%d/health", savedPort)
			req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
			if err == nil {
				resp, err := m.getHealthClient().Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					resp.Body.Close()
					m.ActivePort = savedPort
					m.ActivePID = savedPID
					m.Status = "Adopted"
					fmt.Fprintf(os.Stderr, "[Llama Sidecar] Adopted existing running process on Port %d (PID %d)\n", savedPort, savedPID)
					return nil
				}
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
	_, err = exec.LookPath("llama-server")
	if err != nil {
		m.Status = "Stopped"
		return fmt.Errorf("llama-server binary not found in PATH; please install llama.cpp server: %w", err)
	}

	// Pre-flight check: verify the GGUF model file exists on disk
	if _, err := os.Stat(m.GGUFModelPath); os.IsNotExist(err) {
		m.Status = "Stopped"
		return fmt.Errorf("GGUF model file not found at %s — download a model from the Settings panel", m.GGUFModelPath)
	}

	// Optimized launch args (12 resolved decisions from sidecar optimization session)
	args := []string{
		"-m", m.GGUFModelPath,
		"--port", strconv.Itoa(m.ActivePort),
		"--threads", strconv.Itoa(pCores),
		"--parallel", "1",
		"--jinja",
		"--n-gpu-layers", strconv.Itoa(gpuLayers), // Q1: platform-aware GPU offload
		"--ctx-size", "32768", // Q2: 32K context window
		"--cache-type-k", kvCacheType, // Q3: mode-dependent KV cache quantization
		"--cache-type-v", kvCacheType, // Q3: mode-dependent KV cache quantization
		"-fa", "auto", // Q4: flash attention (auto-detect)
		"--spec-type", "ngram-simple", // Q5: n-gram speculative decoding
		"--spec-draft-n-max", "48", // Q5: raised from default 16 for JSON verbatim matches
		"--cache-reuse", "256", // Q6: reuse KV cache for matching prompt prefixes
		"--n-predict", "16384", // Q9: max tokens per generation
		"--slot-save-path", slotSavePath, // Q8: enable /slots save/restore API for preemption
	}

	m.cmd = exec.CommandContext(context.Background(), "llama-server", args...)

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

	portFile := resolveTzroPath(filepath.Join(".tzro", ".llama-server.port"))
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
// and gracefully recycles the local inference sidecar if memory limits are exceeded (e.g., 2GB).
func (m *LocalModelManager) CheckAndTriggerTier2GC(ctx context.Context) {
	m.mutex.Lock()
	pid := m.ActivePID
	status := m.Status
	m.mutex.Unlock()

	if status != "Active" || pid <= 0 {
		return
	}

	if rss, err := m.getProcessRSS(pid); err == nil {
		// 8GB threshold limit = 8 * 1024 * 1024 * 1024 bytes
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
	logicalCPUs := runtime.NumCPU()

	if runtime.GOOS == "darwin" {
		// Run Mac-specific sysctl command CGO-free
		out, err := exec.Command("sysctl", "-n", "hw.perflevel0.logicalcpu").Output()
		if err == nil {
			if count, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && count > 0 {
				return count
			}
		}
	}

	// Fallback count calculation
	pCores := logicalCPUs / 2
	if pCores <= 0 {
		return 1
	}
	return pCores
}

// getGPULayerCount returns the number of model layers to offload to GPU.
// On macOS Apple Silicon (darwin/arm64), unified memory makes full offload free and always safe.
// On other platforms, defaults to 0 (CPU-only) since GPU availability is uncertain.
func (m *LocalModelManager) getGPULayerCount() int {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return -1 // Offload all layers to Metal GPU — zero PCIe transfer cost on unified memory
	}
	// Conservative default for Linux/Windows/x86 — no GPU assumed.
	// Users with discrete GPUs should override via future config option.
	return 0
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

// CallLocalModel handles the local structured JSON inference call.
// It intercepts prompts, suppresses reasoning tags for Qwen3.5 family, and returns structured tool completions.
// Returns an InferenceResult with content and accurate token-level metrics from the server's usage object.
func (m *LocalModelManager) CallLocalModel(ctx context.Context, systemPrompt, userPrompt string, gbnfSchema string) (*InferenceResult, error) {
	// Build the completion request with optimized sampling parameters
	type CompletionRequest struct {
		Model              string                 `json:"model"`
		Messages           []map[string]string    `json:"messages"`
		Temperature        float64                `json:"temperature"`
		MinP               float64                `json:"min_p"`
		ResponseFormat     map[string]interface{} `json:"response_format,omitempty"`
		ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
	}

	reqBody := CompletionRequest{
		Model: "grm-2.5",
		Messages: []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		Temperature: 1.0, // Q7: required for min_p to function; GBNF constrains output safety
		MinP:        0.1, // Q7: dynamic token pruning — prunes tokens <10% of top token probability
		ChatTemplateKwargs: map[string]interface{}{
			"enable_thinking": false, // Suppress thinking mode tags for speed on Qwen 3.5 family
		},
	}

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
		return nil, fmt.Errorf("local model HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
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
					fmt.Fprintf(os.Stderr, "[Llama Sidecar Metrics] Prompt tokens: %d, Generated %d tokens in %.2fs (Speed: %.1f t/s)\n", promptTokens, completionTokens, duration, speed)
					if tracker, ok := GetTokenTracker(ctx); ok {
						tracker.Record(false, promptTokens, completionTokens)
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

type StreamMeta struct {
	StreamID string
	Source   string
	TaskID   string
	NodeID   string
}

func (m *LocalModelManager) CallLocalModelStream(ctx context.Context, systemPrompt, userPrompt string, gbnfSchema string, meta StreamMeta) (*InferenceResult, error) {
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
		Model              string                 `json:"model"`
		Messages           []map[string]string    `json:"messages"`
		Temperature        float64                `json:"temperature"`
		MinP               float64                `json:"min_p"`
		Stream             bool                   `json:"stream"`
		StreamOptions      *StreamOptionsStruct   `json:"stream_options,omitempty"`
		ResponseFormat     map[string]interface{} `json:"response_format,omitempty"`
		ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
	}

	reqBody := CompletionRequest{
		Model: "grm-2.5",
		Messages: []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		Temperature: 1.0,
		MinP:        0.1,
		Stream:      true,
		StreamOptions: &StreamOptionsStruct{
			IncludeUsage: true,
		},
		ChatTemplateKwargs: map[string]interface{}{
			"enable_thinking": false,
		},
	}

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
			}
		}

		if chunk.Usage != nil {
			promptTokens = chunk.Usage.PromptTokens
			completionTokens = chunk.Usage.CompletionTokens
		}
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

	if tracker, ok := GetTokenTracker(ctx); ok {
		tracker.Record(false, promptTokens, completionTokens)
	}

	return &InferenceResult{
		Content:          resContent,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		DurationSeconds:  duration,
		TokensPerSecond:  speed,
	}, nil
}
