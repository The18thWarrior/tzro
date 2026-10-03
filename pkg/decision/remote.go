package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// RemoteHTTPProvider connects to an external Jev / SystemOne compatible HTTP service.
type RemoteHTTPProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewRemoteHTTPProvider creates a new provider connecting over HTTP/REST.
func NewRemoteHTTPProvider(baseURL, apiKey string, timeout time.Duration) *RemoteHTTPProvider {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &RemoteHTTPProvider{
		client:  &http.Client{Timeout: timeout},
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
	}
}

// Evaluate dispatches the decision request via HTTP POST.
func (p *RemoteHTTPProvider) Evaluate(ctx context.Context, req *DecisionRequest) (*DecisionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	targetURL := p.baseURL
	if !strings.HasSuffix(targetURL, "/decide") && !strings.HasSuffix(targetURL, "/systemone") {
		// If base URL without endpoint is provided, use target endpoint or base URL directly
		// If target URL has a path, use it; otherwise default to targetURL
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http dispatch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected http status %d: %s", resp.StatusCode, resp.Status)
	}

	var decisionResp DecisionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decisionResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if decisionResp.Error != "" {
		return nil, fmt.Errorf("remote provider error: %s", decisionResp.Error)
	}

	return &decisionResp, nil
}

// Close is a no-op for the HTTP provider.
func (p *RemoteHTTPProvider) Close() error {
	p.client.CloseIdleConnections()
	return nil
}
