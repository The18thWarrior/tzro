package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/internal/memory"
)

func TestCacheStore_StoreAndIntrospect(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_cache_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_cache_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	err := memory.DB.Init()
	if err != nil {
		t.Fatalf("failed to initialize test database: %v", err)
	}

	ctx := context.Background()
	rawPayload := `[
		{"Name": "Alice", "Age": 30, "Status": "active"},
		{"Name": "Bob", "Age": 25, "Status": "pending"},
		{"Name": "Charlie", "Age": 35, "Status": "active"}
	]`

	envelopeStr, cacheID, err := DefaultStore.Store(ctx, rawPayload)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if cacheID == "" || !strings.Contains(envelopeStr, "cacheId") {
		t.Errorf("unexpected envelope and cacheID: %s, %s", envelopeStr, cacheID)
	}

	// Introspect from DB
	introspectOutput := DefaultStore.Introspect(ctx, cacheID)
	if introspectOutput != envelopeStr {
		t.Errorf("expected Introspect to return matching stored envelope, got: %s", introspectOutput)
	}

	// Verify backup file path exists and clean it up
	cacheFilePath := filepath.Join(".tzro", "cache", cacheID+".json")
	if _, err := os.Stat(cacheFilePath); os.IsNotExist(err) {
		t.Errorf("expected backup file at %s to exist", cacheFilePath)
	}
	defer func() {
		os.Remove(cacheFilePath)
		os.RemoveAll(".tzro/cache")
	}()

	// Test fallback path: delete from DB, introspect should rebuild from file
	db := memory.DB.RawDB()
	_, err = db.Exec("DELETE FROM disk_cache WHERE cache_id = ?", cacheID)
	if err != nil {
		t.Fatalf("failed to delete cache record from DB: %v", err)
	}

	fallbackOutput := DefaultStore.Introspect(ctx, cacheID)
	if !strings.Contains(fallbackOutput, `"cacheId":`) || !strings.Contains(fallbackOutput, `"dataType": "array"`) {
		t.Errorf("expected introspect fallback output to rebuild envelope from file, got: %s", fallbackOutput)
	}
}

func TestCacheStore_ReadPagination(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_cache_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_cache_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	_ = memory.DB.Init()

	ctx := context.Background()
	rawPayload := `{"records": [
		{"id": 1},
		{"id": 2},
		{"id": 3},
		{"id": 4},
		{"id": 5}
	]}`

	_, cacheID, err := DefaultStore.Store(ctx, rawPayload)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	defer func() {
		os.Remove(filepath.Join(".tzro", "cache", cacheID+".json"))
		os.RemoveAll(".tzro/cache")
	}()

	// Page with offset 1 limit 2
	slicedResult := DefaultStore.Read(ctx, cacheID, 2, 1)
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
	emptyResult := DefaultStore.Read(ctx, cacheID, 10, 100)
	if emptyResult != "[]" {
		t.Errorf("expected empty records '[]', got: %s", emptyResult)
	}
}

func TestProcess_SmallPayload(t *testing.T) {
	// Setup isolated test database to verify no writes
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_cache_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_cache_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	ctx := context.Background()

	// A small HTML-embedded JSON payload (well under 12KB)
	smallPayload := `<div><h1>[{"Name": "Alice", "Status": "active"}]</h1></div>`

	processed, cacheID, err := Process(ctx, smallPayload, "")
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if cacheID != "" {
		t.Errorf("expected cacheID to be empty for small payload, got: %s", cacheID)
	}

	// Small payload should be compacted using HTML strip and tabular TSV
	if !strings.Contains(processed, "Name\tStatus") || !strings.Contains(processed, "Alice\tactive") {
		t.Errorf("expected HTML to be stripped and tabular JSON to be TSV formatted, got: %q", processed)
	}

	// Verify no record exists in database
	db := memory.DB.RawDB()
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM disk_cache").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query db count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 cache records stored in database, got: %d", count)
	}

	// Verify no backup files were created
	cacheFileDir := filepath.Join(".tzro", "cache")
	if _, err := os.Stat(cacheFileDir); !os.IsNotExist(err) {
		t.Errorf("expected backup cache directory to not exist, but it does")
	}
}

