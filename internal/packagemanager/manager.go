package packagemanager

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/tools"

	"github.com/google/uuid"
)

// InstalledApp represents an Agent App that has been installed via the Package Manager.
type InstalledApp struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Status       string   `json:"status"` // "active" | "inactive"
	InstalledAt  int64    `json:"installedAt"`
	Capabilities []string `json:"capabilities"`
}

// Manager handles the Agent App lifecycle: install, uninstall, list, purge.
type Manager struct {
	db      *sql.DB
	mcpReg  *mcp.MCPRegistry
	appsDir string
}

// NewManager creates a new Package Manager instance.
func NewManager(db *sql.DB, mcpReg *mcp.MCPRegistry, appsDir string) *Manager {
	return &Manager{
		db:      db,
		mcpReg:  mcpReg,
		appsDir: appsDir,
	}
}

// InitSchema creates the _tzro_apps and _tzro_migrations tables if they don't exist.
func (m *Manager) InitSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS _tzro_apps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			capabilities TEXT,
			installed_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS _tzro_migrations (
			app_id TEXT NOT NULL,
			migration_file TEXT NOT NULL,
			applied_at INTEGER NOT NULL,
			PRIMARY KEY (app_id, migration_file)
		)`,
	}
	for _, q := range queries {
		if _, err := m.db.Exec(q); err != nil {
			return fmt.Errorf("failed to create packagemanager schema: %w", err)
		}
	}
	return nil
}

// Install processes a .tzroapp archive: parses manifest, extracts files, runs migrations,
// registers tools, indexes prompts, and records the app in _tzro_apps.
func (m *Manager) Install(archivePath string) (*InstalledApp, error) {
	// 1. Open zip archive
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive: %w", err)
	}
	defer r.Close()

	// 2. Find and parse manifest
	manifest, err := m.findAndParseManifest(r)
	if err != nil {
		return nil, err
	}

	// 3. Check for duplicate app ID
	var count int
	err = m.db.QueryRow("SELECT COUNT(*) FROM _tzro_apps WHERE id = ?", manifest.ID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("failed to check app uniqueness: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("agent app '%s' is already installed", manifest.ID)
	}

	// 4. Extract files to .tzro/apps/{appId}/
	appDir := filepath.Join(m.appsDir, manifest.ID)
	if err := m.extractArchive(r, appDir); err != nil {
		return nil, fmt.Errorf("failed to extract archive: %w", err)
	}

	// 5. Run SQL migrations
	if err := m.runMigrations(manifest.ID, appDir, manifest.Migrations); err != nil {
		// Cleanup on failure
		os.RemoveAll(appDir)
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// 6. Register WASM tools with namespaced names
	m.registerWASMTools(manifest.ID, appDir, manifest.Tools)

	// 7. Register MCP daemon incrementally
	if manifest.MCP != nil {
		mcpConfig := mcp.MCPServerConfig{
			Command: manifest.MCP.Command,
			Args:    manifest.MCP.Args,
			Env:     manifest.MCP.Env,
		}
		daemonName := manifest.ID + "_mcp"
		if err := m.mcpReg.RegisterDaemon(daemonName, mcpConfig); err != nil {
			fmt.Printf("[PackageManager] Warning: failed to register MCP daemon for '%s': %v\n", manifest.ID, err)
		}
	}

	// 8. Index pre-authored Procedural Micro-Skills
	m.indexPrompts(manifest.ID, appDir, manifest.Prompts)

	// 9. Record the app in _tzro_apps
	now := time.Now().Unix()
	capsJSON := strings.Join(manifest.Capabilities, ",")
	_, err = m.db.Exec(
		"INSERT INTO _tzro_apps (id, name, version, status, capabilities, installed_at) VALUES (?, ?, ?, 'active', ?, ?)",
		manifest.ID, manifest.Name, manifest.Version, capsJSON, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to record app: %w", err)
	}

	return &InstalledApp{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Status:       "active",
		InstalledAt:  now,
		Capabilities: manifest.Capabilities,
	}, nil
}

// Uninstall soft-disables an app: deregisters tools, stops MCP daemons, marks inactive.
// Preserves data (tables, app directory).
func (m *Manager) Uninstall(appID string) error {
	// Check app exists
	var status string
	err := m.db.QueryRow("SELECT status FROM _tzro_apps WHERE id = ?", appID).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("agent app '%s' is not installed", appID)
	}
	if err != nil {
		return fmt.Errorf("failed to query app: %w", err)
	}
	if status == "inactive" {
		return fmt.Errorf("agent app '%s' is already uninstalled", appID)
	}

	// Deregister all tools with {appId}_ prefix
	for _, t := range tools.GetList() {
		if strings.HasPrefix(t.Name(), appID+"_") {
			tools.Unregister(t.Name())
		}
	}

	// Stop MCP daemon
	daemonName := appID + "_mcp"
	_ = m.mcpReg.UnregisterDaemon(daemonName)

	// Mark inactive
	_, err = m.db.Exec("UPDATE _tzro_apps SET status = 'inactive' WHERE id = ?", appID)
	return err
}

// Purge destructively removes an app: drops tables, removes directory, cleans migration records.
func (m *Manager) Purge(appID string) error {
	// Uninstall first if active
	var status string
	err := m.db.QueryRow("SELECT status FROM _tzro_apps WHERE id = ?", appID).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("agent app '%s' is not installed", appID)
	}
	if err != nil {
		return fmt.Errorf("failed to query app: %w", err)
	}
	if status == "active" {
		if err := m.Uninstall(appID); err != nil {
			return fmt.Errorf("failed to uninstall before purge: %w", err)
		}
	}

	// Drop migration-created tables (tracked by convention: tables prefixed with appId_)
	rows, err := m.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE ?", appID+"_%")
	if err == nil {
		defer rows.Close()
		var tablesToDrop []string
		for rows.Next() {
			var tableName string
			if rows.Scan(&tableName) == nil {
				tablesToDrop = append(tablesToDrop, tableName)
			}
		}
		for _, table := range tablesToDrop {
			m.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		}
	}

	// Remove app directory
	appDir := filepath.Join(m.appsDir, appID)
	os.RemoveAll(appDir)

	// Clean migration records
	m.db.Exec("DELETE FROM _tzro_migrations WHERE app_id = ?", appID)

	// Remove app record
	_, err = m.db.Exec("DELETE FROM _tzro_apps WHERE id = ?", appID)
	return err
}

// List returns all installed apps with their current status.
func (m *Manager) List() ([]InstalledApp, error) {
	rows, err := m.db.Query("SELECT id, name, version, status, capabilities, installed_at FROM _tzro_apps ORDER BY installed_at DESC")
	if err != nil {
		return nil, fmt.Errorf("failed to query apps: %w", err)
	}
	defer rows.Close()

	var apps []InstalledApp
	for rows.Next() {
		var app InstalledApp
		var capsStr sql.NullString
		if err := rows.Scan(&app.ID, &app.Name, &app.Version, &app.Status, &capsStr, &app.InstalledAt); err != nil {
			return nil, fmt.Errorf("failed to scan app: %w", err)
		}
		if capsStr.Valid && capsStr.String != "" {
			app.Capabilities = strings.Split(capsStr.String, ",")
		}
		apps = append(apps, app)
	}
	if apps == nil {
		apps = []InstalledApp{}
	}
	return apps, nil
}

// --- Internal helpers ---

func (m *Manager) findAndParseManifest(r *zip.ReadCloser) (*Manifest, error) {
	for _, f := range r.File {
		if f.Name == "tzro.manifest.json" || filepath.Base(f.Name) == "tzro.manifest.json" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open manifest: %w", err)
			}
			defer rc.Close()
			return ParseManifest(rc)
		}
	}
	return nil, fmt.Errorf("archive does not contain tzro.manifest.json")
}

func (m *Manager) extractArchive(r *zip.ReadCloser, destDir string) error {
	for _, f := range r.File {
		destPath := filepath.Join(destDir, f.Name)

		// Security: prevent zip-slip attacks
		if !strings.HasPrefix(filepath.Clean(destPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		// Create parent directories
		os.MkdirAll(filepath.Dir(destPath), 0755)

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", f.Name, err)
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to create file %s: %w", destPath, err)
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return fmt.Errorf("failed to write file %s: %w", destPath, err)
		}
	}
	return nil
}

func (m *Manager) runMigrations(appID, appDir string, migrationPaths []string) error {
	if len(migrationPaths) == 0 {
		return nil
	}

	// Get already applied migrations
	applied := make(map[string]bool)
	rows, err := m.db.Query("SELECT migration_file FROM _tzro_migrations WHERE app_id = ?", appID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				applied[name] = true
			}
		}
	}

	for _, migPath := range migrationPaths {
		if applied[migPath] {
			continue
		}

		fullPath := filepath.Join(appDir, migPath)
		sqlBytes, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", migPath, err)
		}

		if _, err := m.db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("migration %s failed: %w", migPath, err)
		}

		// Record the migration
		m.db.Exec("INSERT INTO _tzro_migrations (app_id, migration_file, applied_at) VALUES (?, ?, ?)",
			appID, migPath, time.Now().Unix())
	}
	return nil
}

func (m *Manager) registerWASMTools(appID, appDir string, toolDefs []ManifestTool) {
	for _, td := range toolDefs {
		if td.Type != "wasm" {
			continue
		}

		namespacedName := appID + "_" + td.Name
		wasmPath := filepath.Join(appDir, td.Path)

		// Check schema file exists alongside wasm (same base name, .json extension)
		schemaPath := strings.TrimSuffix(wasmPath, filepath.Ext(wasmPath)) + ".json"

		// Register as a placeholder tool with the namespaced name
		// The actual WASM execution is handled by wasm.NewWasmToolAdapter in production,
		// but the schema must exist for registration.
		if _, err := os.Stat(schemaPath); err == nil {
			tools.Register(&tools.FunctionTool{
				NameVal:   namespacedName,
				SchemaVal: readFileOrDefault(schemaPath),
			})
		} else {
			// Register with minimal schema if no schema file found
			tools.Register(&tools.FunctionTool{
				NameVal:   namespacedName,
				SchemaVal: `{"type":"object","properties":{"tool_arguments":{"type":"object","properties":{}}},"required":["tool_arguments"]}`,
			})
		}
	}
}

func (m *Manager) indexPrompts(appID, appDir string, promptPaths []string) {
	if memory.DB == nil || memory.DB.RawDB() == nil {
		return
	}

	for _, promptPath := range promptPaths {
		fullPath := filepath.Join(appDir, promptPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			fmt.Printf("[PackageManager] Warning: failed to read prompt %s: %v\n", promptPath, err)
			continue
		}

		skillID := uuid.New().String()
		skill := memory.Skill{
			ID:                 skillID,
			Name:               fmt.Sprintf("[%s] %s", appID, filepath.Base(promptPath)),
			TriggerDescription: fmt.Sprintf("Agent App '%s' pre-authored SOP: %s", appID, filepath.Base(promptPath)),
			SOPContent:         string(content),
			CreatedAt:          time.Now().Unix(),
		}
		if err := memory.DB.AddSkill(&skill); err != nil {
			fmt.Printf("[PackageManager] Warning: failed to index prompt %s: %v\n", promptPath, err)
		}
	}
}

func readFileOrDefault(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return `{"type":"object","properties":{}}`
	}
	return string(data)
}

// LoadInstalledApps reads all active apps from the database and registers their tools and MCP daemons.
func (m *Manager) LoadInstalledApps() error {
	if err := m.InitSchema(); err != nil {
		return err
	}

	rows, err := m.db.Query("SELECT id FROM _tzro_apps WHERE status = 'active'")
	if err != nil {
		return fmt.Errorf("failed to query active apps: %w", err)
	}
	defer rows.Close()

	var appIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			appIDs = append(appIDs, id)
		}
	}

	for _, appID := range appIDs {
		appDir := filepath.Join(m.appsDir, appID)
		manifestPath := filepath.Join(appDir, "tzro.manifest.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			fmt.Printf("[PackageManager] Warning: failed to read manifest for installed app '%s': %v\n", appID, err)
			continue
		}

		manifest, err := ParseManifest(strings.NewReader(string(manifestData)))
		if err != nil {
			fmt.Printf("[PackageManager] Warning: failed to parse manifest for installed app '%s': %v\n", appID, err)
			continue
		}

		m.registerWASMTools(manifest.ID, appDir, manifest.Tools)

		if manifest.MCP != nil {
			mcpConfig := mcp.MCPServerConfig{
				Command: manifest.MCP.Command,
				Args:    manifest.MCP.Args,
				Env:     manifest.MCP.Env,
			}
			daemonName := manifest.ID + "_mcp"
			if err := m.mcpReg.RegisterDaemon(daemonName, mcpConfig); err != nil {
				fmt.Printf("[PackageManager] Warning: failed to re-register MCP daemon for '%s': %v\n", manifest.ID, err)
			}
		}
	}

	return nil
}
