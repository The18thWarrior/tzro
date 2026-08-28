package proxy

import (
	"bytes"
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
	if !strings.Contains(receivedBody, "[REDACTED_OPENAI_KEY_1]") {
		t.Errorf("expected redacted placeholder in upstream payload, got:\n%s", receivedBody)
	}
}
