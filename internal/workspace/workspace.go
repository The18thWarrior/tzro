package workspace

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
)

// DefaultID is the workspace ID used for legacy/fallback behavior.
const DefaultID = "default"

// ID returns a deterministic workspace identifier for the given root path.
// The path is canonicalized (EvalSymlinks + Clean) and hashed (SHA-256, 12 hex chars).
// Returns DefaultID if rootPath is empty.
func ID(rootPath string) string {
	if rootPath == "" {
		return DefaultID
	}

	// Canonicalize: clean the path, resolve symlinks if possible
	cleaned := filepath.Clean(rootPath)
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(real)
	}

	hash := sha256.Sum256([]byte(cleaned))
	return fmt.Sprintf("%x", hash[:6]) // 6 bytes = 12 hex chars
}
