package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tzro/pkg/dlp"

	_ "modernc.org/sqlite"
)

const (
	// DefaultWorkspaceMaxBytes limits total artifact storage to 100 MB per workspace.
	DefaultWorkspaceMaxBytes int64 = 100 * 1024 * 1024

	// DefaultWorkspaceMaxCount limits total artifacts to 1,000 items per workspace.
	DefaultWorkspaceMaxCount int = 1000

	// MaxSingleArtifactBytes rejects single payloads exceeding 20 MB before SQLite insertion.
	MaxSingleArtifactBytes int64 = 20 * 1024 * 1024

	// DefaultContextTraceMaxBytes limits total trace storage to 50 MB per workspace.
	DefaultContextTraceMaxBytes int64 = 50 * 1024 * 1024
)

// ContextTrace represents an always-on six-stage context assembly diagnostic trace.
type ContextTrace struct {
	ID         string    `json:"id"`
	Workspace  string    `json:"workspace"`
	CreatedAt  time.Time `json:"created_at"`
	Query      string    `json:"query"`
	Budget     int       `json:"budget"`
	ConfigJSON string    `json:"config_json"`
	TraceJSON  string    `json:"trace_json"`
	SizeBytes  int64     `json:"size_bytes"`
}

// ContextTraceMeta provides summary metadata for a context trace without the large JSON payload.
type ContextTraceMeta struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	CreatedAt time.Time `json:"created_at"`
	Query     string    `json:"query"`
	Budget    int       `json:"budget"`
	SizeBytes int64     `json:"size_bytes"`
}

// Blob represents a content-addressed code or text segment.
type Blob struct {
	Hash      string    `json:"hash"`
	FilePath  string    `json:"file_path"`
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// SymbolEntry represents a symbol indexed in SQLite FTS5.
type SymbolEntry struct {
	Workspace string `json:"workspace,omitempty"`
	Symbol    string `json:"symbol"`
	Kind      string `json:"kind"`
	FilePath  string `json:"file_path"`
	Line      int    `json:"line"`
	Hash      string `json:"hash"`
}

// Artifact represents an immutable stored tool output, log, or query result.
type Artifact struct {
	ID               string    `json:"id"`
	Hash             string    `json:"hash"`
	SourceHash       string    `json:"source_hash,omitempty"`
	Type             string    `json:"type"` // log, tabular, text, code
	Workspace        string    `json:"workspace"`
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
	Pinned           bool      `json:"pinned"`
	SizeBytes        int64     `json:"size_bytes"`
	TransformVersion string    `json:"transform_version,omitempty"`
	IsRedacted       bool      `json:"is_redacted"`
	Body             string    `json:"body,omitempty"`
	LastAccessedAt   time.Time `json:"last_accessed_at"`
}

// Store handles local SQLite content-addressed storage and FTS5 search.
type Store struct {
	db            *sql.DB
	mu            sync.RWMutex
	path          string
	hasFTS5       bool
	lastSweepTime time.Time
	policy        *dlp.PolicyEngine
}

// SetPolicy sets the privacy policy engine for the store.
func (s *Store) SetPolicy(pe *dlp.PolicyEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = pe
}

// OpenStore opens or initializes the SQLite store at dbPath.
func OpenStore(dbPath string) (*Store, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db dir: %w", err)
		}
	}

	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", dbPath)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	s := &Store{
		db:   db,
		path: dbPath,
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return s, nil
}

