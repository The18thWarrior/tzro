package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"tzro/internal/symbols"
)

var (
	globalIndexMu sync.RWMutex
	globalIndex   *IndexStore
)

// SetGlobalIndex sets the singleton global index store.
func SetGlobalIndex(store *IndexStore) {
	globalIndexMu.Lock()
	defer globalIndexMu.Unlock()
	globalIndex = store
}

// GetGlobalIndex retrieves the singleton global index store if initialized.
func GetGlobalIndex() *IndexStore {
	globalIndexMu.RLock()
	defer globalIndexMu.RUnlock()
	return globalIndex
}

// ScanAndIndexWorkspace scans the workspace directory, extracts code symbols and doc chunks,
// and updates the IndexStore with delta invalidation.
func ScanAndIndexWorkspace(ctx context.Context, rootDir string, store *IndexStore, embedder Embedder) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("index store is nil")
	}

	indexedCount := 0

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable paths
		}

		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == ".tzro" || name == "bin" ||
				name == ".scratch" || name == ".gemini" || name == ".agents" || name == "dist" || name == "build" || name == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(rootDir, path)
		if relPath == "" {
			relPath = d.Name()
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		hashBytes := sha256.Sum256(content)
		hashStr := hex.EncodeToString(hashBytes[:])

		// Check staleness
		existingHash, exists, err := store.GetFileHash(relPath)
		if err == nil && exists && existingHash == hashStr {
			return nil // Up-to-date
		}

		ext := strings.ToLower(filepath.Ext(path))
		if isCodeExtension(ext) {
			syms, _ := symbols.ExtractAllSymbols(d.Name(), content)
			// Assign relPath to symbols
			for i := range syms {
				syms[i].File = relPath
			}
			_ = store.UpsertCodeFile(relPath, syms, nil, hashStr)
			indexedCount++
		} else if isDocExtension(ext) {
			chunks, _ := ChunkDocument(relPath, content)
			if embedder != nil {
				for i := range chunks {
					emb, err := embedder.Embed(ctx, chunks[i].Header+" "+chunks[i].Content)
					if err == nil && len(emb) > 0 {
						chunks[i].Embedding = emb
					}
				}
			}
			_ = store.UpsertDocChunks(relPath, chunks, hashStr)
			indexedCount++
		}

		return nil
	})

	return indexedCount, err
}

func isCodeExtension(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".c", ".cpp", ".h":
		return true
	}
	return false
}

func isDocExtension(ext string) bool {
	switch ext {
	case ".md", ".markdown", ".txt", ".rst", ".adoc":
		return true
	}
	return false
}
