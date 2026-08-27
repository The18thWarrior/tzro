package comparison

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tzro/internal/index"
)

var (
	baseIndexMu     sync.Mutex
	baseIndexDBPath string
)

// EnsureBaseProjectIndex scans and indexes the projectRoot once into a master SQLite database
// and returns its path. Subsequent calls reuse the existing base index snapshot.
func EnsureBaseProjectIndex(ctx context.Context, projectRoot string, embedder index.Embedder) (string, error) {
	baseIndexMu.Lock()
	defer baseIndexMu.Unlock()

	if baseIndexDBPath != "" {
		if _, err := os.Stat(baseIndexDBPath); err == nil {
			return baseIndexDBPath, nil
		}
	}

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("tzro_base_index_%d.db", time.Now().UnixNano()))
	store, err := index.NewIndexStore(tmpPath)
	if err != nil {
		return "", fmt.Errorf("failed to create base index store: %w", err)
	}

	_, err = index.ScanAndIndexWorkspace(ctx, projectRoot, store, embedder)
	if err != nil {
		_ = store.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to scan workspace for base index: %w", err)
	}

	_ = store.Checkpoint()
	if err := store.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to close base index store: %w", err)
	}

	baseIndexDBPath = tmpPath
	return baseIndexDBPath, nil
}

// ResetBaseProjectIndex removes the cached base index snapshot from disk and clears the reference.
func ResetBaseProjectIndex() {
	baseIndexMu.Lock()
	defer baseIndexMu.Unlock()

	if baseIndexDBPath != "" {
		_ = os.Remove(baseIndexDBPath)
		_ = os.Remove(baseIndexDBPath + "-wal")
		_ = os.Remove(baseIndexDBPath + "-shm")
		baseIndexDBPath = ""
	}
}

// CopyIndexDB copies a source SQLite database to dstPath atomically.
func CopyIndexDB(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return fmt.Errorf("failed to create dest dir: %w", err)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source index db: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create dest index db: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy index db bytes: %w", err)
	}

	return dstFile.Sync()
}