func (s *Store) initSchema() error {
	// Check for v1→v2 migration need (workspace isolation)
	var userVersion int
	_ = s.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion)

	if userVersion < 2 {
		// Check if legacy tables exist by probing for workspace column
		var hasWorkspaceCol bool
		rows, err := s.db.Query(`PRAGMA table_info(symbol_index)`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var cid int
				var name, ctype string
				var notnull, pk int
				var dflt sql.NullString
				if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
					if name == "workspace" {
						hasWorkspaceCol = true
					}
				}
			}
		}

		// Only migrate if legacy tables exist without workspace column
		if !hasWorkspaceCol {
			// Check if the old tables actually have data (they exist from a prior version)
			var tableCount int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbol_index'`).Scan(&tableCount)

			if tableCount > 0 {
				tx, err := s.db.Begin()
				if err != nil {
					return fmt.Errorf("migration begin: %w", err)
				}
				defer tx.Rollback()

				// Drop old FTS5 virtual table and triggers (they reference old schema)
				tx.Exec(`DROP TRIGGER IF EXISTS symbol_ai`)
				tx.Exec(`DROP TRIGGER IF EXISTS symbol_ad`)
				tx.Exec(`DROP TRIGGER IF EXISTS symbol_au`)
				tx.Exec(`DROP TABLE IF EXISTS symbol_fts`)

				// Migrate symbol_index
				_, err = tx.Exec(`CREATE TABLE symbol_index_v2 (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					workspace TEXT NOT NULL,
					symbol TEXT NOT NULL,
					kind TEXT NOT NULL,
					file_path TEXT NOT NULL,
					line INTEGER NOT NULL,
					hash TEXT NOT NULL
				)`)
				if err != nil {
					return fmt.Errorf("migrate symbol_index_v2: %w", err)
				}
				_, err = tx.Exec(`INSERT INTO symbol_index_v2 (id, workspace, symbol, kind, file_path, line, hash)
					SELECT id, '', symbol, kind, file_path, line, hash FROM symbol_index`)
				if err != nil {
					return fmt.Errorf("copy symbol_index: %w", err)
				}
				tx.Exec(`DROP TABLE symbol_index`)
				tx.Exec(`ALTER TABLE symbol_index_v2 RENAME TO symbol_index`)

				// Migrate file_index_state
				_, err = tx.Exec(`CREATE TABLE file_index_state_v2 (
					workspace TEXT NOT NULL,
					file_path TEXT NOT NULL,
					mod_time INTEGER NOT NULL,
					hash TEXT NOT NULL,
					indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
					PRIMARY KEY (workspace, file_path)
				)`)
				if err != nil {
					return fmt.Errorf("migrate file_index_state_v2: %w", err)
				}
				_, err = tx.Exec(`INSERT INTO file_index_state_v2 (workspace, file_path, mod_time, hash, indexed_at)
					SELECT '', file_path, mod_time, hash, indexed_at FROM file_index_state`)
				if err != nil {
					return fmt.Errorf("copy file_index_state: %w", err)
				}
				tx.Exec(`DROP TABLE file_index_state`)
				tx.Exec(`ALTER TABLE file_index_state_v2 RENAME TO file_index_state`)

				// Set version
				tx.Exec(`PRAGMA user_version = 2`)

				if err := tx.Commit(); err != nil {
					return fmt.Errorf("migration commit: %w", err)
				}
			}
		}
	}

	// Set user_version for fresh databases
	if userVersion == 0 {
		_, _ = s.db.Exec(`PRAGMA user_version = 2`)
	}

	baseSchema := `
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
		workspace TEXT NOT NULL,
		symbol TEXT NOT NULL,
		kind TEXT NOT NULL,
		file_path TEXT NOT NULL,
		line INTEGER NOT NULL,
		hash TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_symbol_ws_file ON symbol_index(workspace, file_path);
	CREATE INDEX IF NOT EXISTS idx_symbol_ws_symbol ON symbol_index(workspace, symbol);

	CREATE TABLE IF NOT EXISTS cache_sessions (
		session_id TEXT PRIMARY KEY,
		prefix_hash TEXT NOT NULL,
		last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS file_index_state (
		workspace TEXT NOT NULL,
		file_path TEXT NOT NULL,
		mod_time INTEGER NOT NULL,
		hash TEXT NOT NULL,
		indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (workspace, file_path)
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

	CREATE INDEX IF NOT EXISTS idx_artifacts_ws ON artifacts(workspace);
	CREATE INDEX IF NOT EXISTS idx_artifacts_hash ON artifacts(hash);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		branch TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		schema_version INTEGER NOT NULL,
		manifest_json TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_ws ON sessions(workspace);

	CREATE TABLE IF NOT EXISTS context_traces (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		query TEXT,
		budget INTEGER,
		config_json TEXT NOT NULL,
		trace_json TEXT NOT NULL,
		size_bytes INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_context_traces_ws_created ON context_traces(workspace, created_at);

	CREATE TABLE IF NOT EXISTS trace_outcomes (
		trace_id TEXT PRIMARY KEY,
		outcome_json TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	`
	if _, err := s.db.Exec(baseSchema); err != nil {
		return err
	}

	// Schema migration for existing artifacts table if last_accessed_at is missing from an older database
	_, _ = s.db.Exec(`ALTER TABLE artifacts ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_artifacts_accessed ON artifacts(workspace, last_accessed_at)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_artifacts_expires ON artifacts(pinned, expires_at)`)

	// Attempt to create FTS5 virtual table and triggers; fall back gracefully if unsupported
	ftsSchema := `
	CREATE VIRTUAL TABLE IF NOT EXISTS symbol_fts USING fts5(
		workspace UNINDEXED,
		symbol,
		kind,
		file_path,
		line UNINDEXED,
		hash UNINDEXED,
		content='symbol_index',
		content_rowid='id'
	);

	CREATE TRIGGER IF NOT EXISTS symbol_ai AFTER INSERT ON symbol_index BEGIN
		INSERT INTO symbol_fts(rowid, workspace, symbol, kind, file_path, line, hash)
		VALUES (new.id, new.workspace, new.symbol, new.kind, new.file_path, new.line, new.hash);
	END;

	CREATE TRIGGER IF NOT EXISTS symbol_ad AFTER DELETE ON symbol_index BEGIN
		INSERT INTO symbol_fts(symbol_fts, rowid, workspace, symbol, kind, file_path, line, hash)
		VALUES ('delete', old.id, old.workspace, old.symbol, old.kind, old.file_path, old.line, old.hash);
	END;

	CREATE TRIGGER IF NOT EXISTS symbol_au AFTER UPDATE ON symbol_index BEGIN
		INSERT INTO symbol_fts(symbol_fts, rowid, workspace, symbol, kind, file_path, line, hash)
		VALUES ('delete', old.id, old.workspace, old.symbol, old.kind, old.file_path, old.line, old.hash);
		INSERT INTO symbol_fts(rowid, workspace, symbol, kind, file_path, line, hash)
		VALUES (new.id, new.workspace, new.symbol, new.kind, new.file_path, new.line, new.hash);
	END;
	`
	if _, err := s.db.Exec(ftsSchema); err != nil {
		// Non-fatal: mark FTS5 as unavailable, falling back to standard lexical LIKE queries
		s.hasFTS5 = false
	} else {
		s.hasFTS5 = true
	}

	// Schema migration for existing artifacts table if last_accessed_at is missing
	_, _ = s.db.Exec(`ALTER TABLE artifacts ADD COLUMN last_accessed_at INTEGER NOT NULL DEFAULT 0`)

	return nil
}

