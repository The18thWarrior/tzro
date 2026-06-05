package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"tzro/internal/config"
)

// GetAllowedPaths resolves the allowed filesystem roots for filesystem tools.
// Priority: 1) allowedPaths from .tzro/mcp_config.json, 2) TZRO_DIR env var, 3) cwd
func GetAllowedPaths() []string {
	// Try reading allowedPaths from MCP config
	mcpConfigPath := config.ResolvePath(filepath.Join(".tzro", "mcp_config.json"))
	if data, err := os.ReadFile(mcpConfigPath); err == nil {
		var mcpCfg struct {
			AllowedPaths []string `json:"allowedPaths"`
		}
		if json.Unmarshal(data, &mcpCfg) == nil && len(mcpCfg.AllowedPaths) > 0 {
			return mcpCfg.AllowedPaths
		}
	}

	// Fall back to TZRO_DIR
	if tzroDir := os.Getenv("TZRO_DIR"); tzroDir != "" {
		return []string{tzroDir}
	}

	// Last resort: current working directory
	if cwd, err := os.Getwd(); err == nil {
		return []string{cwd}
	}

	return nil
}

// PathValidator enforces a security boundary for filesystem tools.
// All requested paths must resolve to a location inside one of the allowed roots.
// It rejects path traversal (../) and symlinks that escape the allowed roots.
type PathValidator struct {
	allowedRoots []string
}

// NewPathValidator creates a validator with the given allowed root directories.
// Each root is resolved to an absolute, cleaned path.
func NewPathValidator(allowedRoots []string) *PathValidator {
	cleaned := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(abs))
	}
	return &PathValidator{allowedRoots: cleaned}
}

// ValidatePath checks that the requested path exists and resolves to a location
// inside one of the allowed roots. It resolves symlinks to detect escapes.
// Returns the resolved absolute path on success.
func (v *PathValidator) ValidatePath(requestedPath string) (string, error) {
	if len(v.allowedRoots) == 0 {
		return "", fmt.Errorf("no allowed paths configured")
	}

	// Resolve to absolute and clean
	absPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)

	// Check the path exists
	_, err = os.Lstat(absPath)
	if err != nil {
		return "", fmt.Errorf("path does not exist: %s", absPath)
	}

	// Resolve symlinks to get the real path for security checking
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %w", err)
	}
	realPath = filepath.Clean(realPath)

	// Check that the real path is inside at least one allowed root
	for _, root := range v.allowedRoots {
		if isInsideRoot(realPath, root) {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("path %s is outside all allowed roots", requestedPath)
}

// isInsideRoot checks if a path is inside or equal to the given root directory.
func isInsideRoot(path, root string) bool {
	if path == root {
		return true
	}
	// Ensure the root ends with separator for prefix matching
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}
