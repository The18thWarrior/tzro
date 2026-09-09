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
	"strings"
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
	UpstreamGemini    string
	UpstreamLocal     string
	Store             *store.Store
	Policy            *dlp.PolicyEngine
}

// Metrics tracks token shield performance in real-time.
type Metrics struct {
	TotalRequests     uint64 `json:"total_requests"`
	AnthropicRequests uint64 `json:"anthropic_requests"`
	OpenAIRequests    uint64 `json:"openai_requests"`
	ResponsesRequests uint64 `json:"responses_requests"`
	GeminiRequests    uint64 `json:"gemini_requests"`
	LocalRequests     uint64 `json:"local_requests"`
	BytesProcessed    uint64 `json:"bytes_processed"`
	SecretsRedacted   uint64 `json:"secrets_redacted"`
	PoliciesBlocked     uint64 `json:"policies_blocked"`
	UptimeSeconds       int64  `json:"uptime_seconds"`
	MemoryAllocMB       uint64 `json:"memory_alloc_mb"`
	MeasuredInputTokens *int64 `json:"measured_input_tokens"`
	MeasuredOutputTokens *int64 `json:"measured_output_tokens"`
	MeasuredTotalTokens *int64 `json:"measured_total_tokens"`
	NativeCacheHitTokens *int64 `json:"native_cache_hit_tokens"`
	TzroPrefixLockTokens *int64 `json:"tzro_prefix_lock_tokens"`
}

