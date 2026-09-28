package decision_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tzro/pkg/decision"
)

func TestRemoteHTTPProvider_EvaluatesEndpoint(t *testing.T) {
	// Setup test server
	var receivedAuth string
	var receivedReq decision.DecisionRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")

		if err := json.NewDecoder(r.Body).Decode(&receivedReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := decision.DecisionResponse{
			Answer:     "pkg/proxy/proxy.go",
			Confidence: 0.96,
			Scores: map[string]float64{
				"pkg/proxy/proxy.go": 0.96,
				"pkg/store/store.go": 0.04,
			},
			LatencyMs: 22,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	provider := decision.NewRemoteHTTPProvider(ts.URL, "secret-token", 5*time.Second)
	defer provider.Close()

	req := &decision.DecisionRequest{
		QuestionType: decision.QuestionTypeChoice,
		Prompt:       "Which file is relevant?",
		Options:      []string{"pkg/proxy/proxy.go", "pkg/store/store.go"},
	}

	resp, err := provider.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if receivedAuth != "Bearer secret-token" {
		t.Errorf("expected auth 'Bearer secret-token', got '%s'", receivedAuth)
	}
	if resp.Answer != "pkg/proxy/proxy.go" {
		t.Errorf("expected answer pkg/proxy/proxy.go, got %s", resp.Answer)
	}
	if resp.Confidence != 0.96 {
		t.Errorf("expected confidence 0.96, got %f", resp.Confidence)
	}
}

func TestFactory_CreatesProviderFromConfig(t *testing.T) {
	// Remote provider from config
	remoteCfg := &decision.Config{
		Provider:  "remote",
		RemoteURL: "https://api.example.com",
		APIKey:    "test-key",
	}

	p, err := decision.NewProviderFromConfig(remoteCfg)
	if err != nil {
		t.Fatalf("failed to create remote provider: %v", err)
	}
	if _, ok := p.(*decision.RemoteHTTPProvider); !ok {
		t.Errorf("expected *RemoteHTTPProvider, got %T", p)
	}
	_ = p.Close()

	// Local provider from config
	localCfg := &decision.Config{
		Provider: "local",
		BinPath:  "/bin/echo",
	}
	lp, err := decision.NewProviderFromConfig(localCfg)
	if err != nil {
		t.Fatalf("failed to create local provider: %v", err)
	}
	if _, ok := lp.(*decision.LocalDaemonProvider); !ok {
		t.Errorf("expected *LocalDaemonProvider, got %T", lp)
	}
	_ = lp.Close()
}
