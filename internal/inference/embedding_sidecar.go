package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"tzro/internal/config"
)

// DefaultEmbeddingModelURL is the auto-download URL for All-MiniLM-L6-v2-Q8.
const DefaultEmbeddingModelURL = "https://huggingface.co/second-state/All-MiniLM-L6-v2-Embedding-GGUF/resolve/main/all-MiniLM-L6-v2-Q8_0.gguf?download=true"

// EmbeddingSidecar manages a dedicated llama-server process for neural embeddings.
// Unlike LocalModelManager, this is a minimal struct focused solely on embedding.
// ADR-0075: Neural Embedding Sidecar
type EmbeddingSidecar struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	ActivePort int
	ActivePID  int
	Status     string // "Stopped", "Starting", "Active", "Adopted"
	ModelPath  string
	cache      *EmbeddingCache

	httpClient *http.Client
}

// GlobalEmbeddingSidecar is the singleton embedding sidecar instance.
var GlobalEmbeddingSidecar = &EmbeddingSidecar{
	Status: "Stopped",
	httpClient: &http.Client{
		Timeout: 10 * time.Second,
	},
}

// Start launches the embedding sidecar or adopts an existing one.
// If the model file doesn't exist, it auto-downloads it.
func (s *EmbeddingSidecar) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status == "Active" || s.Status == "Adopted" {
		return nil
	}

	s.ModelPath = config.GetEmbeddingModelPath()
	s.Status = "Starting"

	// Auto-download if model doesn't exist
	if _, err := os.Stat(s.ModelPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Model not found at %s, auto-downloading (~23MB)...\n", s.ModelPath)
		if err := s.downloadModel(); err != nil {
			s.Status = "Stopped"
			return fmt.Errorf("failed to auto-download embedding model: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Download complete: %s\n", s.ModelPath)
	}

	// Initialize cache with model filename as ID (invalidates on model change)
	s.cache = NewEmbeddingCache(filepath.Base(s.ModelPath))

	// Try to adopt existing process
	portFile := config.ResolvePath(".llama-embed.port")
	if data, err := os.ReadFile(portFile); err == nil {
		fields := strings.Split(strings.TrimSpace(string(data)), ":")
		if len(fields) == 2 {
			savedPort, _ := strconv.Atoi(fields[0])
			savedPID, _ := strconv.Atoi(fields[1])

			if proc, err := os.FindProcess(savedPID); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					// Process alive — health check
					healthURL := fmt.Sprintf("http://localhost:%d/health", savedPort)
					req, _ := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
					if resp, err := s.httpClient.Do(req); err == nil && resp.StatusCode == http.StatusOK {
						resp.Body.Close()
						s.ActivePort = savedPort
						s.ActivePID = savedPID
						s.Status = "Adopted"
						fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Adopted existing process on Port %d (PID %d)\n", savedPort, savedPID)
						return nil
					}
				}
			}
			_ = os.Remove(portFile)
		}
	}

	// Allocate port
	port, err := allocateRandomPort()
	if err != nil {
		s.Status = "Stopped"
		return fmt.Errorf("failed to allocate port for embedding sidecar: %w", err)
	}
	s.ActivePort = port

	// Find llama-server binary
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
			s.Status = "Stopped"
			return fmt.Errorf("llama-server binary not found: %w", lookErr)
		}
		llamaBinary = pathBinary
	}

	// Launch with embedding-specific flags
	args := []string{
		"--model", s.ModelPath,
		"--port", strconv.Itoa(port),
		"--embedding",
		"--pooling", "mean",
		"--ctx-size", "512",
		"--batch-size", "512",
		"--threads", "2",
		"--log-disable",
	}

	s.cmd = exec.CommandContext(context.Background(), llamaBinary, args...)

	// Redirect output to log file
	logsDir := config.ResolvePath(filepath.Join(".tzro", "logs"))
	_ = os.MkdirAll(logsDir, 0755)
	logFile, err := os.Create(filepath.Join(logsDir, "llama-embed.log"))
	if err == nil {
		s.cmd.Stdout = logFile
		s.cmd.Stderr = logFile
	}

	if err := s.cmd.Start(); err != nil {
		s.Status = "Stopped"
		return fmt.Errorf("failed to start embedding sidecar: %w", err)
	}

	s.ActivePID = s.cmd.Process.Pid
	s.Status = "Active"

	// Write port file
	_ = os.WriteFile(portFile, []byte(fmt.Sprintf("%d:%d", port, s.ActivePID)), 0644)
	fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Launched on Port %d (PID %d)\n", port, s.ActivePID)

	// Wait for health
	if err := s.waitForHealth(ctx); err != nil {
		s.Stop()
		return fmt.Errorf("embedding sidecar failed health check: %w", err)
	}

	return nil
}