// PutSession records or updates a versioned session manifest.
func (s *Store) PutSession(id, workspace, branch string, schemaVersion int, manifestJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().UnixNano()
	query := `
	INSERT INTO sessions (id, workspace, branch, created_at, schema_version, manifest_json)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		workspace=excluded.workspace,
		branch=excluded.branch,
		schema_version=excluded.schema_version,
		manifest_json=excluded.manifest_json;
	`
	_, err := s.db.Exec(query, id, workspace, branch, now, schemaVersion, manifestJSON)
	return err
}

// HasFTS5 returns true if the SQLite engine successfully initialized the FTS5 virtual table.
func (s *Store) HasFTS5() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasFTS5
}

// GetSession retrieves a stored session manifest by ID, verifying workspace isolation.
func (s *Store) GetSession(id, workspace string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT manifest_json FROM sessions WHERE id = ?`
	var args []any
	args = append(args, id)
	if workspace != "" {
		query += " AND workspace = ?"
		args = append(args, workspace)
	}

	var manifestJSON string
	err := s.db.QueryRow(query, args...).Scan(&manifestJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("session %q not found or access denied by workspace isolation", id)
		}
		return "", err
	}

	return manifestJSON, nil
}

// GetLatestSessionByBranch returns the most recent session manifest for a specific branch.
func (s *Store) GetLatestSessionByBranch(workspace, branch string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT manifest_json FROM sessions WHERE workspace = ? AND branch = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`
	var manifestJSON string
	err := s.db.QueryRow(query, workspace, branch).Scan(&manifestJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no session found for workspace %q on branch %q", workspace, branch)
		}
		return "", err
	}
	return manifestJSON, nil
}

// GetLatestSession returns the most recent session manifest for a workspace regardless of branch.
func (s *Store) GetLatestSession(workspace string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT manifest_json FROM sessions WHERE workspace = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`
	var manifestJSON string
	err := s.db.QueryRow(query, workspace).Scan(&manifestJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no session found for workspace %q", workspace)
		}
		return "", err
	}
	return manifestJSON, nil
}

// ListSessions returns all session manifests for a workspace ordered by created_at DESC.
func (s *Store) ListSessions(workspace string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT manifest_json FROM sessions WHERE workspace = ? ORDER BY created_at DESC, rowid DESC`
	rows, err := s.db.Query(query, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		sessions = append(sessions, m)
	}
	return sessions, rows.Err()
}

// PutContextTrace records a context assembly trace and enforces the 50 MB workspace quota.
func (s *Store) PutContextTrace(trace *ContextTrace) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now().UTC()
	}
	if trace.SizeBytes == 0 {
		trace.SizeBytes = int64(len(trace.ConfigJSON) + len(trace.TraceJSON) + len(trace.Query))
	}

	query := `
	INSERT INTO context_traces (id, workspace, created_at, query, budget, config_json, trace_json, size_bytes)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		workspace=excluded.workspace,
		created_at=excluded.created_at,
		query=excluded.query,
		budget=excluded.budget,
		config_json=excluded.config_json,
		trace_json=excluded.trace_json,
		size_bytes=excluded.size_bytes;
	`
	_, err := s.db.Exec(query,
		trace.ID,
		trace.Workspace,
		trace.CreatedAt.UnixNano(),
		trace.Query,
		trace.Budget,
		trace.ConfigJSON,
		trace.TraceJSON,
		trace.SizeBytes,
	)
	if err != nil {
		return fmt.Errorf("failed to put context trace: %w", err)
	}

	// Enforce 50 MB workspace LRU quota
	if trace.Workspace != "" {
		s.evictContextTracesLRULocked(trace.Workspace, DefaultContextTraceMaxBytes)
	}

	return nil
}

