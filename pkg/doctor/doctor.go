// Package doctor provides live diagnostic probing for tzro proxy routes
// and upstream provider connectivity.
package doctor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// RouteStatus indicates the live state of a proxy route.
type RouteStatus string

const (
	RouteStatusActive      RouteStatus = "ACTIVE"
	RouteStatusOffline     RouteStatus = "OFFLINE"
	RouteStatusUnsupported RouteStatus = "UNSUPPORTED"
)

// RouteReport holds diagnostic results for a local proxy route.
type RouteReport struct {
	Route       string        `json:"route"`
	Adapter     string        `json:"adapter"`
	Status      RouteStatus   `json:"status"`
	Features    []string      `json:"features,omitempty"`
	Latency     time.Duration `json:"latency"`
	Intercepted bool          `json:"intercepted"`
	Error       string        `json:"error,omitempty"`
}

// UpstreamStatus indicates connectivity to an upstream provider.
type UpstreamStatus string

const (
	UpstreamStatusReachable   UpstreamStatus = "REACHABLE"
	UpstreamStatusUnreachable UpstreamStatus = "UNREACHABLE"
	UpstreamStatusOffline     UpstreamStatus = "OFFLINE"
)

// UpstreamReport holds DNS/TLS handshake diagnostics for an upstream provider.
type UpstreamReport struct {
	Provider string         `json:"provider"`
	Endpoint string         `json:"endpoint"`
	Status   UpstreamStatus `json:"status"`
	Latency  time.Duration  `json:"latency"`
	TLSValid bool           `json:"tls_valid"`
	IP       string         `json:"ip,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// UpstreamEndpoint describes an upstream provider to probe.
type UpstreamEndpoint struct {
	Provider string // Human-readable name, e.g. "Anthropic API"
	Host     string // Hostname or IP, e.g. "api.anthropic.com"
	Port     string // Port, e.g. "443"
	Local    bool   // If true, treat as local TCP-only check (no TLS)
}

// knownRoutes defines the canonical route registry for tzro doctor.
var knownRoutes = []struct {
	Route       string
	Adapter     string
	Intercepted bool
}{
	{Route: "/v1/messages", Adapter: "Anthropic Messages API", Intercepted: true},
	{Route: "/v1/chat/completions", Adapter: "OpenAI Chat Completions", Intercepted: true},
	{Route: "/v1/responses", Adapter: "OpenAI Responses API", Intercepted: true},
	{Route: "/v1beta/models/probe:generateContent", Adapter: "Gemini-native Models API", Intercepted: true},
	{Route: "/v1/audio/transcriptions", Adapter: "Audio / Multimodal Speech", Intercepted: false},
	{Route: "/v1/realtime", Adapter: "Websocket Realtime Audio", Intercepted: false},
}

// ProbeRoutes sends zero-cost health probes to each known route on the
// local proxy at proxyBaseURL and returns a RouteReport for each.
func ProbeRoutes(proxyBaseURL string) []RouteReport {
	client := &http.Client{Timeout: 500 * time.Millisecond}

	// Check if the proxy daemon is reachable at all
	proxyOnline := false
	if resp, err := client.Get(proxyBaseURL + "/health"); err == nil {
		resp.Body.Close()
		proxyOnline = true
	}

	reports := make([]RouteReport, 0, len(knownRoutes))
	for _, kr := range knownRoutes {
		rpt := RouteReport{
			Route:       kr.Route,
			Adapter:     kr.Adapter,
			Intercepted: kr.Intercepted,
		}

		if !kr.Intercepted {
			rpt.Status = RouteStatusUnsupported
			reports = append(reports, rpt)
			continue
		}

		if !proxyOnline {
			rpt.Status = RouteStatusOffline
			rpt.Error = "Proxy daemon stopped"
			reports = append(reports, rpt)
			continue
		}

		// Send OPTIONS probe with X-Tzro-Probe header
		req, _ := http.NewRequest(http.MethodOptions, proxyBaseURL+kr.Route, nil)
		req.Header.Set("X-Tzro-Probe", "health")

		start := time.Now()
		resp, err := client.Do(req)
		rpt.Latency = time.Since(start)

		if err != nil {
			rpt.Status = RouteStatusOffline
			rpt.Error = err.Error()
			reports = append(reports, rpt)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK && resp.Header.Get("X-Tzro-Route-Status") == "active" {
			rpt.Status = RouteStatusActive
			// Parse features from response body
			var body struct {
				Features []string `json:"features"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
				rpt.Features = body.Features
			}
		} else if resp.StatusCode == http.StatusNotFound {
			rpt.Status = RouteStatusUnsupported
		} else {
			rpt.Status = RouteStatusOffline
			rpt.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}

		reports = append(reports, rpt)
	}

	return reports
}

// DefaultUpstreams returns the canonical set of upstream endpoints for tzro doctor.
func DefaultUpstreams() []UpstreamEndpoint {
	return []UpstreamEndpoint{
		{Provider: "Anthropic API", Host: "api.anthropic.com", Port: "443"},
		{Provider: "OpenAI API", Host: "api.openai.com", Port: "443"},
		{Provider: "Gemini API", Host: "generativelanguage.googleapis.com", Port: "443"},
		{Provider: "Local Provider", Host: "127.0.0.1", Port: "11434", Local: true},
	}
}

// ProbeUpstreams performs concurrent DNS/TLS handshakes against the given
// upstream endpoints, bounded by ctx. Returns one UpstreamReport per endpoint.
// Zero HTTP bodies are transmitted; zero auth tokens are sent.
func ProbeUpstreams(ctx context.Context, endpoints []UpstreamEndpoint) []UpstreamReport {
	reports := make([]UpstreamReport, len(endpoints))
	var wg sync.WaitGroup

	for i, ep := range endpoints {
		wg.Add(1)
		go func(idx int, ep UpstreamEndpoint) {
			defer wg.Done()
			reports[idx] = probeOneUpstream(ctx, ep)
		}(i, ep)
	}

	wg.Wait()
	return reports
}

func probeOneUpstream(ctx context.Context, ep UpstreamEndpoint) UpstreamReport {
	rpt := UpstreamReport{
		Provider: ep.Provider,
		Endpoint: net.JoinHostPort(ep.Host, ep.Port),
	}

	start := time.Now()
	addr := net.JoinHostPort(ep.Host, ep.Port)

	if ep.Local {
		// Local: TCP dial only, 500ms timeout
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		rpt.Latency = time.Since(start)
		if err != nil {
			rpt.Status = UpstreamStatusOffline
			rpt.Error = err.Error()
			return rpt
		}
		conn.Close()
		rpt.Status = UpstreamStatusReachable
		return rpt
	}

	// Remote: context-aware TCP dial + TLS handshake with certificate validation
	d := &net.Dialer{Timeout: 1500 * time.Millisecond}
	tlsConfig := &tls.Config{
		ServerName: ep.Host,
		MinVersion: tls.VersionTLS12,
	}

	// Use DialContext so cancelled context is respected immediately
	rawConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		rpt.Latency = time.Since(start)
		rpt.Status = UpstreamStatusUnreachable
		rpt.Error = err.Error()
		return rpt
	}

	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		rpt.Latency = time.Since(start)
		rpt.Status = UpstreamStatusUnreachable
		rpt.Error = err.Error()
		return rpt
	}
	rpt.Latency = time.Since(start)

	state := tlsConn.ConnectionState()
	rpt.TLSValid = len(state.VerifiedChains) > 0
	rpt.Status = UpstreamStatusReachable
	tlsConn.Close()
	return rpt
}
