package executor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	res, err := inference.CallCloudModel(ctx, []inference.InferenceMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "usr"}}, `{"type":"object"}`)
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
	res, err := inference.CallCloudModelStream(ctx, []inference.InferenceMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "usr"}}, `{"type":"object"}`, meta, nil)
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

	// Set TZRO_DIR to temp dir so cache backup files are written to a known location.
	tmpDir := t.TempDir()
	oldTzroDir := os.Getenv("TZRO_DIR")
	os.Setenv("TZRO_DIR", tmpDir)
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		if oldTzroDir != "" {
			os.Setenv("TZRO_DIR", oldTzroDir)
		} else {
			os.Unsetenv("TZRO_DIR")
		}
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

	// Verify file backup fallback path — use TZRO_DIR-resolved paths
	cacheFileDir := config.ResolvePath(filepath.Join(".tzro", "cache"))
	_ = os.MkdirAll(cacheFileDir, 0755)

	// Query with DB lookup failure (simulate delete or non-existing DB entry)
	missingCacheID := "cache_missing_456"
	missingCacheFilePath := filepath.Join(cacheFileDir, missingCacheID+".json")
	_ = os.WriteFile(missingCacheFilePath, []byte(rawPayload), 0644)

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

func TestExecuteSQLQuery(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	_ = memory.DB.Init()

	// Setup in-memory ephemeral query DB for this test
	qdb, err2 := sql.Open("sqlite", ":memory:")
	if err2 != nil {
		t.Fatalf("failed to open in-memory query DB: %v", err2)
	}
	qdb.Exec(`CREATE TABLE IF NOT EXISTS _cache_tables (
		table_name TEXT PRIMARY KEY,
		task_id TEXT,
		created_at INTEGER
	)`)
	cache.SetQueryDBForTesting(qdb)
	defer func() {
		qdb.Close()
		cache.SetQueryDBForTesting(nil)
	}()

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

	// Test 1: Duplicates grouping via SQL GROUP BY + HAVING
	dupResult := cache.DefaultStore.Query(context.Background(), cacheID,
		fmt.Sprintf("SELECT Email, COUNT(*) as cnt FROM %s GROUP BY Email HAVING cnt > 1", cacheID))
	if strings.HasPrefix(dupResult, "Error:") {
		t.Fatalf("SQL duplicate query failed: %s", dupResult)
	}
	if !strings.Contains(dupResult, "alice@test.com") {
		t.Errorf("expected alice@test.com in duplicates result, got: %s", dupResult)
	}

	// Test 2: Select with equality match (WHERE Name = 'Bob')
	equalityResult := cache.DefaultStore.Query(context.Background(), cacheID,
		fmt.Sprintf("SELECT * FROM %s WHERE Name = 'Bob'", cacheID))
	if strings.HasPrefix(equalityResult, "Error:") {
		t.Fatalf("SQL equality query failed: %s", equalityResult)
	}
	var bobRecords []map[string]interface{}
	_ = json.Unmarshal([]byte(equalityResult), &bobRecords)
	if len(bobRecords) != 1 {
		t.Errorf("expected only Bob record, got: %s", equalityResult)
	}

	// Test 3: Select with numeric inequality (WHERE Age > 31)
	numericResult := cache.DefaultStore.Query(context.Background(), cacheID,
		fmt.Sprintf("SELECT * FROM %s WHERE Age > 31", cacheID))
	if strings.HasPrefix(numericResult, "Error:") {
		t.Fatalf("SQL numeric query failed: %s", numericResult)
	}
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

	schemaSQL, err := tools.GetSchema("sql_cached_data")
	if err != nil {
		t.Fatalf("failed to get schema: %v", err)
	}
	if !strings.Contains(schemaSQL, "sql") || !strings.Contains(schemaSQL, "cacheId") {
		t.Errorf("expected schema for sql_cached_data to contain sql and cacheId, got: %s", schemaSQL)
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

type MockTool struct {
	ToolName   string
	ToolSchema string
	ToolCall   func(ctx context.Context, args map[string]interface{}) (string, error)
}

func (m *MockTool) Name() string {
	return m.ToolName
}

func (m *MockTool) Description() string {
	return ""
}

func (m *MockTool) GetSchema() (string, error) {
	return m.ToolSchema, nil
}

func (m *MockTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	return m.ToolCall(ctx, args)
}

func TestKahnLevelBranchPruningAndSkipPropagation(t *testing.T) {
	// Initialize test DB
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_test_branch.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_test_branch.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	// Register a mock tool that returning {"exists": true}
	tools.Register(&MockTool{
		ToolName:   "mock_tool_exists",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"exists": true}`, nil
		},
	})
	defer tools.Unregister("mock_tool_exists")

	// Register a mock tool for downstream nodes
	tools.Register(&MockTool{
		ToolName:   "mock_action",
		ToolSchema: `{"type": "object"}`,
		ToolCall: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"status": "ok"}`, nil
		},
	})
	defer tools.Unregister("mock_action")

	// Set up mock llama server for semantic fallback
	semanticServerCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		semanticServerCalled = true
		bodyBytes, _ := io.ReadAll(r.Body)
		bodyStr := string(bodyBytes)

		satisfied := true
		if strings.Contains(bodyStr, "== false") {
			satisfied = false
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fmt.Appendf(nil, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "{\"satisfied\": %t}"
				}
			}],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 2
			}
		}`, satisfied))
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	defer server.Close()

	// Configure inference GlobalLocalModel to route to our mock server
	oldActivePort := inference.GlobalLocalModel.ActivePort
	oldStatus := inference.GlobalLocalModel.Status
	inference.GlobalLocalModel.ActivePort = port
	inference.GlobalLocalModel.Status = "Active"
	defer func() {
		inference.GlobalLocalModel.ActivePort = oldActivePort
		inference.GlobalLocalModel.Status = oldStatus
	}()

	// Construct graph with conditional branches
	// A: executed successfully, returns {"exists": true}
	// B: branch node, condition "{{nodes.A.output.exists}} == false" -> false, so B skipped
	// C: action node downstream of B -> skip propagated, C skipped
	// D: branch node, condition "{{nodes.A.output.exists}} == true" -> true, so D executed/completed
	// E: action node downstream of D -> executed
	// F: branch node, condition "is A's exists semantic true?" -> falls back to local model -> returns satisfied: true -> executed
	// G: action node downstream of F -> executed
	graph := &compiler.ExecutionGraph{
		TaskID: "task-branch-test",
		Nodes: []compiler.GraphNode{
			{ID: "A", Type: "action", Action: "mock_tool_exists", Instructions: "Run A"},
			{ID: "B", Type: "branch", Condition: "{{nodes.A.output.exists}} == false", Instructions: "Branch B"},
			{ID: "C", Type: "action", Action: "mock_action", Instructions: "Run C"},
			{ID: "D", Type: "branch", Condition: "{{nodes.A.output.exists}} == true", Instructions: "Branch D"},
			{ID: "E", Type: "action", Action: "mock_action", Instructions: "Run E"},
			{ID: "F", Type: "branch", Condition: "semantic check: exists is true", Instructions: "Branch F"},
			{ID: "G", Type: "action", Action: "mock_action", Instructions: "Run G"},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "A", TargetID: "B"},
			{SourceID: "B", TargetID: "C"},
			{SourceID: "A", TargetID: "D"},
			{SourceID: "D", TargetID: "E"},
			{SourceID: "A", TargetID: "F"},
			{SourceID: "F", TargetID: "G"},
		},
		CreatedAt: time.Now().Unix(),
	}

	levels, err := compiler.CompileAndSort(graph)
	if err != nil {
		t.Fatalf("failed to compile and sort: %v", err)
	}

	// Run execution
	engine := &ExecutionEngine{}
	engine.InitRegistry()
	ctx := context.Background()
	err = engine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		t.Fatalf("ExecuteGraph failed: %v", err)
	}

	// Verify statuses in the checkpointer DB
	stateB, ok := memory.DB.GetNodeState("task-branch-test", "B")
	if !ok || stateB.Status != "skipped" {
		t.Errorf("expected node B to be skipped, got: %+v", stateB)
	}

	stateC, ok := memory.DB.GetNodeState("task-branch-test", "C")
	if !ok || stateC.Status != "skipped" {
		t.Errorf("expected node C to be skipped via propagation, got: %+v", stateC)
	}

	stateD, ok := memory.DB.GetNodeState("task-branch-test", "D")
	if !ok || stateD.Status != "completed" {
		t.Errorf("expected node D to be completed, got: %+v", stateD)
	}

	stateE, ok := memory.DB.GetNodeState("task-branch-test", "E")
	if !ok || stateE.Status != "completed" {
		t.Errorf("expected node E to be completed, got: %+v", stateE)
	}

	stateF, ok := memory.DB.GetNodeState("task-branch-test", "F")
	if !ok || stateF.Status != "completed" {
		t.Errorf("expected node F to be completed, got: %+v", stateF)
	}

	stateG, ok := memory.DB.GetNodeState("task-branch-test", "G")
	if !ok || stateG.Status != "completed" {
		t.Errorf("expected node G to be completed, got: %+v", stateG)
	}

	if !semanticServerCalled {
		t.Errorf("expected semantic fallback to call local model server, but it was not called")
	}
}