// GetContextTrace retrieves a context trace by ID, strictly respecting workspace isolation.
func (s *Store) GetContextTrace(id, workspace string) (*ContextTrace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, workspace, created_at, query, budget, config_json, trace_json, size_bytes
	FROM context_traces
	WHERE id = ?
	`
	var args []any
	args = append(args, id)
	if workspace != "" {
		query += " AND workspace = ?"
		args = append(args, workspace)
	}

	row := s.db.QueryRow(query, args...)
	var tr ContextTrace
	var createdNano int64
	var q sql.NullString
	var b sql.NullInt64

	err := row.Scan(
		&tr.ID,
		&tr.Workspace,
		&createdNano,
		&q,
		&b,
		&tr.ConfigJSON,
		&tr.TraceJSON,
		&tr.SizeBytes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("context trace %q not found or access denied by workspace isolation", id)
		}
		return nil, err
	}

	if q.Valid {
		tr.Query = q.String
	}
	if b.Valid {
		tr.Budget = int(b.Int64)
	}
	tr.CreatedAt = time.Unix(0, createdNano).UTC()

	return &tr, nil
}

// ListContextTraces returns recent trace summaries for a workspace.
func (s *Store) ListContextTraces(workspace string, limit int) ([]ContextTraceMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	query := `
	SELECT id, workspace, created_at, query, budget, size_bytes
	FROM context_traces
	WHERE workspace = ?
	ORDER BY created_at DESC
	LIMIT ?
	`
	rows, err := s.db.Query(query, workspace, limit)
	if err != nil {
		return nil, fmt.Errorf("list context traces: %w", err)
	}
	defer rows.Close()

	var traces []ContextTraceMeta
	for rows.Next() {
		var m ContextTraceMeta
		var createdNano int64
		var q sql.NullString
		var b sql.NullInt64

		if err := rows.Scan(&m.ID, &m.Workspace, &createdNano, &q, &b, &m.SizeBytes); err != nil {
			return nil, err
		}
		if q.Valid {
			m.Query = q.String
		}
		if b.Valid {
			m.Budget = int(b.Int64)
		}
		m.CreatedAt = time.Unix(0, createdNano).UTC()
		traces = append(traces, m)
	}
	return traces, rows.Err()
}

// EvictContextTracesLRU evicts oldest context traces for a workspace until total bytes <= maxBytes.
func (s *Store) EvictContextTracesLRU(workspace string, maxBytes int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictContextTracesLRULocked(workspace, maxBytes)
}

func (s *Store) evictContextTracesLRULocked(workspace string, maxBytes int64) (int64, error) {
	if workspace == "" || maxBytes <= 0 {
		return 0, nil
	}

	var totalBytes int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM context_traces WHERE workspace = ?`, workspace).Scan(&totalBytes)
	if err != nil || totalBytes <= maxBytes {
		return 0, err
	}

	rows, err := s.db.Query(`
		SELECT id, size_bytes FROM context_traces
		WHERE workspace = ?
		ORDER BY created_at ASC
	`, workspace)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type item struct {
		id   string
		size int64
	}
	var toDelete []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.size); err == nil {
			toDelete = append(toDelete, it)
		}
	}

	var deletedCount int64
	for _, it := range toDelete {
		if totalBytes <= maxBytes {
			break
		}
		if _, err := s.db.Exec(`DELETE FROM context_traces WHERE id = ?`, it.id); err == nil {
			totalBytes -= it.size
			deletedCount++
		}
	}

	return deletedCount, nil
}

// PutTraceOutcome records external evaluation outcome data keyed by trace ID.
func (s *Store) PutTraceOutcome(traceID string, outcomeJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().UnixNano()
	query := `
	INSERT INTO trace_outcomes (trace_id, outcome_json, created_at)
	VALUES (?, ?, ?)
	ON CONFLICT(trace_id) DO UPDATE SET
		outcome_json=excluded.outcome_json,
		created_at=excluded.created_at;
	`
	_, err := s.db.Exec(query, traceID, outcomeJSON, now)
	return err
}

// GetTraceOutcome retrieves the external outcome data for a trace ID.
func (s *Store) GetTraceOutcome(traceID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT outcome_json FROM trace_outcomes WHERE trace_id = ?`
	var outcome string
	err := s.db.QueryRow(query, traceID).Scan(&outcome)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return outcome, nil
}





// GetFileIndexState returns the recorded mod_time and hash for a file within a workspace.
func (s *Store) GetFileIndexState(workspace, filePath string) (int64, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var modTime int64
	var hash string
	err := s.db.QueryRow(`SELECT mod_time, hash FROM file_index_state WHERE workspace = ? AND file_path = ?`, workspace, filePath).Scan(&modTime, &hash)
	if err != nil {
		return 0, "", err
	}
	return modTime, hash, nil
}

// UpdateFileIndexState updates or inserts the file index tracking state, scoped by workspace.
func (s *Store) UpdateFileIndexState(workspace, filePath string, modTime int64, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
	INSERT INTO file_index_state (workspace, file_path, mod_time, hash, indexed_at)
	VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(workspace, file_path) DO UPDATE SET
		mod_time=excluded.mod_time,
		hash=excluded.hash,
		indexed_at=CURRENT_TIMESTAMP
	`, workspace, filePath, modTime, hash)
	return err
}