func TestProcess_LargePayload(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_cache_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_cache_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	ctx := context.Background()

	// Generate a payload that exceeds 12288 bytes
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 300; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"Index": ` + string(rune('0'+(i%10))) + `, "Text": "Some extremely long repetition that will quickly blow up the byte size beyond twelve kilobytes limit standard threshold of tzro executor caching and compaction layers inside packages."}`)
	}
	sb.WriteString("]")
	largePayload := sb.String()

	if len(largePayload) <= 12288 {
		t.Fatalf("test setup error: payload must be > 12288 bytes, got %d", len(largePayload))
	}

	processed, cacheID, err := Process(ctx, largePayload, "")
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if cacheID == "" {
		t.Errorf("expected cacheID to be generated for large payload, got empty")
	}

	if !strings.Contains(processed, `"cacheId":`) || !strings.Contains(processed, `"dataType": "array"`) {
		t.Errorf("expected returned processed payload to be a JSON Cache Envelope, got: %s", processed)
	}

	// Verify backup file path exists and clean it up
	cacheFilePath := filepath.Join(".tzro", "cache", cacheID+".json")
	if _, err := os.Stat(cacheFilePath); os.IsNotExist(err) {
		t.Errorf("expected backup file at %s to exist for large payload", cacheFilePath)
	}
	defer func() {
		os.Remove(cacheFilePath)
		os.RemoveAll(".tzro/cache")
	}()

	// Verify database record exists
	db := memory.DB.RawDB()
	var rawFromDB string
	err = db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", cacheID).Scan(&rawFromDB)
	if err != nil {
		t.Fatalf("failed to query database cache: %v", err)
	}
	if rawFromDB != largePayload {
		t.Errorf("expected database stored payload to match original, but length matched %d vs %d", len(rawFromDB), len(largePayload))
	}
}

type mockQueryEngine struct {
	called bool
}

func (m *mockQueryEngine) Query(ctx context.Context, rawPayload, jqExpr string) string {
	m.called = true
	return "mocked_query_success"
}

func TestQueryEngine_Seam(t *testing.T) {
	ctx := context.Background()
	mock := &mockQueryEngine{}

	// Swap DefaultQueryEngine with our mock seam
	oldEngine := DefaultQueryEngine
	DefaultQueryEngine = mock
	defer func() {
		DefaultQueryEngine = oldEngine
	}()

	store := &sqlCacheStore{}
	// Note: We don't need a real db because our mock handles it or we bypass query
	res := store.Query(ctx, "any_cache_id", ".records | select(.Age > 30)")
	
	// Because raw payload lookup will check database or file, let's isolate by preparing a file backup or DB
	// Setup isolated test database to allow getRawPayload to return safely
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_cache_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_cache_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()
	_ = memory.DB.Init()

	_, cacheID, err := DefaultStore.Store(ctx, `[{"Age": 35}]`)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	defer func() {
		os.Remove(filepath.Join(".tzro", "cache", cacheID+".json"))
		os.RemoveAll(".tzro/cache")
	}()

	res = store.Query(ctx, cacheID, ".records | select(.Age > 30)")
	if res != "mocked_query_success" {
		t.Errorf("expected mock query engine to be called and return success, got: %s", res)
	}
	if !mock.called {
		t.Errorf("expected mockQueryEngine.Query to have been invoked")
	}
}

func TestQueryEngine_Fallback(t *testing.T) {
	// Directly test default query engine fallback with standard records
	rawPayload := `{"records": [
		{"Name": "Alice", "Email": "alice@test.com", "Age": 30.0},
		{"Name": "Bob", "Email": "bob@test.com", "Age": 25.0},
		{"Name": "Alice Dup", "Email": "alice@test.com", "Age": 32.0},
		{"Name": "Diana", "Email": "diana@test.com", "Age": 45.0}
	]}`

	// Even if jq is absent, the fallback is safely executed:
	engine := &jqQueryEngine{}
	ctx := context.Background()

	// 1. Equality filter fallback test
	resEq := engine.Query(ctx, rawPayload, `[.records[] | select(.Name == "Bob")]`)
	if !strings.Contains(resEq, `"Name": "Bob"`) || strings.Contains(resEq, `"Name": "Alice"`) {
		t.Errorf("expected select name == Bob result, got: %s", resEq)
	}

	// 2. Numeric filter fallback test
	resNum := engine.Query(ctx, rawPayload, `[.records[] | select(.Age > 31)]`)
	if !strings.Contains(resNum, `"Name": "Alice Dup"`) || !strings.Contains(resNum, `"Name": "Diana"`) || strings.Contains(resNum, `"Name": "Bob"`) {
		t.Errorf("expected select Age > 31 records (Alice Dup & Diana), got: %s", resNum)
	}
}

type mockColumnPruner struct {
	prunedCols []string
	err        error
	called     bool
}

func (m *mockColumnPruner) Prune(ctx context.Context, headers []string, stepInstruction string) ([]string, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return m.prunedCols, nil
}

func TestPruneColumns_Success(t *testing.T) {
	mock := &mockColumnPruner{
		prunedCols: []string{"Age", "Status"},
	}

	oldPruner := DefaultColumnPruner
	DefaultColumnPruner = mock
	defer func() {
		DefaultColumnPruner = oldPruner
	}()

	tsvInput := "Name\tAge\tStatus\tNotes\tId\nAlice\t30\tactive\tsome note\t1\nBob\t25\tpending\tanother note\t2"
	ctx := context.Background()

	pruned, err := PruneColumns(ctx, tsvInput, "Show age and status")
	if err != nil {
		t.Fatalf("PruneColumns failed: %v", err)
	}

	if !mock.called {
		t.Errorf("expected mock ColumnPruner to be called")
	}

	lines := strings.Split(pruned, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in pruned TSV, got %d", len(lines))
	}

	expectedHeader := "Name\tAge\tStatus\tId"
	if lines[0] != expectedHeader {
		t.Errorf("expected header %q, got %q", expectedHeader, lines[0])
	}

	expectedRow1 := "Alice\t30\tactive\t1"
	if lines[1] != expectedRow1 {
		t.Errorf("expected row 1 %q, got %q", expectedRow1, lines[1])
	}

	expectedRow2 := "Bob\t25\tpending\t2"
	if lines[2] != expectedRow2 {
		t.Errorf("expected row 2 %q, got %q", expectedRow2, lines[2])
	}
}

func TestPruneColumns_FallbackOnError(t *testing.T) {
	mock := &mockColumnPruner{
		err: fmt.Errorf("simulated LLM error"),
	}

	oldPruner := DefaultColumnPruner
	DefaultColumnPruner = mock
	defer func() {
		DefaultColumnPruner = oldPruner
	}()

	tsvInput := "Name\tAge\tStatus\nAlice\t30\tactive"
	ctx := context.Background()

	pruned, err := PruneColumns(ctx, tsvInput, "Show age")
	if err == nil {
		t.Errorf("expected PruneColumns to return an error, got nil")
	}

	if pruned != tsvInput {
		t.Errorf("expected pruned output to match original input on error, got %q", pruned)
	}
}

func TestProcess_IntegrationPruning(t *testing.T) {
	mock := &mockColumnPruner{
		prunedCols: []string{"Age"},
	}

	oldPruner := DefaultColumnPruner
	DefaultColumnPruner = mock
	defer func() {
		DefaultColumnPruner = oldPruner
	}()

	payload := `[
		{"Name": "Alice", "Age": 30, "Notes": "something to discard"},
		{"Name": "Bob", "Age": 25, "Notes": "discard this too"}
	]`

	ctx := context.Background()
	processed, cacheID, err := Process(ctx, payload, "Extract age details")
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if cacheID != "" {
		t.Errorf("expected cacheID to be empty for small payload, got: %s", cacheID)
	}

	lines := strings.Split(processed, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in processed output, got %d. Content: %q", len(lines), processed)
	}

	// Unique key Name should be preserved, Age selected, Notes dropped.
	expectedHeader := "Age\tName"
	if lines[0] != expectedHeader {
		t.Errorf("expected header %q, got %q", expectedHeader, lines[0])
	}

	expectedRow1 := "30\tAlice"
	if lines[1] != expectedRow1 {
		t.Errorf("expected row 1 %q, got %q", expectedRow1, lines[1])
	}
}
