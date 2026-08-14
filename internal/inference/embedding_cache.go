package inference

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"tzro/internal/config"
)

// EmbeddingCache provides a hybrid in-memory + SQLite cache for embedding vectors.
// The sync.Map handles hot-path lookups; SQLite provides persistence across restarts.
// Keys are SHA-256 hashes of the input text.
type EmbeddingCache struct {
	memCache sync.Map // map[string][]float32 (textHash → vector)
	modelID  string   // tracks which model produced cached vectors
	db       *sql.DB
}

// NewEmbeddingCache creates a new embedding cache with its own SQLite connection.
func NewEmbeddingCache(modelID string) *EmbeddingCache {
	c := &EmbeddingCache{modelID: modelID}

	// Open dedicated SQLite DB for embedding cache
	dbPath := filepath.Join(config.ResolvePath(".tzro"), "embedding_cache.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0755)
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EmbeddingCache] Failed to open DB: %v\n", err)
		return c
	}
	c.db = db
	c.ensureTable()
	return c
}

// TextHash returns the SHA-256 hash of a text string for cache keying.
func TextHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// Get retrieves a cached embedding vector. Checks memory first, then SQLite.
func (c *EmbeddingCache) Get(textHash string) ([]float32, bool) {
	// 1. Check in-memory cache
	if v, ok := c.memCache.Load(textHash); ok {
		return v.([]float32), true
	}

	// 2. Check SQLite
	vec, ok := c.getFromDB(textHash)
	if ok {
		// Promote to memory cache
		c.memCache.Store(textHash, vec)
	}
	return vec, ok
}

// Put stores an embedding vector in both memory and SQLite.
func (c *EmbeddingCache) Put(textHash string, vec []float32) {
	c.memCache.Store(textHash, vec)
	c.putToDB(textHash, vec)
}

// GetBatch checks the cache for multiple hashes, returning found vectors
// and the list of hashes that were cache misses.
func (c *EmbeddingCache) GetBatch(hashes []string) (found map[string][]float32, missed []string) {
	found = make(map[string][]float32)
	for _, h := range hashes {
		if vec, ok := c.Get(h); ok {
			found[h] = vec
		} else {
			missed = append(missed, h)
		}
	}
	return found, missed
}

// PutBatch stores multiple embedding vectors.
func (c *EmbeddingCache) PutBatch(entries map[string][]float32) {
	for h, vec := range entries {
		c.Put(h, vec)
	}
}

// Close closes the SQLite database connection.
func (c *EmbeddingCache) Close() {
	if c.db != nil {
		c.db.Close()
	}
}

// ensureTable creates the embedding_cache table if it doesn't exist.
func (c *EmbeddingCache) ensureTable() {
	if c.db == nil {
		return
	}
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS embedding_cache (
		text_hash TEXT PRIMARY KEY,
		embedding BLOB NOT NULL,
		model_id TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EmbeddingCache] Failed to create table: %v\n", err)
	}
}

// getFromDB retrieves a vector from SQLite, checking model_id matches.
func (c *EmbeddingCache) getFromDB(textHash string) ([]float32, bool) {
	if c.db == nil {
		return nil, false
	}

	var blob []byte
	var storedModel string
	err := c.db.QueryRow(
		"SELECT embedding, model_id FROM embedding_cache WHERE text_hash = ?",
		textHash,
	).Scan(&blob, &storedModel)
	if err != nil {
		return nil, false
	}

	// Invalidate if model changed
	if storedModel != c.modelID {
		_, _ = c.db.Exec("DELETE FROM embedding_cache WHERE text_hash = ?", textHash)
		return nil, false
	}

	return blobToFloat32(blob), true
}

// putToDB stores a vector in SQLite.
func (c *EmbeddingCache) putToDB(textHash string, vec []float32) {
	if c.db == nil {
		return
	}

	blob := float32ToBlob(vec)
	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO embedding_cache (text_hash, embedding, model_id, created_at)
		 VALUES (?, ?, ?, strftime('%s', 'now'))`,
		textHash, blob, c.modelID,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[EmbeddingCache] Failed to store embedding: %v\n", err)
	}
}

// float32ToBlob converts a float32 slice to a byte slice (little-endian).
func float32ToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// blobToFloat32 converts a byte slice back to a float32 slice (little-endian).
func blobToFloat32(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(blob)/4)
	for i := range vec {
		bits := uint32(blob[i*4]) |
			uint32(blob[i*4+1])<<8 |
			uint32(blob[i*4+2])<<16 |
			uint32(blob[i*4+3])<<24
		vec[i] = math.Float32frombits(bits)
	}
	return vec
}