// PruneFileSymbols removes all symbols indexed for a deleted or changed file within a workspace.
func (s *Store) PruneFileSymbols(workspace, filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err1 := s.db.Exec(`DELETE FROM symbol_index WHERE workspace = ? AND file_path = ?`, workspace, filePath)
	_, err2 := s.db.Exec(`DELETE FROM file_index_state WHERE workspace = ? AND file_path = ?`, workspace, filePath)
	if err1 != nil {
		return err1
	}
	return err2
}

// PruneWorkspace removes all symbols and file index state for the given workspace.
func (s *Store) PruneWorkspace(workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err1 := s.db.Exec(`DELETE FROM symbol_index WHERE workspace = ?`, workspace)
	_, err2 := s.db.Exec(`DELETE FROM file_index_state WHERE workspace = ?`, workspace)
	if err1 != nil {
		return err1
	}
	return err2
}

// LogAudit records an evaluated policy action in audit_log.
func (s *Store) LogAudit(policyID, action, pattern, path, redactedPreview string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO audit_log (policy_id, action, pattern, path, redacted_preview) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, policyID, action, pattern, path, redactedPreview)
	return err
}

// GetAuditLogs retrieves recent audit logs up to limit.
func (s *Store) GetAuditLogs(limit int) ([]map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, timestamp, policy_id, action, pattern, path, redacted_preview FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []map[string]any
	for rows.Next() {
		var id int
		var ts, polID, act, pat, pth, prev string
		if err := rows.Scan(&id, &ts, &polID, &act, &pat, &pth, &prev); err != nil {
			return nil, err
		}
		logs = append(logs, map[string]any{
			"id":               id,
			"timestamp":        ts,
			"policy_id":        polID,
			"action":           act,
			"pattern":          pat,
			"path":             pth,
			"redacted_preview": prev,
		})
	}
	return logs, rows.Err()
}


// GetRecentUnexpiredArtifactIDs returns up to limit artifact IDs in the given workspace
// that are either pinned or have not yet expired, ordered by creation date descending.
func (s *Store) GetRecentUnexpiredArtifactIDs(workspace string, limit int) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC().UnixNano()
	rows, err := s.db.Query(
		`SELECT id FROM artifacts
		 WHERE workspace = ? AND (pinned = TRUE OR expires_at > ?)
		 ORDER BY created_at DESC LIMIT ?`,
		workspace, now, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent artifacts: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan artifact id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ComputeHash returns the first 8 characters of the SHA-256 hash of the content.
func ComputeHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}

