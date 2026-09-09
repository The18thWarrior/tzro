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

	_ "modernc.org/sqlite"
)

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
	Symbol   string `json:"symbol"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Hash     string `json:"hash"`
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
}

// Store handles local SQLite content-addressed storage and FTS5 search.
type Store struct {
	db   *sql.DB
	mu   sync.RWMutex
	path string
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
	schema := `
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

	CREATE INDEX IF NOT EXISTS idx_symbol_name ON symbol_index(symbol);
	CREATE INDEX IF NOT EXISTS idx_symbol_file ON symbol_index(file_path);

	CREATE VIRTUAL TABLE IF NOT EXISTS symbol_fts USING fts5(
		symbol,
		kind,
		file_path,
		line UNINDEXED,
		hash UNINDEXED,
		content='symbol_index',
		content_rowid='id'
	);

	CREATE TRIGGER IF NOT EXISTS symbol_ai AFTER INSERT ON symbol_index BEGIN
		INSERT INTO symbol_fts(rowid, symbol, kind, file_path, line, hash)
		VALUES (new.id, new.symbol, new.kind, new.file_path, new.line, new.hash);
	END;

	CREATE TRIGGER IF NOT EXISTS symbol_ad AFTER DELETE ON symbol_index BEGIN
		INSERT INTO symbol_fts(symbol_fts, rowid, symbol, kind, file_path, line, hash)
		VALUES ('delete', old.id, old.symbol, old.kind, old.file_path, old.line, old.hash);
	END;

	CREATE TRIGGER IF NOT EXISTS symbol_au AFTER UPDATE ON symbol_index BEGIN
		INSERT INTO symbol_fts(symbol_fts, rowid, symbol, kind, file_path, line, hash)
		VALUES ('delete', old.id, old.symbol, old.kind, old.file_path, old.line, old.hash);
		INSERT INTO symbol_fts(rowid, symbol, kind, file_path, line, hash)
		VALUES (new.id, new.symbol, new.kind, new.file_path, new.line, new.hash);
	END;

	CREATE TABLE IF NOT EXISTS cache_sessions (
		session_id TEXT PRIMARY KEY,
		prefix_hash TEXT NOT NULL,
		last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS file_index_state (
		file_path TEXT PRIMARY KEY,
		mod_time INTEGER NOT NULL,
		hash TEXT NOT NULL,
		indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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
		body TEXT NOT NULL
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
	`
	_, err := s.db.Exec(schema)
	return err
}

// PutSession records or updates a versioned session manifest.
func (s *Store) PutSession(id, workspace, branch string, schemaVersion int, manifestJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Unix()
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




// GetFileIndexState returns the recorded mod_time and hash for a file.
func (s *Store) GetFileIndexState(filePath string) (int64, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var modTime int64
	var hash string
	err := s.db.QueryRow(`SELECT mod_time, hash FROM file_index_state WHERE file_path = ?`, filePath).Scan(&modTime, &hash)
	if err != nil {
		return 0, "", err
	}
	return modTime, hash, nil
}

// UpdateFileIndexState updates or inserts the file index tracking state.
func (s *Store) UpdateFileIndexState(filePath string, modTime int64, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
	INSERT INTO file_index_state (file_path, mod_time, hash, indexed_at)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(file_path) DO UPDATE SET
		mod_time=excluded.mod_time,
		hash=excluded.hash,
		indexed_at=CURRENT_TIMESTAMP
	`, filePath, modTime, hash)
	return err
}

// PruneFileSymbols removes all symbols indexed for a deleted or changed file.
func (s *Store) PruneFileSymbols(filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err1 := s.db.Exec(`DELETE FROM symbol_index WHERE file_path = ?`, filePath)
	_, err2 := s.db.Exec(`DELETE FROM file_index_state WHERE file_path = ?`, filePath)
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
func (s *Store) PutArtifact(art *Artifact) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if art.Hash == "" {
		art.Hash = SHA256Full(art.Body)
	}
	if art.ID == "" {
		art.ID = "art_" + art.Hash[:16]
	}
	if art.CreatedAt.IsZero() {
		art.CreatedAt = time.Now().UTC()
	}
	if art.ExpiresAt.IsZero() {
		// Default TTL: 7 days
		art.ExpiresAt = art.CreatedAt.Add(7 * 24 * time.Hour)
	}
	art.SizeBytes = int64(len(art.Body))

	query := `
	INSERT INTO artifacts (id, hash, source_hash, type, workspace, created_at, expires_at, pinned, size_bytes, transform_version, is_redacted, body)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		pinned=excluded.pinned,
		expires_at=excluded.expires_at;
	`
	_, err := s.db.Exec(query,
		art.ID,
		art.Hash,
		art.SourceHash,
		art.Type,
		art.Workspace,
		art.CreatedAt.Unix(),
		art.ExpiresAt.Unix(),
		art.Pinned,
		art.SizeBytes,
		art.TransformVersion,
		art.IsRedacted,
		art.Body,
	)
	if err != nil {
		return "", fmt.Errorf("failed to put artifact: %w", err)
	}

	return art.ID, nil
}

