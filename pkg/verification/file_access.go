package verification

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceFileAccess provides filesystem operations bound to a workspace root.
type WorkspaceFileAccess struct {
	workspace string
}

// NewWorkspaceFileAccess creates a file access implementation that restricts
// operations to the given workspace directory.
func NewWorkspaceFileAccess(workspace string) (FileAccess, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve workspace: %w", err)
	}
	// Resolve symlinks to get the canonical path (e.g., /var → /private/var on macOS).
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve workspace symlinks: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return &WorkspaceFileAccess{workspace: abs}, nil
}

// resolveSafe resolves a workspace-relative path and verifies it stays inside
// the workspace, including symlink resolution.
func (w *WorkspaceFileAccess) resolveSafe(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute path not allowed: %s", relPath)
	}
	cleaned := filepath.Clean(relPath)
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path escapes workspace: %s", relPath)
	}

	abs := filepath.Join(w.workspace, cleaned)

	// Resolve parent symlinks to detect escape.
	dir := filepath.Dir(abs)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		if !strings.HasPrefix(resolved, w.workspace) {
			return "", fmt.Errorf("symlink target escapes workspace: %s", relPath)
		}
	}

	return abs, nil
}

// Read returns the current state of a workspace-relative file.
func (w *WorkspaceFileAccess) Read(_ context.Context, path string) (FileState, error) {
	abs, err := w.resolveSafe(path)
	if err != nil {
		return FileState{}, err
	}

	info, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return FileState{Exists: false}, nil
	}
	if err != nil {
		return FileState{}, fmt.Errorf("cannot stat %s: %w", path, err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return FileState{}, fmt.Errorf("cannot read %s: %w", path, err)
	}

	return FileState{
		Exists: true,
		Bytes:  data,
		Mode:   info.Mode(),
	}, nil
}

// Write creates or updates a file at the workspace-relative path.
// It creates parent directories as needed while preserving the workspace boundary.
func (w *WorkspaceFileAccess) Write(_ context.Context, path string, state FileState) error {
	abs, err := w.resolveSafe(path)
	if err != nil {
		return err
	}

	// Create parent directories.
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cannot create directory for %s: %w", path, err)
	}

	mode := state.Mode
	if mode == 0 {
		mode = 0644
	}

	if err := os.WriteFile(abs, state.Bytes, mode); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}
