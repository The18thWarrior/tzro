package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestProxy_AnthropicInterceptionAndDLP(t *testing.T) {
	// Mock upstream Anthropic server
	var receivedBody string
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_123","type":"message","content":[{"type":"text","text":"Hello from mock Anthropic"}]}`))
	}))
	defer mockUpstream.Close()

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:        "127.0.0.1:0",
		UpstreamAnthropic: mockUpstream.URL,
		Store:             s,
	})

	testClient := httptest.NewServer(proxySrv.httpSrv.Handler)
	defer testClient.Close()

	// Send request with an API key inside prompt to verify DLP redaction
	reqPayload := `{
		"model": "claude-3-5-sonnet",
		"messages": [
			{"role": "user", "content": "My secret key is sk-proj-1234567890abcdef1234567890abcdef please analyze it"}
		]
	}`

	resp, err := http.Post(testClient.URL+"/v1/messages", "application/json", bytes.NewReader([]byte(reqPayload)))
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify upstream did NOT receive the plain-text secret
	if strings.Contains(receivedBody, "sk-proj-") {
		t.Errorf("expected secret to be redacted before hitting upstream, got:\n%s", receivedBody)
	}
	if !strings.Contains(receivedBody, "[REDACTED_OPENAI_KEY_") {
		t.Errorf("expected redacted placeholder in upstream payload, got:\n%s", receivedBody)
	}
}

func TestProxy_HealthProbeShortCircuits(t *testing.T) {
	// Mock upstream: if hit, test fails — probe should never reach upstream
	upstreamHit := false
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer mockUpstream.Close()

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:        "127.0.0.1:0",
		UpstreamAnthropic: mockUpstream.URL,
		UpstreamOpenAI:    mockUpstream.URL,
		UpstreamGemini:    mockUpstream.URL,
		Store:             s,
	})

	testSrv := httptest.NewServer(proxySrv.httpSrv.Handler)
	defer testSrv.Close()

	routes := []string{
		"/v1/messages",
		"/v1/chat/completions",
		"/v1/responses",
		"/v1beta/models/gemini-pro:generateContent",
	}

	for _, route := range routes {
		t.Run("OPTIONS"+route, func(t *testing.T) {
			upstreamHit = false

			req, _ := http.NewRequest(http.MethodOptions, testSrv.URL+route, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
			if got := resp.Header.Get("X-Tzro-Route-Status"); got != "active" {
				t.Errorf("expected X-Tzro-Route-Status=active, got %q", got)
			}
			if upstreamHit {
				t.Errorf("upstream was hit — probe should short-circuit locally")
			}

			// Verify JSON body has expected fields
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode JSON body: %v", err)
			}
			if body["status"] != "active" {
				t.Errorf("expected status=active in body, got %v", body["status"])
			}
		})

		t.Run("X-Tzro-Probe"+route, func(t *testing.T) {
			upstreamHit = false

			req, _ := http.NewRequest(http.MethodGet, testSrv.URL+route, nil)
			req.Header.Set("X-Tzro-Probe", "health")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
			if got := resp.Header.Get("X-Tzro-Route-Status"); got != "active" {
				t.Errorf("expected X-Tzro-Route-Status=active, got %q", got)
			}
			if upstreamHit {
				t.Errorf("upstream was hit — probe should short-circuit locally")
			}
		})
	}
}