// GetArtifact retrieves an artifact by ID, strictly verifying workspace isolation if workspace is provided.
func (s *Store) GetArtifact(id string, workspace string) (*Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `
	SELECT id, hash, source_hash, type, workspace, created_at, expires_at, pinned, size_bytes, transform_version, is_redacted, body
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
	var createdAtSec, expiresAtSec int64
	var srcHash, transVer sql.NullString

	err := row.Scan(
		&a.ID,
		&a.Hash,
		&srcHash,
		&a.Type,
		&a.Workspace,
		&createdAtSec,
		&expiresAtSec,
		&a.Pinned,
		&a.SizeBytes,
		&transVer,
		&a.IsRedacted,
		&a.Body,
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
	a.CreatedAt = time.Unix(createdAtSec, 0).UTC()
	a.ExpiresAt = time.Unix(expiresAtSec, 0).UTC()

	return &a, nil
}

// PruneExpiredArtifacts removes unpinned expired artifacts past their TTL.
func (s *Store) PruneExpiredArtifacts() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Unix()
	res, err := s.db.Exec(`DELETE FROM artifacts WHERE pinned = FALSE AND expires_at < ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EnforceQuota prunes the oldest unpinned artifacts in a workspace until total bytes <= maxBytes.
func (s *Store) EnforceQuota(workspace string, maxBytes int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var totalBytes int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM artifacts WHERE workspace = ?`, workspace).Scan(&totalBytes)
	if err != nil {
		return 0, err
	}

	if totalBytes <= maxBytes {
		return 0, nil
	}

	var deletedCount int64
	// Delete oldest unpinned artifacts until quota is met
	rows, err := s.db.Query(`SELECT id, size_bytes FROM artifacts WHERE workspace = ? AND pinned = FALSE ORDER BY created_at ASC`, workspace)
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
		if totalBytes <= maxBytes {
			break
		}
		_, err := s.db.Exec(`DELETE FROM artifacts WHERE id = ?`, it.id)
		if err == nil {
			totalBytes -= it.size
			deletedCount++
		}
	}

	return deletedCount, nil
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

// IndexSymbol records a symbol mapping.
func (s *Store) IndexSymbol(symbol, kind, filePath string, line int, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO symbol_index (symbol, kind, file_path, line, hash) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(query, symbol, kind, filePath, line, hash)
	return err
}

// SearchSymbols finds matching symbols using FTS5 full-text match with BM25 ranking, falling back to LIKE if needed.
func (s *Store) SearchSymbols(queryStr string, limit int) ([]SymbolEntry, error) {
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
	if len(ftsTokens) > 0 {
		ftsExpr := strings.Join(ftsTokens, " OR ")
		ftsQuery := `
		SELECT symbol, kind, file_path, line, hash
		FROM symbol_fts
		WHERE symbol_fts MATCH ?
		ORDER BY bm25(symbol_fts)
		LIMIT ?
		`
		rows, err := s.db.Query(ftsQuery, ftsExpr, limit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e SymbolEntry
				if err := rows.Scan(&e.Symbol, &e.Kind, &e.FilePath, &e.Line, &e.Hash); err == nil {
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
	SELECT symbol, kind, file_path, line, hash 
	FROM symbol_index 
	WHERE symbol LIKE ? OR file_path LIKE ?
	LIMIT ?
	`
	pattern := "%" + queryStr + "%"
	rows, err := s.db.Query(query, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var e SymbolEntry
		if err := rows.Scan(&e.Symbol, &e.Kind, &e.FilePath, &e.Line, &e.Hash); err != nil {
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
