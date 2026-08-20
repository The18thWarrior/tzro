package workspace

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResolveFromRoots extracts the workspace root path from MCP Root objects.
// Returns the first file:// root's path as rootPath. Additional file:// roots
// are returned as extraPaths for allowedPaths expansion.
// Returns ("", nil) if no file:// roots are provided.
func ResolveFromRoots(roots []*mcp.Root) (rootPath string, extraPaths []string) {
	for _, root := range roots {
		if root == nil {
			continue
		}
		path := fileURIToPath(root.URI)
		if path == "" {
			continue
		}
		if rootPath == "" {
			rootPath = path
		} else {
			extraPaths = append(extraPaths, path)
		}
	}
	return rootPath, extraPaths
}

// ResolveFromEnv reads the TZRO_WORKSPACE environment variable.
// Returns "" if not set.
func ResolveFromEnv() string {
	return os.Getenv("TZRO_WORKSPACE")
}

// fileURIToPath converts a file:// URI to a local filesystem path.
// Returns "" for non-file URIs or on parse error.
func fileURIToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Path
}

// ResolveFromCwd walks up from the given directory to find a .git root.
// Returns the git root path if found, or dir itself (canonicalized) if no .git is found.
func ResolveFromCwd(dir string) string {
	// Canonicalize the input
	cleaned := filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = filepath.Clean(real)
	}

	current := cleaned
	for {
		gitPath := filepath.Join(current, ".git")
		if fi, err := os.Stat(gitPath); err == nil && fi.IsDir() {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root without finding .git
			return cleaned // return original dir
		}
		current = parent
	}
}