// Stop terminates the embedding sidecar process.
func (s *EmbeddingSidecar) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
		}
	}

	portFile := config.ResolvePath(".llama-embed.port")
	_ = os.Remove(portFile)
	s.Status = "Stopped"
	s.ActivePort = 0
	s.ActivePID = 0
	return nil
}

// IsAvailable returns true if the embedding sidecar is running and healthy.
func (s *EmbeddingSidecar) IsAvailable() bool {
	return s.Status == "Active" || s.Status == "Adopted"
}

// Embed implements embeddings.EmbeddingEngine — returns a neural embedding vector
// for a single text string. Uses the cache transparently.
func (s *EmbeddingSidecar) Embed(ctx context.Context, text string) ([]float32, error) {
	if !s.IsAvailable() {
		return nil, fmt.Errorf("embedding sidecar not available")
	}

	hash := TextHash(text)
	if vec, ok := s.cache.Get(hash); ok {
		return vec, nil
	}

	vecs, err := s.embedHTTP(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}

	s.cache.Put(hash, vecs[0])
	return vecs[0], nil
}

// EmbedBatch embeds multiple texts in a single HTTP call, using the cache.
// Cache hits are returned immediately; only misses are sent to the sidecar.
func (s *EmbeddingSidecar) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if !s.IsAvailable() {
		return nil, fmt.Errorf("embedding sidecar not available")
	}

	results := make([][]float32, len(texts))
	hashes := make([]string, len(texts))
	for i, t := range texts {
		hashes[i] = TextHash(t)
	}

	// Check cache
	var missIndices []int
	var missTexts []string
	for i, h := range hashes {
		if vec, ok := s.cache.Get(h); ok {
			results[i] = vec
		} else {
			missIndices = append(missIndices, i)
			missTexts = append(missTexts, texts[i])
		}
	}

	if len(missTexts) == 0 {
		return results, nil
	}

	// Batch embed misses
	vecs, err := s.embedHTTP(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(missTexts) {
		return nil, fmt.Errorf("embedding response count mismatch: got %d, want %d", len(vecs), len(missTexts))
	}

	// Store results
	for i, idx := range missIndices {
		results[idx] = vecs[i]
		s.cache.Put(hashes[idx], vecs[i])
	}

	return results, nil
}

// CosineSimilarity implements embeddings.EmbeddingEngine.
func (s *EmbeddingSidecar) CosineSimilarity(v1, v2 []float32) float32 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0.0
	}
	var dot, norm1, norm2 float32
	for i := range v1 {
		dot += v1[i] * v2[i]
		norm1 += v1[i] * v1[i]
		norm2 += v2[i] * v2[i]
	}
	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}
	return dot / (float32(math.Sqrt(float64(norm1))) * float32(math.Sqrt(float64(norm2))))
}

// embedHTTP sends a batch embedding request to the sidecar's /v1/embeddings endpoint.
func (s *EmbeddingSidecar) embedHTTP(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := map[string]interface{}{
		"input": texts,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d/v1/embeddings", s.ActivePort)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding sidecar returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	vecs := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		vecs[i] = d.Embedding
	}
	return vecs, nil
}

// waitForHealth waits up to 15 seconds for the sidecar to become healthy.
func (s *EmbeddingSidecar) waitForHealth(ctx context.Context) error {
	healthURL := fmt.Sprintf("http://localhost:%d/health", s.ActivePort)
	for attempt := 0; attempt < 30; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
		resp, err := s.httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Healthy after %d attempts\n", attempt+1)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("embedding sidecar did not become healthy within 15s")
}

// downloadModel downloads the default embedding model from HuggingFace.
func (s *EmbeddingSidecar) downloadModel() error {
	modelsDir := config.GetModelsDir()
	_ = os.MkdirAll(modelsDir, 0755)

	destPath := s.ModelPath
	tmpPath := destPath + ".tmp"

	resp, err := http.Get(DefaultEmbeddingModelURL)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	n, err := io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download interrupted: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[Embedding Sidecar] Downloaded %d bytes\n", n)

	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}