// SHA256Full computes the complete 64-character hex SHA-256 hash of data.
func SHA256Full(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// PutArtifact stores an immutable artifact with full SHA-256 identity, workspace isolation, and TTL.
// It enforces MaxSingleArtifactBytes pre-flight and workspace quotas (count and bytes) post-insert.
func (s *Store) PutArtifact(art *Artifact) (string, error) {
	// Pre-flight: reject oversized payloads before acquiring the lock
	if int64(len(art.Body)) > MaxSingleArtifactBytes {
		return "", fmt.Errorf("artifact body size %d bytes exceeds maximum allowed %d bytes (20 MB)", len(art.Body), MaxSingleArtifactBytes)
	}

	// Privacy policy enforcement: block/deny rejection before any data touches disk
	if s.policy != nil {
		eval := s.policy.EvaluateContent(art.Body)
		if !eval.Allowed {
			return "", fmt.Errorf("artifact storage blocked by privacy policy: %s", eval.Reason)
		}
		// Run redactor to detect and mask secrets
		redactor := dlp.NewRedactor()
		redacted, mapping := redactor.Redact(art.Body)
		if len(mapping) > 0 {
			art.SourceHash = SHA256Full(art.Body)
			art.Body = redacted
			art.IsRedacted = true
			art.Hash = ""
			art.SizeBytes = 0
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if art.Hash == "" {
		art.Hash = SHA256Full(art.Body)
	}
	if art.ID == "" {
		h := art.Hash
		if len(h) > 16 {
			h = h[:16]
		}
		art.ID = "art_" + h
	}
	if art.CreatedAt.IsZero() {
		art.CreatedAt = time.Now().UTC()
	}
	if art.ExpiresAt.IsZero() {
		// Default TTL: 7 days
		art.ExpiresAt = art.CreatedAt.Add(7 * 24 * time.Hour)
	}
	if art.LastAccessedAt.IsZero() {
		art.LastAccessedAt = art.CreatedAt
	}
	art.SizeBytes = int64(len(art.Body))

	query := `
	INSERT INTO artifacts (id, hash, source_hash, type, workspace, created_at, expires_at, pinned, size_bytes, transform_version, is_redacted, body, last_accessed_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		pinned=excluded.pinned,
		expires_at=excluded.expires_at,
		last_accessed_at=excluded.last_accessed_at;
	`
	_, err := s.db.Exec(query,
		art.ID,
		art.Hash,
		art.SourceHash,
		art.Type,
		art.Workspace,
		art.CreatedAt.UnixNano(),
		art.ExpiresAt.UnixNano(),
		art.Pinned,
		art.SizeBytes,
		art.TransformVersion,
		art.IsRedacted,
		art.Body,
		art.LastAccessedAt.UnixNano(),
	)
	if err != nil {
		return "", fmt.Errorf("failed to put artifact: %w", err)
	}

	// Post-insert: enforce workspace quotas (deadlock-free — lock already held)
	if art.Workspace != "" {
		s.evictArtifactsLRULocked(art.Workspace, DefaultWorkspaceMaxCount, DefaultWorkspaceMaxBytes)
	}

	// Opportunistic expiry sweep if >10 minutes since last sweep
	if time.Since(s.lastSweepTime) > 10*time.Minute {
		s.evictExpiredArtifactsLocked()
		s.lastSweepTime = time.Now()
	}

	return art.ID, nil
}

// GetArtifact retrieves an artifact by ID, strictly verifying workspace isolation if workspace is provided.
// It updates the last_accessed_at timestamp for LRU cache tracking.
func (s *Store) GetArtifact(id string, workspace string) (*Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	SELECT id, hash, source_hash, type, workspace, created_at, expires_at, pinned, size_bytes, transform_version, is_redacted, body, last_accessed_at
	FROM artifacts
	WHERE id = ?
	`
	var args []any
	args = append(args, id)
	if workspace != "" {
		query += " AND workspace = ?"
		args = append(args, workspace)
	}

	row := s.db.QueryRow(query, args...)
	var a Artifact
	var createdAtNano, expiresAtNano, lastAccessedNano int64
	var srcHash, transVer sql.NullString

	err := row.Scan(
		&a.ID,
		&a.Hash,
		&srcHash,
		&a.Type,
		&a.Workspace,
		&createdAtNano,
		&expiresAtNano,
		&a.Pinned,
		&a.SizeBytes,
		&transVer,
		&a.IsRedacted,
		&a.Body,
		&lastAccessedNano,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("artifact %q not found or access denied by workspace isolation", id)
		}
		return nil, err
	}

	if srcHash.Valid {
		a.SourceHash = srcHash.String
	}
	if transVer.Valid {
		a.TransformVersion = transVer.String
	}
	a.CreatedAt = time.Unix(0, createdAtNano).UTC()
	a.ExpiresAt = time.Unix(0, expiresAtNano).UTC()
	if lastAccessedNano == 0 {
		a.LastAccessedAt = a.CreatedAt
	} else {
		a.LastAccessedAt = time.Unix(0, lastAccessedNano).UTC()
	}

	// Update last_accessed_at for LRU
	nowNano := time.Now().UTC().UnixNano()
	_, _ = s.db.Exec(`UPDATE artifacts SET last_accessed_at = ? WHERE id = ?`, nowNano, id)
	a.LastAccessedAt = time.Unix(0, nowNano).UTC()

	return &a, nil
}

// PruneExpiredArtifacts removes unpinned expired artifacts past their TTL.
// Deprecated: prefer EvictExpiredArtifacts which uses the same logic.
func (s *Store) PruneExpiredArtifacts() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictExpiredArtifactsLocked()
}

// evictExpiredArtifactsLocked removes unpinned expired artifacts. Assumes s.mu is held.
func (s *Store) evictExpiredArtifactsLocked() (int64, error) {
	now := time.Now().UTC().UnixNano()
	res, err := s.db.Exec(`DELETE FROM artifacts WHERE pinned = FALSE AND expires_at < ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EvictExpiredArtifacts removes unpinned artifacts whose TTL has elapsed.
// Uses the (pinned, expires_at) index for efficient non-scanning deletes.
func (s *Store) EvictExpiredArtifacts() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictExpiredArtifactsLocked()
}

// EnforceQuota is an alias for EvictArtifactsLRU with unlimited count, matching legacy callers.
func (s *Store) EnforceQuota(workspace string, maxBytes int64) (int64, error) {
	return s.EvictArtifactsLRU(workspace, 0, maxBytes)
}

// EvictArtifactsLRU prunes the least recently accessed unpinned artifacts in a workspace
// until total artifact count <= maxCount (if maxCount > 0) AND total bytes <= maxBytes (if maxBytes > 0).
func (s *Store) EvictArtifactsLRU(workspace string, maxCount int, maxBytes int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictArtifactsLRULocked(workspace, maxCount, maxBytes)
}

// evictArtifactsLRULocked is the internal eviction helper that assumes s.mu is already held.
// This prevents deadlocks when called from PutArtifact (which already holds s.mu).
func (s *Store) evictArtifactsLRULocked(workspace string, maxCount int, maxBytes int64) (int64, error) {
	if workspace == "" {
		return 0, nil
	}

	var totalBytes int64
	var totalCount int
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0), COUNT(*) FROM artifacts WHERE workspace = ?`, workspace).Scan(&totalBytes, &totalCount)
	if err != nil {
		return 0, err
	}

	needCountPrune := maxCount > 0 && totalCount > maxCount
	needBytesPrune := maxBytes > 0 && totalBytes > maxBytes

	if !needCountPrune && !needBytesPrune {
		return 0, nil
	}

	var deletedCount int64
	// Query unpinned artifacts ordered by least recently accessed first
	rows, err := s.db.Query(`
		SELECT id, size_bytes 
		FROM artifacts 
		WHERE workspace = ? AND pinned = FALSE 
		ORDER BY last_accessed_at ASC, created_at ASC
	`, workspace)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type item struct {
		id   string
		size int64
	}
	var toDelete []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.size); err == nil {
			toDelete = append(toDelete, it)
		}
	}

	for _, it := range toDelete {
		countOK := maxCount <= 0 || totalCount <= maxCount
		bytesOK := maxBytes <= 0 || totalBytes <= maxBytes
		if countOK && bytesOK {
			break
		}

		if _, err := s.db.Exec(`DELETE FROM artifacts WHERE id = ?`, it.id); err == nil {
			totalBytes -= it.size
			totalCount--
			deletedCount++
		}
	}

	return deletedCount, nil
}

// ListArtifacts returns all artifacts for a workspace without loading bodies (for CLI display).
// Results are ordered by created_at DESC.
func (s *Store) ListArtifacts(workspace string) ([]Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, hash, source_hash, type, workspace, created_at, expires_at, pinned, size_bytes, transform_version, is_redacted, last_accessed_at
		FROM artifacts
		WHERE workspace = ?
		ORDER BY created_at DESC
	`, workspace)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	defer rows.Close()

	var artifacts []Artifact
	for rows.Next() {
		var a Artifact
		var createdAtNano, expiresAtNano, lastAccessedNano int64
		var srcHash, transVer sql.NullString

		if err := rows.Scan(
			&a.ID, &a.Hash, &srcHash, &a.Type, &a.Workspace,
			&createdAtNano, &expiresAtNano, &a.Pinned, &a.SizeBytes,
			&transVer, &a.IsRedacted, &lastAccessedNano,
		); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}

		if srcHash.Valid {
			a.SourceHash = srcHash.String
		}
		if transVer.Valid {
			a.TransformVersion = transVer.String
		}
		a.CreatedAt = time.Unix(0, createdAtNano).UTC()
		a.ExpiresAt = time.Unix(0, expiresAtNano).UTC()
		if lastAccessedNano == 0 {
			a.LastAccessedAt = a.CreatedAt
		} else {
			a.LastAccessedAt = time.Unix(0, lastAccessedNano).UTC()
		}
		// Body intentionally not loaded
		artifacts = append(artifacts, a)
	}
	return artifacts, rows.Err()
}

