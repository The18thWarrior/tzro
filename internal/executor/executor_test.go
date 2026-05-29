package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
)

func TestCallCloudModel(t *testing.T) {
	// Set mock API Key
	oldConfig := config.Get()
	defer config.Save(&oldConfig)

	cfg := oldConfig
	cfg.CloudAPIKey = "dummy-key"
	cfg.CloudProvider = "openai"
	config.Save(&cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dummy-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "{\"tool_arguments\": {\"query\": \"hello\"}}"
				}
			}]
		}`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Intercept the HTTP client inside callCloudModel/callCloudModelStream by mocking the endpoint in a test
	// But callCloudModel uses hardcoded URLs (https://api.openai.com/v1/chat/completions)
	// How do we redirect? We can dynamically change the CloudProvider check, or we can mock http.DefaultTransport!
	// Yes! Redirecting all HTTP requests to our test server is super powerful.
	oldTransport := http.DefaultTransport
	defer func() {
		http.DefaultTransport = oldTransport
	}()

	http.DefaultTransport = &mockRoundTripper{server.URL, oldTransport}

	ctx := context.Background()
	res, err := inference.CallCloudModel(ctx, "sys", "usr", `{"type":"object"}`)
	if err != nil {
		t.Fatalf("callCloudModel failed: %v", err)
	}

	if res != `{"tool_arguments": {"query": "hello"}}` {
		t.Errorf("unexpected response: %s", res)
	}
}

func TestCallCloudModelStream(t *testing.T) {
	oldConfig := config.Get()
	defer config.Save(&oldConfig)

	cfg := oldConfig
	cfg.CloudAPIKey = "dummy-key"
	cfg.CloudProvider = "openai"
	config.Save(&cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", http.StatusInternalServerError)
			return
		}

		chunks := []string{
			`{"choices":[{"delta":{"content":"foo"}}]}`,
			`{"choices":[{"delta":{"content":"bar"}}]}`,
		}

		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	oldTransport := http.DefaultTransport
	defer func() {
		http.DefaultTransport = oldTransport
	}()
	http.DefaultTransport = &mockRoundTripper{server.URL, oldTransport}

	streamID := "cloud-test"
	meta := inference.StreamMeta{
		StreamID: streamID,
		Source:   "executor",
		TaskID:   "task-1",
		NodeID:   "node-1",
	}

	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.StreamID == streamID
	})
	defer sub.Unsubscribe()

	ctx := context.Background()
	res, err := inference.CallCloudModelStream(ctx, "sys", "usr", `{"type":"object"}`, meta, nil)
	if err != nil {
		t.Fatalf("CallCloudModelStream failed: %v", err)
	}

	if res != "foobar" {
		t.Errorf("expected foobar, got %s", res)
	}

	// Verify chunks published to stream bus
	var tokens []string
	for i := 0; i < 3; i++ {
		select {
		case chunk := <-sub.Ch:
			if chunk.Type == "token" {
				tokens = append(tokens, chunk.Content)
			}
		case <-time.After(100 * time.Millisecond):
		}
	}

	if len(tokens) != 2 || tokens[0] != "foo" || tokens[1] != "bar" {
		t.Errorf("unexpected tokens: %v", tokens)
	}
}

type mockRoundTripper struct {
	targetURL     string
	realTransport http.RoundTripper
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Re-route to test server
	targetReq, err := http.NewRequest(req.Method, m.targetURL, req.Body)
	if err != nil {
		return nil, err
	}
	targetReq.Header = req.Header
	return m.realTransport.RoundTrip(targetReq)
}

func TestDiskBackedCachePersistence(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	rawPayload := `{"records": [{"id": 1, "name": "Test Node", "type": "A"}]}`

	// Write to DB cache
	_, cacheID, err := cache.DefaultStore.Store(context.Background(), rawPayload)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Read and verify cache envelope
	env := cache.DefaultStore.Introspect(context.Background(), cacheID)
	if !strings.Contains(env, cacheID) {
		t.Errorf("expected introspect output to contain cache ID, got: %s", env)
	}

	// Verify file backup fallback path
	cacheFileDir := filepath.Join(".tzro", "cache")
	_ = os.MkdirAll(cacheFileDir, 0755)
	cacheFilePath := filepath.Join(cacheFileDir, cacheID+".json")
	_ = os.WriteFile(cacheFilePath, []byte(rawPayload), 0644)
	defer func() {
		os.Remove(cacheFilePath)
		os.RemoveAll(".tzro/cache")
	}()

	// Query with DB lookup failure (simulate delete or non-existing DB entry)
	missingCacheID := "cache_missing_456"
	missingCacheFilePath := filepath.Join(cacheFileDir, missingCacheID+".json")
	_ = os.WriteFile(missingCacheFilePath, []byte(rawPayload), 0644)
	defer os.Remove(missingCacheFilePath)

	// Since database won't have it, it should fall back to disk file and build envelope dynamically
	fallbackEnv := cache.DefaultStore.Introspect(context.Background(), missingCacheID)
	if !strings.Contains(fallbackEnv, "dataType") || !strings.Contains(fallbackEnv, "cache_") {
		t.Errorf("expected introspect fallback envelope to be generated from file, got: %s", fallbackEnv)
	}
}

func TestExecuteReadCachedData(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	_ = memory.DB.Init()

	rawPayload := `{"records": [
		{"id": 1},
		{"id": 2},
		{"id": 3},
		{"id": 4},
		{"id": 5}
	]}`
	_, cacheID, err := cache.DefaultStore.Store(context.Background(), rawPayload)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	defer func() {
		os.Remove(filepath.Join(".tzro", "cache", cacheID+".json"))
		os.RemoveAll(".tzro/cache")
	}()

	// Page with offset 1 limit 2
	slicedResult := cache.DefaultStore.Read(context.Background(), cacheID, 2, 1)
	var records []map[string]interface{}
	err = json.Unmarshal([]byte(slicedResult), &records)
	if err != nil {
		t.Fatalf("failed to unmarshal sliced result: %v", err)
	}

	if len(records) != 2 {
		t.Errorf("expected limit of 2, got: %d", len(records))
	}
	if records[0]["id"].(float64) != 2 || records[1]["id"].(float64) != 3 {
		t.Errorf("expected records with id 2 and 3, got: %v", records)
	}

	// Edge case pagination
	emptyResult := cache.DefaultStore.Read(context.Background(), cacheID, 10, 100)
	if emptyResult != "[]" {
		t.Errorf("expected empty records '[]', got: %s", emptyResult)
	}
}

func TestExecuteJQQueryFallback(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	_ = memory.DB.Init()

	// Setup payload containing duplicate emails and ages
	rawPayload := `{"records": [
		{"Name": "Alice", "Email": "alice@test.com", "Age": 30.0},
		{"Name": "Bob", "Email": "bob@test.com", "Age": 25.0},
		{"Name": "Alice Dup", "Email": "alice@test.com", "Age": 32.0},
		{"Name": "Diana", "Email": "diana@test.com", "Age": 45.0}
	]}`
	_, cacheID, err := cache.DefaultStore.Store(context.Background(), rawPayload)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	defer func() {
		os.Remove(filepath.Join(".tzro", "cache", cacheID+".json"))
		os.RemoveAll(".tzro/cache")
	}()

	// Test 1: Duplicates grouping filter (basicJQFallback logic)
	dupResult := cache.DefaultStore.Query(context.Background(), cacheID, ".records | group_by(.Email) | .[] | select(length > 1)")
	var dupRecords []map[string]interface{}
	_ = json.Unmarshal([]byte(dupResult), &dupRecords)

	if len(dupRecords) != 2 {
		t.Errorf("expected 2 duplicate email records, got: %d (%s)", len(dupRecords), dupResult)
	}
	if dupRecords[0]["Email"].(string) != "alice@test.com" || dupRecords[1]["Email"].(string) != "alice@test.com" {
		t.Errorf("expected duplicate email records to have alice@test.com, got: %v", dupRecords)
	}

	// Test 2: Select with equality match
	equalityResult := cache.DefaultStore.Query(context.Background(), cacheID, `[.records[] | select(.Name == "Bob")]`)
	var bobRecords []map[string]interface{}
	_ = json.Unmarshal([]byte(equalityResult), &bobRecords)

	if len(bobRecords) != 1 || bobRecords[0]["Name"].(string) != "Bob" {
		t.Errorf("expected only Bob record, got: %s", equalityResult)
	}

	// Test 3: Select with numeric inequality match
	numericResult := cache.DefaultStore.Query(context.Background(), cacheID, `[.records[] | select(.Age > 31)]`)
	var ageRecords []map[string]interface{}
	_ = json.Unmarshal([]byte(numericResult), &ageRecords)

	if len(ageRecords) != 2 {
		t.Errorf("expected 2 records with Age > 31 (Alice Dup:32, Diana:45), got: %d (%v)", len(ageRecords), ageRecords)
	}
}

func TestGBNFSchemas(t *testing.T) {
	_ = tools.Init("")

	// Verify schema returns successfully for all three new cache tools
	schemaIntrospect, err := tools.GetSchema("introspect_cache")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaIntrospect, "cacheId") {
		t.Errorf("expected schema for introspect_cache to contain cacheId, got: %s", schemaIntrospect)
	}

	schemaRead, err := tools.GetSchema("read_cached_data")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaRead, "limit") || !strings.Contains(schemaRead, "offset") {
		t.Errorf("expected schema for read_cached_data to contain limit/offset, got: %s", schemaRead)
	}

	schemaJQ, err := tools.GetSchema("jq_cached_data")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaJQ, "filter") {
		t.Errorf("expected schema for jq_cached_data to contain filter, got: %s", schemaJQ)
	}
}

var _ telemetry.EventPublisher = (*mockEventPublisher)(nil)

type mockEventPublisher struct {
	mu     sync.Mutex
	events []string
}

func (m *mockEventPublisher) PublishEvent(eventType, taskID, nodeID, payload string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, fmt.Sprintf("%s:%s:%s", eventType, taskID, nodeID))
}

func (m *mockEventPublisher) PublishStream(chunk stream.StreamChunk) {}

func TestExecutionEngineTelemetryIsolation(t *testing.T) {
	// Initialize memory DB for test
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_engine.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_engine.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	mockPub := &mockEventPublisher{}
	engine := &ExecutionEngine{
		Publisher: mockPub,
	}

	graph := &compiler.ExecutionGraph{
		TaskID: "task-test-telemetry",
		Nodes: []compiler.GraphNode{
			{
				ID:           "node-1",
				Type:         "action",
				Action:       "slack_message",
				Instructions: "Hello Slack",
				Status:       "pending",
			},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels := [][]string{{"node-1"}}

	_ = tools.Init("")

	ctx := context.Background()
	err := engine.ExecuteGraph(ctx, graph, levels)

	mockPub.mu.Lock()
	events := mockPub.events
	mockPub.mu.Unlock()

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %v (error: %v)", events, err)
	}

	if !strings.Contains(events[0], "task_started:task-test-telemetry:") {
		t.Errorf("expected first event to be task_started, got %s", events[0])
	}

	if !strings.Contains(events[1], "node_started:task-test-telemetry:node-1") {
		t.Errorf("expected second event to be node_started, got %s", events[1])
	}
}
