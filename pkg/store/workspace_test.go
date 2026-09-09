package store

import (
	"database/sql"
	"testing"
)

// Slice 1: SymbolEntry has a Workspace field
func TestSymbolEntry_HasWorkspaceField(t *testing.T) {
	e := SymbolEntry{Workspace: "my-workspace"}
	if e.Workspace != "my-workspace" {
		t.Errorf("expected Workspace 'my-workspace', got %q", e.Workspace)
	}
}

// Slice 2: IndexSymbol stores workspace in symbol_index
func TestIndexSymbol_StoresWorkspace(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	err = s.IndexSymbol("ws-a", "Foo", "func", "a.go", 1, "h1")
	if err != nil {
		t.Fatalf("IndexSymbol failed: %v", err)
	}

	// Verify workspace stored via raw SQL
	var ws string
	err = s.db.QueryRow(`SELECT workspace FROM symbol_index WHERE symbol = 'Foo'`).Scan(&ws)
	if err != nil {
		t.Fatalf("raw query failed: %v", err)
	}
	if ws != "ws-a" {
		t.Errorf("expected workspace 'ws-a', got %q", ws)
	}
}

// Slice 3: SearchSymbols returns only symbols from the requested workspace
func TestSearchSymbols_WorkspaceIsolation(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Index same symbol name in two different workspaces
	_ = s.IndexSymbol("ws-a", "Handler", "func", "api.go", 10, "ha")
	_ = s.IndexSymbol("ws-b", "Handler", "func", "api.go", 20, "hb")
	_ = s.IndexSymbol("ws-a", "Middleware", "func", "mid.go", 5, "hm")

	// Search in ws-a should return only ws-a symbols
	results, err := s.SearchSymbols("ws-a", "Handler", 10)
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}
	for _, r := range results {
		if r.Workspace != "ws-a" {
			t.Errorf("expected workspace 'ws-a', got %q for symbol %q", r.Workspace, r.Symbol)
		}
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result in ws-a, got %d", len(results))
	}
	if results[0].Hash != "ha" {
		t.Errorf("expected hash 'ha', got %q", results[0].Hash)
	}

	// Search in ws-b should return only ws-b symbols
	resultsB, err := s.SearchSymbols("ws-b", "Handler", 10)
	if err != nil {
		t.Fatalf("SearchSymbols ws-b failed: %v", err)
	}
	if len(resultsB) != 1 {
		t.Errorf("expected 1 result in ws-b, got %d", len(resultsB))
	}
	if len(resultsB) > 0 && resultsB[0].Workspace != "ws-b" {
		t.Errorf("expected workspace 'ws-b', got %q", resultsB[0].Workspace)
	}
}

// Slice 4: file_index_state compound key (workspace, file_path)
func TestFileIndexState_WorkspaceIsolation(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Two workspaces, same relative path, different hashes
	err = s.UpdateFileIndexState("ws-a", "main.go", 1000, "hash-a")
	if err != nil {
		t.Fatalf("UpdateFileIndexState ws-a failed: %v", err)
	}
	err = s.UpdateFileIndexState("ws-b", "main.go", 2000, "hash-b")
	if err != nil {
		t.Fatalf("UpdateFileIndexState ws-b failed: %v", err)
	}

	// Retrieve ws-a
	modA, hashA, err := s.GetFileIndexState("ws-a", "main.go")
	if err != nil {
		t.Fatalf("GetFileIndexState ws-a failed: %v", err)
	}
	if modA != 1000 || hashA != "hash-a" {
		t.Errorf("ws-a: expected (1000, hash-a), got (%d, %s)", modA, hashA)
	}

	// Retrieve ws-b
	modB, hashB, err := s.GetFileIndexState("ws-b", "main.go")
	if err != nil {
		t.Fatalf("GetFileIndexState ws-b failed: %v", err)
	}
	if modB != 2000 || hashB != "hash-b" {
		t.Errorf("ws-b: expected (2000, hash-b), got (%d, %s)", modB, hashB)
	}
}

// Slice 5: PruneFileSymbols only deletes symbols from the targeted workspace
func TestPruneFileSymbols_WorkspaceIsolation(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Index same file in two workspaces
	_ = s.IndexSymbol("ws-a", "Foo", "func", "shared.go", 1, "ha")
	_ = s.IndexSymbol("ws-b", "Bar", "func", "shared.go", 2, "hb")
	_ = s.UpdateFileIndexState("ws-a", "shared.go", 100, "fha")
	_ = s.UpdateFileIndexState("ws-b", "shared.go", 200, "fhb")

	// Prune ws-a
	err = s.PruneFileSymbols("ws-a", "shared.go")
	if err != nil {
		t.Fatalf("PruneFileSymbols failed: %v", err)
	}

	// ws-a symbols gone
	res, _ := s.SearchSymbols("ws-a", "Foo", 10)
	if len(res) != 0 {
		t.Errorf("expected ws-a symbols pruned, got %d", len(res))
	}

	// ws-b symbols survive
	resB, _ := s.SearchSymbols("ws-b", "Bar", 10)
	if len(resB) != 1 {
		t.Errorf("expected ws-b symbols to survive, got %d", len(resB))
	}

	// ws-a file_index_state gone
	_, _, err = s.GetFileIndexState("ws-a", "shared.go")
	if err == nil {
		t.Errorf("expected ws-a file_index_state to be pruned")
	}

	// ws-b file_index_state survives
	_, _, err = s.GetFileIndexState("ws-b", "shared.go")
	if err != nil {
		t.Errorf("expected ws-b file_index_state to survive: %v", err)
	}
}