// GetWorkspaceArtifactStats returns the total count and byte footprint of artifacts in a workspace.
func (s *Store) GetWorkspaceArtifactStats(workspace string) (count int, totalBytes int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err = s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM artifacts WHERE workspace = ?`,
		workspace,
	).Scan(&count, &totalBytes)
	return
}


// PutBlob stores a content block and returns its hash.
func (s *Store) PutBlob(filePath string, startLine, endLine int, body string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hash := ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, startLine, endLine, body))

	query := `
	INSERT INTO content_blobs (hash, file_path, start_line, end_line, body, created_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(hash) DO UPDATE SET
		body=excluded.body,
		created_at=excluded.created_at;
	`
	_, err := s.db.Exec(query, hash, filePath, startLine, endLine, body, time.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("failed to put blob: %w", err)
	}

	return hash, nil
}

// GetBlob retrieves a stored blob by its hash.
func (s *Store) GetBlob(hash string) (*Blob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT hash, file_path, start_line, end_line, body, created_at FROM content_blobs WHERE hash = ?`
	row := s.db.QueryRow(query, hash)

	var b Blob
	if err := row.Scan(&b.Hash, &b.FilePath, &b.StartLine, &b.EndLine, &b.Body, &b.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("blob with hash #%s not found", hash)
		}
		return nil, err
	}

	return &b, nil
}

// IndexSymbol records a symbol mapping scoped to a workspace.
func (s *Store) IndexSymbol(workspace, symbol, kind, filePath string, line int, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO symbol_index (workspace, symbol, kind, file_path, line, hash) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, workspace, symbol, kind, filePath, line, hash)
	return err
}