// Server is the transparent reverse proxy.
type Server struct {
	cfg     Config
	httpSrv *http.Server
	dlp     *dlp.Redactor
	policy  *dlp.PolicyEngine
	kvLock  *kvlock.LockGuard
	store   *store.Store
	metrics Metrics
	startAt time.Time
	mu      sync.RWMutex
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
	if cfg.UpstreamGemini == "" {
		if env := os.Getenv("TZRO_UPSTREAM_GEMINI"); env != "" {
			cfg.UpstreamGemini = env
		} else {
			cfg.UpstreamGemini = "https://generativelanguage.googleapis.com"
		}
	}
	if cfg.UpstreamLocal == "" {
		if env := os.Getenv("TZRO_UPSTREAM_LOCAL"); env != "" {
			cfg.UpstreamLocal = env
		} else {
			cfg.UpstreamLocal = "http://127.0.0.1:11434"
		}
	}

	policy := cfg.Policy
	if policy == nil {
		policy = dlp.NewPolicyEngine(nil)
	}

	s := &Server{
		cfg:     cfg,
		dlp:     dlp.NewRedactor(),
		policy:  policy,
		kvLock:  kvlock.NewLockGuard(),
		store:   cfg.Store,
		startAt: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", s.handleAnthropic)
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAI)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/v1beta/", s.handleGemini)
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

// Handler returns the HTTP handler for use with httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return s.httpSrv.Handler
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
		ResponsesRequests: atomic.LoadUint64(&s.metrics.ResponsesRequests),
		GeminiRequests:    atomic.LoadUint64(&s.metrics.GeminiRequests),
		LocalRequests:     atomic.LoadUint64(&s.metrics.LocalRequests),
		BytesProcessed:    atomic.LoadUint64(&s.metrics.BytesProcessed),
		SecretsRedacted:   atomic.LoadUint64(&s.metrics.SecretsRedacted),
		PoliciesBlocked:   atomic.LoadUint64(&s.metrics.PoliciesBlocked),
		UptimeSeconds:     int64(time.Since(s.startAt).Seconds()),
		MemoryAllocMB:     m.Alloc / 1024 / 1024,
	}

	s.mu.RLock()
	if s.metrics.MeasuredInputTokens != nil {
		v := *s.metrics.MeasuredInputTokens
		snap.MeasuredInputTokens = &v
	}
	if s.metrics.MeasuredOutputTokens != nil {
		v := *s.metrics.MeasuredOutputTokens
		snap.MeasuredOutputTokens = &v
	}
	if s.metrics.MeasuredTotalTokens != nil {
		v := *s.metrics.MeasuredTotalTokens
		snap.MeasuredTotalTokens = &v
	}
	if s.metrics.NativeCacheHitTokens != nil {
		v := *s.metrics.NativeCacheHitTokens
		snap.NativeCacheHitTokens = &v
	}
	if s.metrics.TzroPrefixLockTokens != nil {
		v := *s.metrics.TzroPrefixLockTokens
		snap.TzroPrefixLockTokens = &v
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

func (s *Server) handleAnthropic(w http.ResponseWriter, r *http.Request) {
	if s.isHealthProbe(r) {
		s.respondProbe(w, r)
		return
	}

	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.AnthropicRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	// Privacy Policy Check (fail-closed)
	if s.policy != nil {
		eval := s.policy.EvaluateContent(string(bodyBytes))
		if !eval.Allowed {
			atomic.AddUint64(&s.metrics.PoliciesBlocked, 1)
			http.Error(w, fmt.Sprintf("Egress blocked by workspace privacy policy: %s", eval.Reason), http.StatusForbidden)
			return
		}
	}

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
	if s.isHealthProbe(r) {
		s.respondProbe(w, r)
		return
	}

	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.OpenAIRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	// Privacy Policy Check (fail-closed)
	if s.policy != nil {
		eval := s.policy.EvaluateContent(string(bodyBytes))
		if !eval.Allowed {
			atomic.AddUint64(&s.metrics.PoliciesBlocked, 1)
			http.Error(w, fmt.Sprintf("Egress blocked by workspace privacy policy: %s", eval.Reason), http.StatusForbidden)
			return
		}
	}

	// 1. DLP Secret Redaction
	redactedText, dlpMap := s.dlp.Redact(string(bodyBytes))
	if len(dlpMap) > 0 {
		atomic.AddUint64(&s.metrics.SecretsRedacted, uint64(len(dlpMap)))
	}

	// 2. KV-Cache Prefix Locking & Normalization
	normalized, _, _ := s.kvLock.NormalizeOpenAI([]byte(redactedText))

	// 3. Proxy to upstream (route to local if model or host indicates local Ollama/LM Studio)
	upstream := s.cfg.UpstreamOpenAI
	isLocal := false
	if r.Header.Get("X-Tzro-Local") == "true" || strings.Contains(r.Host, "localhost") || strings.Contains(r.Host, "127.0.0.1") {
		// If explicit local routing or local caller specifies local upstream
		if s.cfg.UpstreamLocal != "" && (r.Header.Get("X-Tzro-Local") == "true" || strings.HasPrefix(s.cfg.UpstreamOpenAI, "http://127.0.0.1") || strings.HasPrefix(s.cfg.UpstreamOpenAI, "http://localhost")) {
			upstream = s.cfg.UpstreamLocal
			isLocal = true
		}
	}
	if isLocal {
		atomic.AddUint64(&s.metrics.LocalRequests, 1)
	}

	targetURL, _ := url.Parse(upstream + "/v1/chat/completions")
	s.forwardRequest(w, r, targetURL, normalized, dlpMap)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	if s.isHealthProbe(r) {
		s.respondProbe(w, r)
		return
	}

	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.ResponsesRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	if s.policy != nil {
		eval := s.policy.EvaluateContent(string(bodyBytes))
		if !eval.Allowed {
			atomic.AddUint64(&s.metrics.PoliciesBlocked, 1)
			http.Error(w, fmt.Sprintf("Egress blocked by workspace privacy policy: %s", eval.Reason), http.StatusForbidden)
			return
		}
	}

	redactedText, dlpMap := s.dlp.Redact(string(bodyBytes))
	if len(dlpMap) > 0 {
		atomic.AddUint64(&s.metrics.SecretsRedacted, uint64(len(dlpMap)))
	}

	normalized, _, _ := s.kvLock.NormalizeResponses([]byte(redactedText))

	targetURL, _ := url.Parse(s.cfg.UpstreamOpenAI + "/v1/responses")
	s.forwardRequest(w, r, targetURL, normalized, dlpMap)
}

func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request) {
	if s.isHealthProbe(r) {
		s.respondProbe(w, r)
		return
	}

	atomic.AddUint64(&s.metrics.TotalRequests, 1)
	atomic.AddUint64(&s.metrics.GeminiRequests, 1)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	atomic.AddUint64(&s.metrics.BytesProcessed, uint64(len(bodyBytes)))

	if s.policy != nil {
		eval := s.policy.EvaluateContent(string(bodyBytes))
		if !eval.Allowed {
			atomic.AddUint64(&s.metrics.PoliciesBlocked, 1)
			http.Error(w, fmt.Sprintf("Egress blocked by workspace privacy policy: %s", eval.Reason), http.StatusForbidden)
			return
		}
	}

	redactedText, dlpMap := s.dlp.Redact(string(bodyBytes))
	if len(dlpMap) > 0 {
		atomic.AddUint64(&s.metrics.SecretsRedacted, uint64(len(dlpMap)))
	}

	normalized, _, _ := s.kvLock.NormalizeGemini([]byte(redactedText))

	// Forward with intact path and query params (e.g. key=... or streamGenerateContent)
	targetURL, _ := url.Parse(s.cfg.UpstreamGemini + r.URL.RequestURI())
	s.forwardRequest(w, r, targetURL, normalized, dlpMap)
}

