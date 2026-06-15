package agent

import (
	"context"
	"os"
	"sync"
	"testing"

	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

// mockLLMClient implements LLMClient for testing.
type mockLLMClient struct {
	mu      sync.Mutex
	calls   int
	lastSys string
	resp    string
	err     error
}

func (m *mockLLMClient) CallModel(_ context.Context, sys, user, schema string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastSys = sys
	return m.resp, m.err
}

func TestBackgroundAgentName(t *testing.T) {
	ba := NewBackgroundAgent("test-agent")
	if ba.AgentName() != "test-agent" {
		t.Errorf("expected 'test-agent', got '%s'", ba.AgentName())
	}
}

func TestBackgroundAgentLLMClientWiring(t *testing.T) {
	ba := NewBackgroundAgent("test")
	if ba.GetLLMClient() != nil {
		t.Error("expected nil LLM client initially")
	}

	mock := &mockLLMClient{resp: "ok"}
	ba.SetLLMClient(mock)
	if ba.GetLLMClient() == nil {
		t.Error("expected non-nil LLM client after set")
	}
}

func TestBackgroundAgentTelemetrySubscription(t *testing.T) {
	tm := telemetry.NewTelemetryManager()
	ba := NewBackgroundAgent("test")
	ba.SetTelemetryManager(tm)

	sub := ba.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "test"
	})
	if sub == nil {
		t.Fatal("expected non-nil subscription")
	}

	// Publish and verify receipt
	tm.PublishStream(stream.StreamChunk{Source: "test", Type: "ping", Content: "hello"})

	select {
	case chunk := <-sub.Ch:
		if chunk.Content != "hello" {
			t.Errorf("expected 'hello', got '%s'", chunk.Content)
		}
	default:
		t.Error("expected to receive chunk from subscription")
	}

	ba.Unsubscribe()
}

func TestBackgroundAgentLifecycle(t *testing.T) {
	ba := NewBackgroundAgent("test")
	if ba.IsRunning() {
		t.Error("expected not running initially")
	}

	_, cancel := context.WithCancel(context.Background())
	ba.SetCancel(cancel)
	if !ba.IsRunning() {
		t.Error("expected running after SetCancel")
	}

	ba.Cancel()
	if ba.IsRunning() {
		t.Error("expected not running after Cancel")
	}
}

func TestBackgroundAgentAlertProduction(t *testing.T) {
	// Initialize memory DB for notification storage
	memory.DB.SetDBPathForTesting("test_agent_alert.db")
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init memory DB: %v", err)
	}
	defer func() {
		_ = memory.DB.Close()
		_ = os.Remove("test_agent_alert.db")
	}()

	tm := telemetry.NewTelemetryManager()
	ba := NewBackgroundAgent("sentinel")
	ba.SetTelemetryManager(tm)

	// Subscribe to capture published chunks
	sub := tm.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "sentinel"
	})
	defer sub.Unsubscribe()

	err := ba.ProduceAlert(context.Background(), "suggestion", "Test Alert", "This is a test alert", "target123")
	if err != nil {
		t.Fatalf("unexpected error producing alert: %v", err)
	}

	// Verify notification was stored
	notifs, err := memory.DB.GetNotifications("")
	if err != nil {
		t.Fatalf("failed to get notifications: %v", err)
	}
	found := false
	for _, n := range notifs {
		if n.Source == "sentinel" && n.Title == "Test Alert" && n.TargetID == "target123" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected sentinel alert in notifications")
	}

	// Verify StreamBus chunk was published
	select {
	case chunk := <-sub.Ch:
		if chunk.Type != "sentinel_alert" {
			t.Errorf("expected type 'sentinel_alert', got '%s'", chunk.Type)
		}
	default:
		t.Error("expected chunk on StreamBus")
	}
}

func TestBackgroundAgentDeduplication(t *testing.T) {
	memory.DB.SetDBPathForTesting("test_agent_dedup.db")
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init memory DB: %v", err)
	}
	defer func() {
		_ = memory.DB.Close()
		_ = os.Remove("test_agent_dedup.db")
	}()

	ba := NewBackgroundAgent("sentinel")
	fp := Fingerprint("mem_123", "auth/middleware.go")

	// No active alert initially
	if ba.HasActiveAlert(fp) {
		t.Error("expected no active alert initially")
	}

	// Produce an alert
	_ = ba.ProduceAlert(context.Background(), "suggestion", "Test", "msg", fp)

	// Now should have active alert
	if !ba.HasActiveAlert(fp) {
		t.Error("expected active alert after production")
	}
}

func TestFingerprint(t *testing.T) {
	fp1 := Fingerprint("mem_123", "auth/middleware.go")
	fp2 := Fingerprint("mem_123", "auth/middleware.go")
	fp3 := Fingerprint("mem_456", "auth/middleware.go")

	if fp1 != fp2 {
		t.Error("same inputs should produce same fingerprint")
	}
	if fp1 == fp3 {
		t.Error("different inputs should produce different fingerprints")
	}
	if len(fp1) != 64 {
		t.Errorf("expected 64-char hex, got %d chars", len(fp1))
	}
}