// SearchSymbols finds matching symbols using FTS5 full-text match with BM25 ranking, falling back to LIKE if needed.
// Results are scoped to the given workspace.
func (s *Store) SearchSymbols(workspace, queryStr string, limit int) ([]SymbolEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	// Prepare FTS5 query tokens
	words := strings.Fields(queryStr)
	var ftsTokens []string
	for _, w := range words {
		// Strip special FTS5 operators and quote
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				return r
			}
			return ' '
		}, w)
		for _, part := range strings.Fields(clean) {
			if part != "" {
				ftsTokens = append(ftsTokens, part+"*")
			}
		}
	}

	var results []SymbolEntry
	if s.hasFTS5 && len(ftsTokens) > 0 {
		ftsExpr := strings.Join(ftsTokens, " OR ")
		ftsQuery := `
		SELECT workspace, symbol, kind, file_path, line, hash
		FROM symbol_fts
		WHERE symbol_fts MATCH ? AND workspace = ?
		ORDER BY bm25(symbol_fts)
		LIMIT ?
		`
		rows, err := s.db.Query(ftsQuery, ftsExpr, workspace, limit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e SymbolEntry
				if err := rows.Scan(&e.Workspace, &e.Symbol, &e.Kind, &e.FilePath, &e.Line, &e.Hash); err == nil {
					results = append(results, e)
				}
			}
			if len(results) > 0 {
				return results, nil
			}
		}
	}

	// Fallback to LIKE query
	query := `
	SELECT workspace, symbol, kind, file_path, line, hash 
	FROM symbol_index 
	WHERE (symbol LIKE ? OR file_path LIKE ?) AND workspace = ?
	LIMIT ?
	`
	pattern := "%" + queryStr + "%"
	rows, err := s.db.Query(query, pattern, pattern, workspace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.Workspace, &e.Symbol, &e.Kind, &e.FilePath, &e.Line, &e.Hash); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}


// systemTables are protected from QuerySQL access.
var systemTables = map[string]bool{
	"content_blobs":  true,
	"symbol_index":   true,
	"cache_sessions": true,
	"sqlite_master":  true,
	"sqlite_schema":  true,
}

// ImportTabular creates a dynamic table and bulk-inserts rows from tabular data.
// Column names are sanitized to prevent SQL injection. Uses a transaction for performance.
func (s *Store) ImportTabular(tableName string, columns []string, rows [][]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate table name: alphanumeric + underscores only
	for _, c := range tableName {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return fmt.Errorf("invalid table name: %q", tableName)
		}
	}

	// Reject system table names
	if systemTables[tableName] {
		return fmt.Errorf("cannot import into system table: %s", tableName)
	}

	// Sanitize column names
	safeCols := make([]string, len(columns))
	for i, col := range columns {
		safe := sanitizeIdentifier(col)
		if safe == "" {
			safe = fmt.Sprintf("col_%d", i)
		}
		safeCols[i] = safe
	}

	// Build CREATE TABLE
	var colDefs []string
	for _, col := range safeCols {
		colDefs = append(colDefs, fmt.Sprintf("\"%s\" TEXT", col))
	}
	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS \"%s\" (%s)", tableName, strings.Join(colDefs, ", "))

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(createSQL); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// Clear existing data (idempotent reimport)
	if _, err := tx.Exec(fmt.Sprintf("DELETE FROM \"%s\"", tableName)); err != nil {
		return fmt.Errorf("clear table: %w", err)
	}

	// Prepare bulk insert
	placeholders := make([]string, len(safeCols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT INTO \"%s\" (%s) VALUES (%s)",
		tableName,
		strings.Join(func() []string {
			quoted := make([]string, len(safeCols))
			for i, c := range safeCols {
				quoted[i] = fmt.Sprintf("\"%s\"", c)
			}
			return quoted
		}(), ", "),
		strings.Join(placeholders, ", "))

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, row := range rows {
		args := make([]any, len(safeCols))
		for i := 0; i < len(safeCols); i++ {
			if i < len(row) {
				args[i] = row[i]
			} else {
				args[i] = ""
			}
		}
		if _, err := stmt.Exec(args...); err != nil {
			return fmt.Errorf("insert row: %w", err)
		}
	}

	return tx.Commit()
}

// QuerySQL executes a read-only SQL query against an imported tabular table.
// Only SELECT statements are allowed, and only against non-system tables.
func (s *Store) QuerySQL(sql string) ([]map[string]string, []string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Validate: must be SELECT
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return nil, nil, fmt.Errorf("only SELECT queries are allowed, got: %s", trimmed[:min(len(trimmed), 20)])
	}

	// Reject queries that reference system tables
	lowerSQL := strings.ToLower(trimmed)
	for tbl := range systemTables {
		if strings.Contains(lowerSQL, strings.ToLower(tbl)) {
			return nil, nil, fmt.Errorf("access to system table %q is not allowed", tbl)
		}
	}

	rows, err := s.db.Query(trimmed)
	if err != nil {
		return nil, nil, fmt.Errorf("query execution: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var results []map[string]string
	for rows.Next() {
		ptrs := make([]any, len(cols))
		values := make([]any, len(cols))
		for i := range ptrs {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make(map[string]string, len(cols))
		for i, col := range cols {
			if values[i] != nil {
				row[col] = fmt.Sprintf("%v", values[i])
			} else {
				row[col] = ""
			}
		}
		results = append(results, row)
	}

	return results, cols, rows.Err()
}

// sanitizeIdentifier strips non-alphanumeric chars (except underscore) from a SQL identifier.
func sanitizeIdentifier(name string) string {
	var sb strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
