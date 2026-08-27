package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
