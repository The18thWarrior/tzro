package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"tzro/pkg/dlp"
	"tzro/pkg/kvlock"
	"tzro/pkg/store"
)

// Config holds proxy configuration options.
type Config struct {
	ListenAddr        string
	UpstreamAnthropic string
	UpstreamOpenAI    string
	Store             *store.Store
}

// Metrics tracks token shield performance in real-time.
type Metrics struct {
	TotalRequests     uint64 `json:"total_requests"`
	AnthropicRequests uint64 `json:"anthropic_requests"`
	OpenAIRequests    uint64 `json:"openai_requests"`
	BytesProcessed    uint64 `json:"bytes_processed"`
	SecretsRedacted   uint64 `json:"secrets_redacted"`
	UptimeSeconds     int64  `json:"uptime_seconds"`
	MemoryAllocMB     uint64 `json:"memory_alloc_mb"`
}

// Server is the transparent reverse proxy.
type Server struct {
	cfg      Config
	httpSrv  *http.Server
	dlp      *dlp.Redactor
	kvLock   *kvlock.LockGuard
	store    *store.Store
	metrics  Metrics
	startAt  time.Time
	mu       sync.RWMutex
}

// NewServer initializes the proxy server.
func NewServer(cfg Config) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:7878"
	}
	if cfg.UpstreamAnthropic == "" {
		if env := os.Getenv("TZRO_UPSTREAM_ANTHROPIC"); env != "" {
			cfg.UpstreamAnthropic = env
		} else {
			cfg.UpstreamAnthropic = "https://api.anthropic.com"
		}
	}
	if cfg.UpstreamOpenAI == "" {
		if env := os.Getenv("TZRO_UPSTREAM_OPENAI"); env != "" {
			cfg.UpstreamOpenAI = env
		} else {
			cfg.UpstreamOpenAI = "https://api.openai.com"
		}
	}

	s := &Server{
		cfg:     cfg,
		dlp:     dlp.NewRedactor(),
		kvLock:  kvlock.NewLockGuard(),
		store:   cfg.Store,
		startAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleAnthropic)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAI)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/health", s.handleHealth)

	s.httpSrv = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	return s
}

// Start launches the proxy server.
func (s *Server) Start() error {
	return s.httpSrv.ListenAndServe()
}

// Shutdown stops the proxy server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok","engine":"tzro-v2-token-shield"}`))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	snap := Metrics{
		TotalRequests:     atomic.LoadUint64(&s.metrics.TotalRequests),
		AnthropicRequests: atomic.LoadUint64(&s.metrics.AnthropicRequests),
		OpenAIRequests:    atomic.LoadUint64(&s.metrics.OpenAIRequests),
		BytesProcessed:    atomic.LoadUint64(&s.metrics.BytesProcessed),
		SecretsRedacted:   atomic.LoadUint64(&s.metrics.SecretsRedacted),
		UptimeSeconds:     int64(time.Since(s.startAt).Seconds()),
		MemoryAllocMB:     m.Alloc / 1024 / 1024,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.AnthropicRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	// 1. DLP Secret Redaction
	redactedText, dlpMap := s.dlp.Redact(string(bodyBytes))
	if len(dlpMap) > 0 {
		atomic.AddUint64(&s.metrics.SecretsRedacted, uint64(len(dlpMap)))
	}

	// 2. KV-Cache Prefix Locking & Normalization
	normalized, _, _ := s.kvLock.NormalizeAnthropic([]byte(redactedText))

	// 3. Proxy to upstream
	targetURL, _ := url.Parse(s.cfg.UpstreamAnthropic + "/v1/messages")
	s.forwardRequest(w, r, targetURL, normalized, dlpMap)
}

func (s *Server) handleOpenAI(w http.ResponseWriter, r *http.Request) {
	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.OpenAIRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	// 1. DLP Secret Redaction
	redactedText, dlpMap := s.dlp.Redact(string(bodyBytes))
	if len(dlpMap) > 0 {
		atomic.AddUint64(&s.metrics.SecretsRedacted, uint64(len(dlpMap)))
	}

	// 2. KV-Cache Prefix Locking & Normalization
	normalized, _, _ := s.kvLock.NormalizeOpenAI([]byte(redactedText))

	// 3. Proxy to upstream
	targetURL, _ := url.Parse(s.cfg.UpstreamOpenAI + "/v1/chat/completions")
	s.forwardRequest(w, r, targetURL, normalized, dlpMap)
}

func (s *Server) forwardRequest(w http.ResponseWriter, r *http.Request, targetURL *url.URL, body []byte, dlpMap map[string]string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "Failed to create upstream request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		if key != "Host" && key != "Content-Length" {
			for _, val := range values {
				req.Header.Add(key, val)
			}
		}
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Upstream request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, val := range values {
			w.Header().Add(key, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream response back to client (flushing immediately for SSE tokens)
	flusher, isFlusher := w.(http.Flusher)
	buf := make([]byte, 4096)

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}
}
