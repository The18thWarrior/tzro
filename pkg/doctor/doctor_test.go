package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"tzro/pkg/proxy"
	"tzro/pkg/store"
)

func TestProbeRoutes_OfflineWhenProxyUnreachable(t *testing.T) {
	// Probe a URL where nothing is listening — all intercepted routes should be OFFLINE
	reports := ProbeRoutes("http://127.0.0.1:19999")

	interceptedCount := 0
	for _, r := range reports {
		if !r.Intercepted {
			if r.Status != RouteStatusUnsupported {
				t.Errorf("route %s: expected UNSUPPORTED for unintercepted route, got %s", r.Route, r.Status)
			}
			continue
		}
		interceptedCount++
		if r.Status != RouteStatusOffline {
			t.Errorf("route %s: expected OFFLINE, got %s", r.Route, r.Status)
		}
	}
	if interceptedCount == 0 {
		t.Fatal("expected at least one intercepted route in reports")
	}
}

func TestProbeRoutes_ActiveWithLiveProxy(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	proxySrv := proxy.NewServer(proxy.Config{
		ListenAddr: "127.0.0.1:0",
		Store:      s,
	})

	testSrv := httptest.NewServer(proxySrv.Handler())
	defer testSrv.Close()

	reports := ProbeRoutes(testSrv.URL)

	activeCount := 0
	for _, r := range reports {
		if !r.Intercepted {
			continue
		}
		if r.Status != RouteStatusActive {
			t.Errorf("route %s: expected ACTIVE, got %s (error: %s)", r.Route, r.Status, r.Error)
			continue
		}
		activeCount++
		if len(r.Features) == 0 {
			t.Errorf("route %s: expected features, got none", r.Route)
		}
		if r.Latency <= 0 {
			t.Errorf("route %s: expected positive latency, got %v", r.Route, r.Latency)
		}
	}
	if activeCount == 0 {
		t.Fatal("expected at least one ACTIVE route")
	}
}

func TestProbeRoutes_UninterceptedRoutesUnsupported(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	proxySrv := proxy.NewServer(proxy.Config{
		ListenAddr: "127.0.0.1:0",
		Store:      s,
	})

	testSrv := httptest.NewServer(proxySrv.Handler())
	defer testSrv.Close()

	reports := ProbeRoutes(testSrv.URL)

	unsupportedCount := 0
	for _, r := range reports {
		if r.Intercepted {
			continue
		}
		unsupportedCount++
		if r.Status != RouteStatusUnsupported {
			t.Errorf("route %s: expected UNSUPPORTED, got %s", r.Route, r.Status)
		}
	}
	if unsupportedCount == 0 {
		t.Fatal("expected at least one UNSUPPORTED route")
	}
}

func TestProbeUpstreams_ReachableForLocalListener(t *testing.T) {
	// Start a local TCP listener on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	// Accept connections in background to prevent refused
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	endpoints := []UpstreamEndpoint{
		{Provider: "Test Local", Host: "127.0.0.1", Port: fmt.Sprintf("%d", addr.Port), Local: true},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reports := ProbeUpstreams(ctx, endpoints)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Status != UpstreamStatusReachable {
		t.Errorf("expected REACHABLE, got %s (error: %s)", reports[0].Status, reports[0].Error)
	}
	if reports[0].Latency <= 0 {
		t.Errorf("expected positive latency, got %v", reports[0].Latency)
	}
}

func TestProbeUpstreams_OfflineForRefusedConnection(t *testing.T) {
	// Use a port that's definitely not listening
	endpoints := []UpstreamEndpoint{
		{Provider: "Dead Local", Host: "127.0.0.1", Port: "19998", Local: true},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reports := ProbeUpstreams(ctx, endpoints)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Status != UpstreamStatusOffline {
		t.Errorf("expected OFFLINE, got %s", reports[0].Status)
	}
}

func TestProbeUpstreams_RespectsContextTimeout(t *testing.T) {
	// Cancel the context immediately — all probes should fail fast
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before probing

	endpoints := []UpstreamEndpoint{
		{Provider: "Anthropic API", Host: "api.anthropic.com", Port: "443", Local: false},
	}

	start := time.Now()
	reports := ProbeUpstreams(ctx, endpoints)
	elapsed := time.Since(start)

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Status == UpstreamStatusReachable {
		t.Errorf("expected non-REACHABLE with cancelled context, got REACHABLE")
	}
	if elapsed > 1*time.Second {
		t.Errorf("expected fast failure with cancelled context, took %v", elapsed)
	}
}
