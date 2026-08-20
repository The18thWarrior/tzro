package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Info holds metadata for a registered workspace.
type Info struct {
	ID          string `json:"id"`
	RootPath    string `json:"root"`
	DisplayName string `json:"name"`
	CreatedAt   int64  `json:"created"`
	LastActive  int64  `json:"lastActive"`
}

// Registry manages workspace directories under a base path.
type Registry struct {
	baseDir string
}

// NewRegistry creates a registry rooted at baseDir (typically $TZRO_DIR/workspaces).
func NewRegistry(baseDir string) *Registry {
	return &Registry{baseDir: baseDir}
}

// Register creates a new workspace directory and workspace.json if it doesn't exist.
// Returns the Info. Idempotent — if already registered, returns existing info.
func (r *Registry) Register(id string, rootPath string) (*Info, error) {
	dir := filepath.Join(r.baseDir, id)
	jsonPath := filepath.Join(dir, "workspace.json")

	// If workspace.json already exists, return existing info
	if data, err := os.ReadFile(jsonPath); err == nil {
		var info Info
		if err := json.Unmarshal(data, &info); err == nil {
			return &info, nil
		}
	}

	// Create workspace directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create workspace dir: %w", err)
	}

	// Derive display name from root path
	displayName := filepath.Base(rootPath)
	if displayName == "." || displayName == "" {
		displayName = id
	}

	now := time.Now().UnixMilli()
	info := &Info{
		ID:          id,
		RootPath:    rootPath,
		DisplayName: displayName,
		CreatedAt:   now,
		LastActive:  now,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal workspace info: %w", err)
	}

	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write workspace.json: %w", err)
	}

	return info, nil
}

// Get returns Info for a workspace by ID, or error if not found.
func (r *Registry) Get(id string) (*Info, error) {
	jsonPath := filepath.Join(r.baseDir, id, "workspace.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("workspace %q not found: %w", id, err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse workspace.json: %w", err)
	}
	return &info, nil
}

// List returns all registered workspaces.
func (r *Registry) List() ([]*Info, error) {
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var workspaces []*Info
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := r.Get(entry.Name())
		if err != nil {
			continue // skip directories without valid workspace.json
		}
		workspaces = append(workspaces, info)
	}
	return workspaces, nil
}

// Touch updates the LastActive timestamp for a workspace.
func (r *Registry) Touch(id string) error {
	info, err := r.Get(id)
	if err != nil {
		return err
	}

	info.LastActive = time.Now().UnixMilli()

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace info: %w", err)
	}

	jsonPath := filepath.Join(r.baseDir, id, "workspace.json")
	return os.WriteFile(jsonPath, data, 0644)
}

// DBPath returns the path to a workspace's tzro.db file.
func (r *Registry) DBPath(id string) string {
	return filepath.Join(r.baseDir, id, "tzro.db")
}
