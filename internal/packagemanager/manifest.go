// Package packagemanager implements the Agent App lifecycle manager for .tzroapp archives.
// It handles manifest parsing, file extraction, SQL migration tracking, tool registration
// with app-scoped namespacing, and Procedural Micro-Skill indexing.
package packagemanager

import (
	"encoding/json"
	"fmt"
	"io"
)

// Manifest represents the parsed tzro.manifest.json from a .tzroapp archive.
type Manifest struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Capabilities []string       `json:"capabilities"`
	Tools        []ManifestTool `json:"tools"`
	Prompts      []string       `json:"prompts"`
	Migrations   []string       `json:"migrations"`
	MCP          *MCPServerDef  `json:"mcp,omitempty"`
}

// ManifestTool declares a tool bundled inside the .tzroapp archive.
type ManifestTool struct {
	Name string `json:"name"`
	Type string `json:"type"` // "wasm" or "mcp"
	Path string `json:"path"` // relative path within the archive
}

// MCPServerDef declares an MCP server configuration for the app.
type MCPServerDef struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// ParseManifest decodes and validates a tzro.manifest.json from the given reader.
func ParseManifest(r io.Reader) (*Manifest, error) {
	var m Manifest
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid manifest JSON: %w", err)
	}

	if m.ID == "" {
		return nil, fmt.Errorf("manifest validation failed: 'id' is required")
	}
	if len(m.Tools) == 0 {
		return nil, fmt.Errorf("manifest validation failed: at least one tool is required (toolless packages are not Agent Apps)")
	}
	if m.Version == "" {
		m.Version = "0.0.0"
	}

	return &m, nil
}