// isHealthProbe returns true if the request is a zero-cost health probe
// (OPTIONS method or X-Tzro-Probe: health header).
func (s *Server) isHealthProbe(r *http.Request) bool {
	return r.Method == http.MethodOptions || r.Header.Get("X-Tzro-Probe") == "health"
}

// respondProbe writes a JSON health probe response with route metadata.
// Zero body read, zero DLP evaluation, zero upstream egress.
func (s *Server) respondProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Tzro-Route-Status", "active")
	w.Header().Set("X-Tzro-Features", "kvlock,dlp")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "active",
		"route":    r.URL.Path,
		"features": []string{"kvlock", "dlp"},
	})
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

	// Stream response back to client (flushing immediately for SSE tokens) and buffer for usage telemetry
	flusher, isFlusher := w.(http.Flusher)
	buf := make([]byte, 4096)
	var responseAccumulator bytes.Buffer

	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			responseAccumulator.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
		}
		if err != nil {
			break
		}
	}

	// Extract empirical token usage
	respBytes := responseAccumulator.Bytes()
	var usage TokenUsage
	var hasUsage bool

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		usage, hasUsage = ExtractUsageFromSSE(respBytes)
	} else {
		usage, hasUsage = ExtractUsageFromJSON(respBytes)
	}

	if hasUsage && usage.IsMeasured {
		s.mu.Lock()
		if usage.PromptTokens != nil {
			if s.metrics.MeasuredInputTokens == nil {
				v := *usage.PromptTokens
				s.metrics.MeasuredInputTokens = &v
			} else {
				*s.metrics.MeasuredInputTokens += *usage.PromptTokens
			}
		}
		if usage.CompletionTokens != nil {
			if s.metrics.MeasuredOutputTokens == nil {
				v := *usage.CompletionTokens
				s.metrics.MeasuredOutputTokens = &v
			} else {
				*s.metrics.MeasuredOutputTokens += *usage.CompletionTokens
			}
		}
		if usage.TotalTokens != nil {
			if s.metrics.MeasuredTotalTokens == nil {
				v := *usage.TotalTokens
				s.metrics.MeasuredTotalTokens = &v
			} else {
				*s.metrics.MeasuredTotalTokens += *usage.TotalTokens
			}
		}
		if usage.NativeCacheRead != nil {
			if s.metrics.NativeCacheHitTokens == nil {
				v := *usage.NativeCacheRead
				s.metrics.NativeCacheHitTokens = &v
			} else {
				*s.metrics.NativeCacheHitTokens += *usage.NativeCacheRead
			}
		}
		if usage.TzroPrefixLocked != nil {
			if s.metrics.TzroPrefixLockTokens == nil {
				v := *usage.TzroPrefixLocked
				s.metrics.TzroPrefixLockTokens = &v
			} else {
				*s.metrics.TzroPrefixLockTokens += *usage.TzroPrefixLocked
			}
		}
		s.mu.Unlock()
	}
}
