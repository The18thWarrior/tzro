package observer

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"tzro/internal/memory"
	"tzro/internal/proactivity"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
)

func TestObserverAgentIsolationAndDebounce(t *testing.T) {
	// Create two distinct telemetry managers and observer agents
	mgr1 := telemetry.NewTelemetryManager()
	mgr2 := telemetry.NewTelemetryManager()

	agent1 := NewObserverAgent()
	agent1.SetTelemetryManager(mgr1)

	agent2 := NewObserverAgent()
	agent2.SetTelemetryManager(mgr2)

	// Short debounce interval and threshold for rapid, isolated testing
	debounceInterval := 50 * time.Millisecond
	agent1.SetDebounceInterval(debounceInterval)
	agent1.SetAuditThreshold(3)

	agent2.SetDebounceInterval(debounceInterval)
	agent2.SetAuditThreshold(3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start both agents' background loops
	agent1.Start(ctx)
	agent2.Start(ctx)

	// Subscribe to each telemetry manager
	sub1 := mgr1.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Type == "observer_audit"
	})
	defer sub1.Unsubscribe()

	sub2 := mgr2.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Type == "observer_audit"
	})
	defer sub2.Unsubscribe()

	// 1. Verify parallel isolation by sending events concurrently to both managers.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		// Send 3 events to mgr1 to trigger immediate threshold audit (3 events threshold)
		mgr1.PublishEvent("task_started", "task-A", "node-1", "Start A")
		mgr1.PublishEvent("node_completed", "task-A", "node-1", "Done node 1")
		mgr1.PublishEvent("node_completed", "task-A", "node-2", "Done node 2")
	}()

	go func() {
		defer wg.Done()
		// Send only 1 event to mgr2. It should NOT trigger the threshold audit.
		// It will eventually trigger the inactivity debounce audit after debounceInterval.
		mgr2.PublishEvent("task_started", "task-B", "node-1", "Start B")
	}()

	wg.Wait()

	// Check threshold audit for mgr1 / sub1
	select {
	case chunk := <-sub1.Ch:
		if chunk.Type != "observer_audit" {
			t.Errorf("sub1: expected observer_audit, got %s", chunk.Type)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("sub1 timed out waiting for threshold audit")
	}

	// Verify that sub2 has not received any threshold audit immediately
	select {
	case chunk := <-sub2.Ch:
		if chunk.Content == "" {
			t.Error("sub2 received empty chunk")
		}
	default:
		// Correct! Inactivity debounce has not expired yet
	}

	// Wait for the inactivity debounce threshold on mgr2 / sub2
	select {
	case chunk := <-sub2.Ch:
		if chunk.Type != "observer_audit" {
			t.Errorf("sub2: expected observer_audit, got %s", chunk.Type)
		}
	case <-time.After(300 * time.Millisecond):
		t.Error("sub2 timed out waiting for inactivity debounce audit")
	}

	// Verify that sub1 has NO more audits (no pollution from mgr2)
	select {
	case chunk := <-sub1.Ch:
		t.Errorf("sub1 received unexpected polluted chunk: %+v", chunk)
	default:
		// Correct! Isolation verified!
	}
}

func TestObserverAgentStopAndCleanup(t *testing.T) {
	mgr := telemetry.NewTelemetryManager()
	agent := NewObserverAgent()
	agent.SetTelemetryManager(mgr)
	agent.SetDebounceInterval(10 * time.Millisecond)

	ctx := context.Background()
	agent.Start(ctx)

	// Stop agent
	agent.Stop()

	// Send an event
	mgr.PublishEvent("task_started", "task-C", "node-1", "Start C")

	// Wait to ensure no background panic or thread leak
	time.Sleep(20 * time.Millisecond)
}

type MockLLMClient struct {
	mu           sync.Mutex
	calledCount  int
	calledPrompt string
	returnMem    string
	returnKG     string
}

func (m *MockLLMClient) CallModel(ctx context.Context, systemPrompt, userPrompt string, jsonSchema string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calledCount++
	m.calledPrompt = systemPrompt

	if strings.Contains(systemPrompt, "self-improvement reflection agent") {
		return m.returnMem, nil
	}
	if strings.Contains(systemPrompt, "relational knowledge graph extraction agent") {
		return m.returnKG, nil
	}
	return "{}", nil
}

