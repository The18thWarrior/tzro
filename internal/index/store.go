package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"tzro/internal/symbols"

	_ "modernc.org/sqlite"
)

// IndexStore manages the SQLite repository pre-index database.
type IndexStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewIndexStore opens or creates the SQLite index database at dbPath.
func NewIndexStore(dbPath string) (*IndexStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating index dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening index db: %w", err)
	}

	store := &IndexStore{db: db}
	if err := store.ensureSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}

	return store, nil
}

// Close closes the underlying SQLite database connection.
func (s *IndexStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Checkpoint executes a WAL truncate checkpoint to flush all data to the main DB file.
func (s *IndexStore) Checkpoint() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		return err
	}
	return nil
}

func (s *IndexStore) ensureSchema() error {
	queries := []string{
		// Files tracked in index with content hashes for delta invalidation
		`CREATE TABLE IF NOT EXISTS index_files (
			file_path TEXT PRIMARY KEY,
			plane TEXT NOT NULL, -- 'code' | 'doc'
			content_hash TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,

		// Code plane symbols
		`CREATE TABLE IF NOT EXISTS index_symbols (
			id TEXT PRIMARY KEY,
			file_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			signature TEXT,
			doc_comment TEXT,
			line INTEGER,
			end_line INTEGER,
			exported INTEGER,
			FOREIGN KEY(file_path) REFERENCES index_files(file_path) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_name ON index_symbols(name)`,
		`CREATE INDEX IF NOT EXISTS idx_symbols_file ON index_symbols(file_path)`,

		// Code plane call edges
		`CREATE TABLE IF NOT EXISTS index_edges (
			caller_file TEXT NOT NULL,
			caller_name TEXT NOT NULL,
			callee_file TEXT NOT NULL,
			callee_name TEXT NOT NULL,
			call_line INTEGER,
			edge_kind TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_caller ON index_edges(caller_file, caller_name)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_callee ON index_edges(callee_file, callee_name)`,

		// Document plane chunks
		`CREATE TABLE IF NOT EXISTS index_doc_chunks (
			id TEXT PRIMARY KEY,
			file_path TEXT NOT NULL,
			kind TEXT NOT NULL,
			header TEXT,
			content TEXT NOT NULL,
			symbol_refs TEXT, -- JSON array of strings
			embedding_json TEXT, -- Serialized float32 array
			FOREIGN KEY(file_path) REFERENCES index_files(file_path) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_file ON index_doc_chunks(file_path)`,

		// Full-Text Search virtual table (FTS5) for both code and doc items
		`CREATE VIRTUAL TABLE IF NOT EXISTS index_fts USING fts5(
			id,
			file_path,
			kind,
			title,
			signature,
			content,
			tokenize = 'porter unicode61'
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("executing %s: %w", q, err)
		}
	}
	return nil
}

// GetFileHash returns the stored content hash and existence for a file.
func (s *IndexStore) GetFileHash(filePath string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var hash string
	err := s.db.QueryRow(`SELECT content_hash FROM index_files WHERE file_path = ?`, filePath).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// UpsertCodeFile registers or updates symbols and call edges for a code file.
func (s *IndexStore) UpsertCodeFile(relPath string, syms []symbols.Symbol, edges []symbols.CallEdge, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()

	// Update index_files
	_, err = tx.Exec(`INSERT INTO index_files(file_path, plane, content_hash, updated_at)
		VALUES (?, 'code', ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET content_hash = excluded.content_hash, updated_at = excluded.updated_at`,
		relPath, hash, now)
	if err != nil {
		return fmt.Errorf("updating index_files: %w", err)
	}

	// Clean up existing symbols, edges, and FTS entries for this file
	if _, err := tx.Exec(`DELETE FROM index_symbols WHERE file_path = ?`, relPath); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM index_edges WHERE caller_file = ?`, relPath); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM index_fts WHERE file_path = ?`, relPath); err != nil {
		return err
	}

	// Insert symbols and FTS
	symStmt, err := tx.Prepare(`INSERT INTO index_symbols(id, file_path, name, kind, signature, doc_comment, line, end_line, exported)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer symStmt.Close()

	ftsStmt, err := tx.Prepare(`INSERT INTO index_fts(id, file_path, kind, title, signature, content)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ftsStmt.Close()

	for i, sym := range syms {
		symID := fmt.Sprintf("%s:%s:%d", relPath, sym.Name, sym.Line)
		if sym.Name == "" {
			symID = fmt.Sprintf("%s:sym:%d", relPath, i)
		}
		exp := 0
		if sym.Exported {
			exp = 1
		}
		if _, err := symStmt.Exec(symID, relPath, sym.Name, string(sym.Kind), sym.Signature, sym.DocComment, sym.Line, sym.EndLine, exp); err != nil {
			return fmt.Errorf("inserting symbol %s: %w", sym.Name, err)
		}

		// Index in FTS
		content := sym.DocComment
		if _, err := ftsStmt.Exec(symID, relPath, string(sym.Kind), sym.Name, sym.Signature, content); err != nil {
			return fmt.Errorf("inserting fts symbol %s: %w", sym.Name, err)
		}
	}

	// Insert call edges
	if len(edges) > 0 {
		edgeStmt, err := tx.Prepare(`INSERT INTO index_edges(caller_file, caller_name, callee_file, callee_name, call_line, edge_kind)
			VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer edgeStmt.Close()

		for _, edge := range edges {
			if _, err := edgeStmt.Exec(edge.CallerFile, edge.CallerName, edge.CalleeFile, edge.CalleeName, edge.CallLine, edge.EdgeKind); err != nil {
				return fmt.Errorf("inserting edge: %w", err)
			}
		}
	}

	return tx.Commit()
}

// UpsertDocChunks registers or updates chunks for a document file.
func (s *IndexStore) UpsertDocChunks(relPath string, chunks []DocChunk, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()

	// Update index_files
	_, err = tx.Exec(`INSERT INTO index_files(file_path, plane, content_hash, updated_at)
		VALUES (?, 'doc', ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET content_hash = excluded.content_hash, updated_at = excluded.updated_at`,
		relPath, hash, now)
	if err != nil {
		return fmt.Errorf("updating index_files: %w", err)
	}

	// Clean up existing chunks and FTS entries
	if _, err := tx.Exec(`DELETE FROM index_doc_chunks WHERE file_path = ?`, relPath); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM index_fts WHERE file_path = ?`, relPath); err != nil {
		return err
	}

	chunkStmt, err := tx.Prepare(`INSERT INTO index_doc_chunks(id, file_path, kind, header, content, symbol_refs, embedding_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer chunkStmt.Close()

	ftsStmt, err := tx.Prepare(`INSERT INTO index_fts(id, file_path, kind, title, signature, content)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ftsStmt.Close()

	for _, chunk := range chunks {
		symRefsJSON, _ := json.Marshal(chunk.SymbolRefs)
		var embJSON string
		if len(chunk.Embedding) > 0 {
			embBytes, _ := json.Marshal(chunk.Embedding)
			embJSON = string(embBytes)
		}

		if _, err := chunkStmt.Exec(chunk.ID, relPath, chunk.Kind, chunk.Header, chunk.Content, string(symRefsJSON), embJSON); err != nil {
			return fmt.Errorf("inserting doc chunk %s: %w", chunk.ID, err)
		}

		// Index in FTS: title=Header, signature=symbol references, content=Body text
		sig := strings.Join(chunk.SymbolRefs, " ")
		if _, err := ftsStmt.Exec(chunk.ID, relPath, chunk.Kind, chunk.Header, sig, chunk.Content); err != nil {
			return fmt.Errorf("inserting fts chunk %s: %w", chunk.ID, err)
		}
	}

	return tx.Commit()
}

// SearchFTS performs a BM25 query against index_fts table.
func (s *IndexStore) SearchFTS(query string, limit int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	// Sanitize FTS5 query to prevent syntax errors
	sanitized := sanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}

	sqlQuery := `SELECT id, file_path, kind, title, signature, content, bm25(index_fts) as rank
		FROM index_fts
		WHERE index_fts MATCH ?
		ORDER BY rank ASC
		LIMIT ?`

	rows, err := s.db.Query(sqlQuery, sanitized, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var res SearchResult
		var rank float64
		if err := rows.Scan(&res.ID, &res.FilePath, &res.Kind, &res.Title, &res.Signature, &res.Content, &rank); err != nil {
			return nil, err
		}
		// Convert BM25 negative score to positive similarity score
		res.Score = 1.0 / (1.0 + rank)
		if strings.HasPrefix(res.Kind, "doc_") {
			res.SourceType = "doc"
		} else {
			res.SourceType = "code"
		}
		results = append(results, res)
	}

	return results, rows.Err()
}

func sanitizeFTS5Query(q string) string {
	// Strip special punctuation and join with OR for broad recall
	tokens := strings.FieldsFunc(q, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
	})
	if len(tokens) == 0 {
		return ""
	}
	var clean []string
	for _, tok := range tokens {
		if len(tok) > 1 {
			clean = append(clean, `"`+tok+`"`)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, " OR ")
}
