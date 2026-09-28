package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Start the installed public command, so Full uses the user's proxy entry point.
func startProxy(ctx context.Context, cfg Config, p *prepared) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	proxyCtx, cancel := context.WithCancel(ctx)
	upstream := strings.TrimSuffix(strings.TrimSuffix(cfg.BaseURL, "/"), "/v1")
	env := append(append([]string{}, p.env...), "TZRO_UPSTREAM_LOCAL="+upstream)
	cmd := command(proxyCtx, env, p.result.Workspace, p.binary, "start", "--port", strconv.Itoa(port), "--upstream-openai", upstream)
	if err := cmd.Start(); err != nil {
		cancel()
		return "", func() {}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			cancel()
			return "", func() {}, fmt.Errorf("proxy exited before readiness: %v", err)
		case <-ctx.Done():
			stop()
			return "", func() {}, ctx.Err()
		case <-ticker.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodOptions, base+"/v1/chat/completions", nil)
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base + "/v1", stop, nil
			}
		}
	}
}

func proxyRequests(base string) (int, error) {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(strings.TrimSuffix(base, "/v1") + "/metrics")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var metrics struct {
		Total int `json:"total_requests"`
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("proxy metrics: HTTP %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&metrics)
	return metrics.Total, err
}