func TestObserverAgentReflectionAndKGExtraction(t *testing.T) {
	// Initialize test database
	memory.DB.SetDBPathForTesting("test_observer_reflection.db")
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer func() {
		_ = memory.DB.Close()
		_ = os.Remove("test_observer_reflection.db")
	}()

	mgr := telemetry.NewTelemetryManager()
	agent := NewObserverAgent()
	agent.SetTelemetryManager(mgr)
	agent.SetDebounceInterval(10 * time.Millisecond)
	agent.SetAuditThreshold(1) // Trigger audit instantly on 1 event

	// Mock outputs
	mockReturnMem := `{
		"memories": [
			{
				"type": "preference",
				"content": "User prefers Acme account over Globex",
				"context": "Alice corrections info",
				"confidence": 0.95
			}
		]
	}`

	mockReturnKG := `{
		"nodes": [
			{
				"id": "con_alice",
				"type": "contact",
				"name": "Alice Smith",
				"metadata": {"title": "Manager"}
			},
			{
				"id": "acc_acme",
				"type": "account",
				"name": "Acme Corp",
				"metadata": {"industry": "Software"}
			}
		],
		"edges": [
			{
				"type": "belongs_to",
				"sourceId": "con_alice",
				"targetId": "acc_acme",
				"metadata": {"role": "Primary"}
			}
		]
	}`

	mockClient := &MockLLMClient{
		returnMem: mockReturnMem,
		returnKG:  mockReturnKG,
	}
	agent.SetLLMClient(mockClient)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent.Start(ctx)

	// Subscribe to observer audits
	sub := mgr.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "observer" && chunk.Type == "observer_audit"
	})
	defer sub.Unsubscribe()

	// Publish a context-rich event to trigger immediate audit
	mgr.PublishEvent("node_completed", "task-reflection-1", "node-ref-1", "User says Alice belongs to Acme, not Globex")

	// Wait for audit and reflection to finish in background goroutine
	select {
	case chunk := <-sub.Ch:
		if !strings.Contains(chunk.Content, "Completed automated verification") {
			t.Errorf("Unexpected verification audit content: %s", chunk.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timed out waiting for telemetry audit chunk")
	}

	// Give background reflection goroutine a moment to complete
	time.Sleep(100 * time.Millisecond)

	mockClient.mu.Lock()
	calls := mockClient.calledCount
	mockClient.mu.Unlock()

	if calls < 2 {
		t.Errorf("Expected LLM client to be called at least twice (for memories and KG), got %d calls", calls)
	}

	// Verify SQLite has the auto-synthesized memories
	mems := memory.DB.GetMemories()
	var foundPref bool
	for _, m := range mems {
		if m.Type == "preference" && strings.Contains(m.Content, "Acme") {
			foundPref = true
			if m.Source != "auto_reflection" {
				t.Errorf("Expected source to be auto_reflection, got %s", m.Source)
			}
		}
	}
	if !foundPref {
		t.Error("Did not find synthesized preference memory in SQLite")
	}

	// Verify SQLite has the extracted nodes and edges in the Knowledge Graph
	nodes := memory.DB.GetNodes()
	if _, exists := nodes["con_alice"]; !exists {
		t.Error("Did not find node con_alice in SQLite Knowledge Graph")
	}
	if _, exists := nodes["acc_acme"]; !exists {
		t.Error("Did not find node acc_acme in SQLite Knowledge Graph")
	}

	edges := memory.DB.GetEdges()
	var foundEdge bool
	for _, e := range edges {
		if e.SourceID == "con_alice" && e.TargetID == "acc_acme" && e.EdgeType == "belongs_to" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Error("Did not find edge con_alice -> acc_acme in SQLite Knowledge Graph")
	}
}

func TestObserverDefersReflectionDuringForeground(t *testing.T) {
	// Initialize test database
	memory.DB.SetDBPathForTesting("test_observer_defer.db")
	if err := memory.DB.Init(); err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer func() {
		_ = memory.DB.Close()
		_ = os.Remove("test_observer_defer.db")
	}()

	// Clear any stale state from other tests
	proactivity.ClearActiveTasks()
	proactivity.ClearCallbacks()

	mgr := telemetry.NewTelemetryManager()
	agent := NewObserverAgent()
	agent.SetTelemetryManager(mgr)
	agent.SetDebounceInterval(10 * time.Millisecond)
	agent.SetAuditThreshold(1)

	mockClient := &MockLLMClient{
		returnMem: `{"memories": []}`,
		returnKG:  `{"nodes": [], "edges": []}`,
	}
	agent.SetLLMClient(mockClient)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agent.Start(ctx)

	// Register a foreground task BEFORE publishing events
	proactivity.RegisterActiveUserTask("test_foreground_task")

	// Subscribe to observer audits
	sub := mgr.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.Source == "observer" && chunk.Type == "observer_audit"
	})
	defer sub.Unsubscribe()

	// Publish an event — this will trigger audit, but LLM should be deferred
	mgr.PublishEvent("node_completed", "task-defer-1", "node-1", "Some event payload")

	// Wait for audit to fire (deterministic checks still run, audit notification still sent)
	select {
	case <-sub.Ch:
		// Audit notification received — good
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timed out waiting for audit notification")
	}

	// Give goroutine time to NOT call LLM
	time.Sleep(50 * time.Millisecond)

	mockClient.mu.Lock()
	callsDuringForeground := mockClient.calledCount
	mockClient.mu.Unlock()

	if callsDuringForeground != 0 {
		t.Errorf("Expected 0 LLM calls during foreground activity, got %d", callsDuringForeground)
	}

	// Verify events are buffered, not lost
	agent.mu.RLock()
	deferredCount := len(agent.deferredEvents)
	agent.mu.RUnlock()

	if deferredCount == 0 {
		t.Error("Expected deferred events to be buffered, but buffer is empty")
	}

	// Clear the foreground — this triggers resume callbacks which flush deferred events
	proactivity.DeregisterActiveUserTask("test_foreground_task")

	// Give the flushed goroutine time to execute
	time.Sleep(100 * time.Millisecond)

	mockClient.mu.Lock()
	callsAfterResume := mockClient.calledCount
	mockClient.mu.Unlock()

	if callsAfterResume < 2 {
		t.Errorf("Expected at least 2 LLM calls after resume (memory + KG), got %d", callsAfterResume)
	}

	// Verify deferred buffer is now empty
	agent.mu.RLock()
	deferredAfter := len(agent.deferredEvents)
	agent.mu.RUnlock()

	if deferredAfter != 0 {
		t.Errorf("Expected deferred events to be flushed, but %d batches remain", deferredAfter)
	}
}