// Slice 6: PruneWorkspace removes all data for a workspace
func TestPruneWorkspace(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Populate two workspaces
	_ = s.IndexSymbol("ws-a", "Alpha", "func", "a.go", 1, "h1")
	_ = s.IndexSymbol("ws-a", "Beta", "func", "b.go", 2, "h2")
	_ = s.IndexSymbol("ws-b", "Gamma", "func", "c.go", 3, "h3")
	_ = s.UpdateFileIndexState("ws-a", "a.go", 100, "fa")
	_ = s.UpdateFileIndexState("ws-a", "b.go", 200, "fb")
	_ = s.UpdateFileIndexState("ws-b", "c.go", 300, "fc")

	// Prune ws-a
	err = s.PruneWorkspace("ws-a")
	if err != nil {
		t.Fatalf("PruneWorkspace failed: %v", err)
	}

	// ws-a should be empty
	res, _ := s.SearchSymbols("ws-a", "Alpha", 10)
	if len(res) != 0 {
		t.Errorf("expected ws-a symbols pruned, got %d", len(res))
	}
	_, _, err = s.GetFileIndexState("ws-a", "a.go")
	if err == nil {
		t.Errorf("expected ws-a file state to be pruned")
	}

	// ws-b should survive
	resB, _ := s.SearchSymbols("ws-b", "Gamma", 10)
	if len(resB) != 1 {
		t.Errorf("expected ws-b symbols to survive, got %d", len(resB))
	}
	_, _, err = s.GetFileIndexState("ws-b", "c.go")
	if err != nil {
		t.Errorf("expected ws-b file state to survive: %v", err)
	}
}

// Slice 7: PRAGMA user_version migration from v1 to v2
func TestMigration_V1toV2(t *testing.T) {
	// Create a v1 database manually with old schema (no workspace columns)
	dir := t.TempDir()
	dbPath := dir + "/test-migrate.db"

	db, err := openRawDB(dbPath)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}

	// Create v1 schema (no workspace columns)
	v1Schema := `
	CREATE TABLE IF NOT EXISTS content_blobs (
		hash TEXT PRIMARY KEY,
		file_path TEXT NOT NULL,
		start_line INTEGER NOT NULL,
		end_line INTEGER NOT NULL,
		body TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS symbol_index (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		symbol TEXT NOT NULL,
		kind TEXT NOT NULL,
		file_path TEXT NOT NULL,
		line INTEGER NOT NULL,
		hash TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS file_index_state (
		file_path TEXT PRIMARY KEY,
		mod_time INTEGER NOT NULL,
		hash TEXT NOT NULL,
		indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS cache_sessions (
		session_id TEXT PRIMARY KEY,
		prefix_hash TEXT NOT NULL,
		last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		policy_id TEXT NOT NULL,
		action TEXT NOT NULL,
		pattern TEXT NOT NULL,
		path TEXT NOT NULL,
		redacted_preview TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS artifacts (
		id TEXT PRIMARY KEY,
		hash TEXT NOT NULL,
		source_hash TEXT,
		type TEXT NOT NULL,
		workspace TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		pinned BOOLEAN DEFAULT FALSE,
		size_bytes INTEGER NOT NULL,
		transform_version TEXT,
		is_redacted BOOLEAN DEFAULT FALSE,
		body TEXT NOT NULL,
		last_accessed_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		branch TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		schema_version INTEGER NOT NULL,
		manifest_json TEXT NOT NULL
	);
	`
	if _, err := db.Exec(v1Schema); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}

	// Insert legacy data
	_, _ = db.Exec(`INSERT INTO symbol_index (symbol, kind, file_path, line, hash) VALUES ('LegacyFunc', 'func', 'legacy.go', 10, 'lh1')`)
	_, _ = db.Exec(`INSERT INTO file_index_state (file_path, mod_time, hash) VALUES ('legacy.go', 500, 'lfh1')`)

	// user_version is 0 by default (v1)
	db.Close()

	// Re-open with OpenStore which should trigger migration
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore after migration failed: %v", err)
	}
	defer s.Close()

	// Verify user_version is now 2
	var version int
	_ = s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != 2 {
		t.Errorf("expected PRAGMA user_version = 2, got %d", version)
	}

	// Legacy rows should have workspace = ""
	mod, hash, err := s.GetFileIndexState("", "legacy.go")
	if err != nil {
		t.Fatalf("GetFileIndexState for legacy row failed: %v", err)
	}
	if mod != 500 || hash != "lfh1" {
		t.Errorf("legacy file_index_state mismatch: got (%d, %s)", mod, hash)
	}

	// Legacy symbol should be searchable in workspace ""
	res, err := s.SearchSymbols("", "LegacyFunc", 10)
	if err != nil {
		t.Fatalf("SearchSymbols for legacy failed: %v", err)
	}
	if len(res) != 1 || res[0].Symbol != "LegacyFunc" {
		t.Errorf("expected legacy symbol 'LegacyFunc', got %+v", res)
	}
}

// openRawDB opens a raw SQLite DB without tzro schema init (for migration testing).
func openRawDB(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}
