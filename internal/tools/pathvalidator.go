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
// Returns a deduplicated union of:
//  1. allowedPaths from .tzro/mcp_config.json (if present)
//  2. TZRO_DIR env var (always included if set, as a safety net)
//  3. cwd (last resort, only if nothing else resolves)
func GetAllowedPaths() []string {
	seen := make(map[string]bool)
	var paths []string

	addPath := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		cleaned := filepath.Clean(abs)
		if real, err := filepath.EvalSymlinks(cleaned); err == nil {
			cleaned = filepath.Clean(real)
		}
		if !seen[cleaned] {
			seen[cleaned] = true
			paths = append(paths, cleaned)
		}
	}

	// 1. Try reading allowedPaths from MCP config
	mcpConfigPath := config.ResolvePath("mcp_config.json")
	if data, err := os.ReadFile(mcpConfigPath); err == nil {
		var mcpCfg struct {
			AllowedPaths []string `json:"allowedPaths"`
		}
		if json.Unmarshal(data, &mcpCfg) == nil {
			for _, p := range mcpCfg.AllowedPaths {
				addPath(p)
			}
		}
	}

	// 2. Always include TZRO_DIR as a guaranteed root (safety net)
	if tzroDir := os.Getenv("TZRO_DIR"); tzroDir != "" {
		addPath(tzroDir)
	}

	// 3. Last resort: current working directory (only if nothing else resolved)
	if len(paths) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			addPath(cwd)
		}
	}

	return paths
}

// PathValidator enforces a security boundary for filesystem tools.
// All requested paths must resolve to a location inside one of the allowed roots.
// It rejects path traversal (../) and symlinks that escape the allowed roots.
type PathValidator struct {
	// staticRoots are roots provided at construction time (may be empty for dynamic-only mode).
	staticRoots []string
	// dynamic controls whether GetAllowedPaths() is re-evaluated on each call.
	dynamic bool
}

// NewPathValidator creates a validator with the given allowed root directories.
// Each root is resolved to an absolute, cleaned path.
// The validator also dynamically re-reads GetAllowedPaths() on each validation
// to pick up config changes without requiring a restart.
func NewPathValidator(allowedRoots []string) *PathValidator {
	cleaned := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		c := filepath.Clean(abs)
		if real, err := filepath.EvalSymlinks(c); err == nil {
			c = filepath.Clean(real)
		}
		cleaned = append(cleaned, c)
	}
	return &PathValidator{staticRoots: cleaned, dynamic: true}
}

// NewStaticPathValidator creates a validator that only checks the given roots
// without dynamically re-reading GetAllowedPaths(). Use in tests where
// dynamic resolution would break isolation.
func NewStaticPathValidator(allowedRoots []string) *PathValidator {
	v := NewPathValidator(allowedRoots)
	v.dynamic = false
	return v
}

// resolveRoots returns the effective allowed roots by merging static roots
// with dynamically resolved paths (if dynamic mode is enabled).
func (v *PathValidator) resolveRoots() []string {
	if !v.dynamic {
		return v.staticRoots
	}

	seen := make(map[string]bool)
	var merged []string

	for _, r := range v.staticRoots {
		if !seen[r] {
			seen[r] = true
			merged = append(merged, r)
		}
	}

	for _, r := range GetAllowedPaths() {
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		cleaned := filepath.Clean(abs)
		if !seen[cleaned] {
			seen[cleaned] = true
			merged = append(merged, cleaned)
		}
	}

	return merged
}

// ValidatePath checks that the requested path exists and resolves to a location
// inside one of the allowed roots. It resolves symlinks to detect escapes.
// Returns the resolved absolute path on success.
func (v *PathValidator) ValidatePath(requestedPath string) (string, error) {
	roots := v.resolveRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("no allowed paths configured")
	}

	// Resolve to absolute and clean
	absPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)

	// Check the path exists. If it doesn't and the request was a relative path,
	// try resolving against each allowed root. Local models commonly emit bare
	// relative paths like "cmd" or "internal/tools" which should resolve against
	// the project root (an allowed root), not the daemon's working directory.
	if _, statErr := os.Lstat(absPath); statErr != nil {
		if !filepath.IsAbs(requestedPath) {
			resolved := false
			for _, root := range roots {
				candidate := filepath.Clean(filepath.Join(root, requestedPath))
				if _, err2 := os.Lstat(candidate); err2 == nil {
					absPath = candidate
					resolved = true
					break
				}
			}
			if !resolved {
				return "", fmt.Errorf("path does not exist: %s (also tried relative to allowed roots)", requestedPath)
			}
		} else {
			return "", fmt.Errorf("path does not exist: %s", absPath)
		}
	}

	// Resolve symlinks to get the real path for security checking
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks: %w", err)
	}
	realPath = filepath.Clean(realPath)

	// Check that the real path is inside at least one allowed root
	for _, root := range roots {
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

// ValidateWritePath validates a path for write operations. Unlike ValidatePath,
// it does not require the file to already exist — it checks that the resolved
// absolute path falls within allowed roots. If the file exists, symlinks are
// resolved for security. If it doesn't exist, the parent directory is validated.
func (v *PathValidator) ValidateWritePath(requestedPath string) (string, error) {
	roots := v.resolveRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("no allowed paths configured")
	}

	// Resolve to absolute and clean
	absPath, err := filepath.Abs(requestedPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	absPath = filepath.Clean(absPath)

	// If the file exists, resolve symlinks for security
	if _, statErr := os.Lstat(absPath); statErr == nil {
		realPath, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve symlinks: %w", err)
		}
		realPath = filepath.Clean(realPath)
		for _, root := range roots {
			if isInsideRoot(realPath, root) {
				return absPath, nil
			}
		}
		return "", fmt.Errorf("path %s is outside all allowed roots", requestedPath)
	}

	// File doesn't exist — resolve symlinks on the nearest existing ancestor directory
	// so that temp directories (e.g. /var -> /private/var) match allowed roots correctly.
	checkPath := absPath
	parent := filepath.Dir(absPath)
	for parent != "" && parent != "/" && parent != "." {
		if _, err := os.Stat(parent); err == nil {
			if realParent, err := filepath.EvalSymlinks(parent); err == nil {
				rel, _ := filepath.Rel(parent, absPath)
				checkPath = filepath.Clean(filepath.Join(realParent, rel))
			}
			break
		}
		parent = filepath.Dir(parent)
	}

	for _, root := range roots {
		if isInsideRoot(checkPath, root) {
			return checkPath, nil
		}
		if isInsideRoot(absPath, root) {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("path %s is outside all allowed roots", requestedPath)
}
