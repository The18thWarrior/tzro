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

	CREATE TABLE IF NOT EXISTS cache_sessions (
		session_id TEXT PRIMARY KEY,
		prefix_hash TEXT NOT NULL,
		last_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

// ComputeHash returns the first 8 characters of the SHA-256 hash of the content.
func ComputeHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
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

// SearchSymbols finds matching symbols in the index.
func (s *Store) SearchSymbols(queryStr string, limit int) ([]SymbolEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

	var results []SymbolEntry
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
