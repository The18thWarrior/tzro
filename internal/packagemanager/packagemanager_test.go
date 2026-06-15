package packagemanager

import (
	"archive/zip"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"tzro/internal/mcp"
	"tzro/internal/tools"

	_ "modernc.org/sqlite"
)

func TestParseManifest_ValidManifest(t *testing.T) {
	manifestJSON := `{
		"id": "hubspot",
		"name": "HubSpot CRM Integration",
		"version": "1.0.0",
		"capabilities": ["network_outbound", "database_write"],
		"tools": [
			{"name": "create_contact", "type": "wasm", "path": "wasm/create_contact.wasm"},
			{"name": "search_contacts", "type": "wasm", "path": "wasm/search_contacts.wasm"}
		],
		"prompts": ["prompts/create-contact-sop.md"],
		"migrations": ["db/001_create_contacts.sql"],
		"mcp": {
			"command": "node",
			"args": ["server.js"],
			"env": {"HUBSPOT_API_KEY": "$HUBSPOT_API_KEY"}
		}
	}`

	f, err := os.CreateTemp(t.TempDir(), "manifest-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(manifestJSON)
	f.Close()

	file, _ := os.Open(f.Name())
	defer file.Close()

	manifest, err := ParseManifest(file)
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}

	if manifest.ID != "hubspot" {
		t.Errorf("expected ID 'hubspot', got '%s'", manifest.ID)
	}
	if manifest.Name != "HubSpot CRM Integration" {
		t.Errorf("expected Name 'HubSpot CRM Integration', got '%s'", manifest.Name)
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("expected Version '1.0.0', got '%s'", manifest.Version)
	}
	if len(manifest.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(manifest.Capabilities))
	}
	if len(manifest.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(manifest.Tools))
	}
	if manifest.Tools[0].Name != "create_contact" {
		t.Errorf("expected first tool name 'create_contact', got '%s'", manifest.Tools[0].Name)
	}
	if manifest.Tools[0].Type != "wasm" {
		t.Errorf("expected first tool type 'wasm', got '%s'", manifest.Tools[0].Type)
	}
	if len(manifest.Prompts) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(manifest.Prompts))
	}
	if len(manifest.Migrations) != 1 {
		t.Errorf("expected 1 migration, got %d", len(manifest.Migrations))
	}
	if manifest.MCP == nil {
		t.Fatal("expected MCP config to be set")
	}
	if manifest.MCP.Command != "node" {
		t.Errorf("expected MCP command 'node', got '%s'", manifest.MCP.Command)
	}
}

func TestParseManifest_MissingID(t *testing.T) {
	manifestJSON := `{
		"name": "No ID App",
		"version": "1.0.0",
		"tools": [{"name": "foo", "type": "wasm", "path": "wasm/foo.wasm"}]
	}`

	f := createTempManifest(t, manifestJSON)
	file, _ := os.Open(f)
	defer file.Close()

	_, err := ParseManifest(file)
	if err == nil {
		t.Fatal("expected error for missing ID, got nil")
	}
}

func TestParseManifest_EmptyTools(t *testing.T) {
	manifestJSON := `{
		"id": "empty",
		"name": "Empty Tools App",
		"version": "1.0.0",
		"tools": []
	}`

	f := createTempManifest(t, manifestJSON)
	file, _ := os.Open(f)
	defer file.Close()

	_, err := ParseManifest(file)
	if err == nil {
		t.Fatal("expected error for empty tools, got nil")
	}
}

func TestParseManifest_NoToolsField(t *testing.T) {
	manifestJSON := `{
		"id": "notool",
		"name": "No Tools App",
		"version": "1.0.0"
	}`

	f := createTempManifest(t, manifestJSON)
	file, _ := os.Open(f)
	defer file.Close()

	_, err := ParseManifest(file)
	if err == nil {
		t.Fatal("expected error for missing tools field, got nil")
	}
}

func createTempManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tzro.manifest.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Install tests ---

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	tempDir := t.TempDir()
	appsDir := filepath.Join(tempDir, "apps")

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	mcpReg := &mcp.MCPRegistry{}
	mcpReg.InitForTesting()

	mgr := NewManager(db, mcpReg, appsDir)
	if err := mgr.InitSchema(); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	return mgr, tempDir
}

func buildTestArchive(t *testing.T, manifest string, extraFiles map[string]string) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "test.tzroapp")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)

	// Write manifest
	mf, _ := w.Create("tzro.manifest.json")
	mf.Write([]byte(manifest))

	// Write extra files
	for name, content := range extraFiles {
		ef, _ := w.Create(name)
		ef.Write([]byte(content))
	}

	w.Close()
	return archivePath
}

func TestInstall_ExtractsFilesToCorrectDirectory(t *testing.T) {
	mgr, tempDir := newTestManager(t)

	manifest := `{
		"id": "hubspot",
		"name": "HubSpot",
		"version": "1.0.0",
		"capabilities": ["network_outbound"],
		"tools": [{"name": "create_contact", "type": "wasm", "path": "wasm/create_contact.wasm"}]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/create_contact.wasm": "fake-wasm-binary",
		"wasm/create_contact.json": `{"type":"object","properties":{}}`,
	})

	app, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	if app.ID != "hubspot" {
		t.Errorf("expected app ID 'hubspot', got '%s'", app.ID)
	}
	if app.Status != "active" {
		t.Errorf("expected app status 'active', got '%s'", app.Status)
	}

	// Verify directory structure
	appsDir := filepath.Join(tempDir, "apps")
	appDir := filepath.Join(appsDir, "hubspot")
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		t.Errorf("expected app directory to exist: %s", appDir)
	}

	wasmFile := filepath.Join(appDir, "wasm", "create_contact.wasm")
	if _, err := os.Stat(wasmFile); os.IsNotExist(err) {
		t.Errorf("expected WASM file to be extracted: %s", wasmFile)
	}
}

func TestInstall_RejectsDuplicateAppID(t *testing.T) {
	mgr, _ := newTestManager(t)

	manifest := `{
		"id": "hubspot",
		"name": "HubSpot",
		"version": "1.0.0",
		"tools": [{"name": "create_contact", "type": "wasm", "path": "wasm/create_contact.wasm"}]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/create_contact.wasm": "fake-wasm-binary",
		"wasm/create_contact.json": `{"type":"object","properties":{}}`,
	})

	// First install succeeds
	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("First install failed: %v", err)
	}

	// Second install with same ID fails
	_, err = mgr.Install(archive)
	if err == nil {
		t.Fatal("expected error for duplicate app ID, got nil")
	}
}

func TestInstall_RunsSQLMigrations(t *testing.T) {
	mgr, _ := newTestManager(t)

	manifest := `{
		"id": "myapp",
		"name": "My App",
		"version": "1.0.0",
		"tools": [{"name": "do_thing", "type": "wasm", "path": "wasm/do_thing.wasm"}],
		"migrations": ["db/001_create_items.sql"]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/do_thing.wasm":      "fake",
		"db/001_create_items.sql": "CREATE TABLE IF NOT EXISTS myapp_items (id TEXT PRIMARY KEY, name TEXT);",
	})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify the table was created
	var tableName string
	err = mgr.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='myapp_items'").Scan(&tableName)
	if err != nil {
		t.Fatalf("Migration table not created: %v", err)
	}

	// Verify migration was recorded in _tzro_migrations
	var migFile string
	err = mgr.db.QueryRow("SELECT migration_file FROM _tzro_migrations WHERE app_id = 'myapp'").Scan(&migFile)
	if err != nil {
		t.Fatalf("Migration not recorded: %v", err)
	}
	if migFile != "db/001_create_items.sql" {
		t.Errorf("expected migration file 'db/001_create_items.sql', got '%s'", migFile)
	}
}

func TestInstall_SkipsAlreadyAppliedMigrations(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Pre-seed a migration record
	mgr.db.Exec("INSERT INTO _tzro_migrations (app_id, migration_file, applied_at) VALUES ('myapp2', 'db/001_init.sql', 1000)")

	manifest := `{
		"id": "myapp2",
		"name": "My App 2",
		"version": "1.0.0",
		"tools": [{"name": "widget", "type": "wasm", "path": "wasm/widget.wasm"}],
		"migrations": ["db/001_init.sql", "db/002_add_col.sql"]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/widget.wasm":   "fake",
		"db/001_init.sql":    "CREATE TABLE IF NOT EXISTS myapp2_data (id TEXT PRIMARY KEY);",
		"db/002_add_col.sql": "ALTER TABLE myapp2_data ADD COLUMN label TEXT;",
	})

	// The first migration is already applied, so Install should only run 002
	// But 002 depends on 001 table existing — we need to create it first via the already-applied marker
	mgr.db.Exec("CREATE TABLE IF NOT EXISTS myapp2_data (id TEXT PRIMARY KEY)")

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify both migrations recorded (one pre-seeded, one new)
	var count int
	mgr.db.QueryRow("SELECT COUNT(*) FROM _tzro_migrations WHERE app_id = 'myapp2'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 migration records, got %d", count)
	}
}

func TestInstall_RegistersWASMToolsWithNamespace(t *testing.T) {
	mgr, _ := newTestManager(t)

	manifest := `{
		"id": "hubspot",
		"name": "HubSpot",
		"version": "1.0.0",
		"tools": [
			{"name": "create_contact", "type": "wasm", "path": "wasm/create_contact.wasm"},
			{"name": "list_deals", "type": "wasm", "path": "wasm/list_deals.wasm"}
		]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/create_contact.wasm": "fake",
		"wasm/create_contact.json": `{"type":"object","properties":{}}`,
		"wasm/list_deals.wasm":     "fake",
		"wasm/list_deals.json":     `{"type":"object","properties":{}}`,
	})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify tools are registered with namespaced names
	tool1 := tools.GetTool("hubspot_create_contact")
	if tool1 == nil {
		t.Error("expected tool 'hubspot_create_contact' to be registered")
	}

	tool2 := tools.GetTool("hubspot_list_deals")
	if tool2 == nil {
		t.Error("expected tool 'hubspot_list_deals' to be registered")
	}

	// Verify the un-namespaced name is NOT registered
	rawTool := tools.GetTool("create_contact")
	if rawTool != nil {
		t.Error("expected un-namespaced tool 'create_contact' to NOT be registered")
	}

	// Cleanup
	tools.Unregister("hubspot_create_contact")
	tools.Unregister("hubspot_list_deals")
}

func TestInstall_RegistersMCPDaemonIncrementally(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Register a pre-existing daemon
	mgr.mcpReg.RegisterDaemon("existing_daemon", mcp.MCPServerConfig{
		Command: "echo",
		Args:    []string{"hello"},
	})

	manifest := `{
		"id": "slackbot",
		"name": "Slack Bot",
		"version": "1.0.0",
		"tools": [{"name": "send_msg", "type": "mcp", "path": ""}],
		"mcp": {
			"command": "node",
			"args": ["slack-server.js"],
			"env": {"SLACK_TOKEN": "xoxb-123"}
		}
	}`

	archive := buildTestArchive(t, manifest, map[string]string{})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify new daemon was registered
	_, found := mgr.mcpReg.GetDaemon("slackbot_mcp")
	if !found {
		t.Error("expected MCP daemon 'slackbot_mcp' to be registered")
	}

	// Verify pre-existing daemon is still there
	_, existingFound := mgr.mcpReg.GetDaemon("existing_daemon")
	if !existingFound {
		t.Error("expected pre-existing daemon 'existing_daemon' to still be registered")
	}
}

func TestUninstall_DeregistersToolsAndMarksInactive(t *testing.T) {
	mgr, _ := newTestManager(t)

	manifest := `{
		"id": "hubspot",
		"name": "HubSpot",
		"version": "1.0.0",
		"tools": [{"name": "create_contact", "type": "wasm", "path": "wasm/create_contact.wasm"}]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/create_contact.wasm": "fake",
		"wasm/create_contact.json": `{"type":"object","properties":{}}`,
	})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify tool is registered
	if tools.GetTool("hubspot_create_contact") == nil {
		t.Fatal("expected tool to be registered before uninstall")
	}

	err = mgr.Uninstall("hubspot")
	if err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// Verify tool is deregistered
	if tools.GetTool("hubspot_create_contact") != nil {
		t.Error("expected tool to be deregistered after uninstall")
	}

	// Verify app status is inactive
	var status string
	mgr.db.QueryRow("SELECT status FROM _tzro_apps WHERE id = 'hubspot'").Scan(&status)
	if status != "inactive" {
		t.Errorf("expected app status 'inactive', got '%s'", status)
	}
}

func TestUninstall_PreservesData(t *testing.T) {
	mgr, tempDir := newTestManager(t)

	manifest := `{
		"id": "myapp",
		"name": "My App",
		"version": "1.0.0",
		"tools": [{"name": "widget", "type": "wasm", "path": "wasm/widget.wasm"}],
		"migrations": ["db/001_create_items.sql"]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/widget.wasm":        "fake",
		"db/001_create_items.sql": "CREATE TABLE IF NOT EXISTS myapp_items (id TEXT PRIMARY KEY, name TEXT);",
	})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Insert data into the migration-created table
	mgr.db.Exec("INSERT INTO myapp_items (id, name) VALUES ('item1', 'Widget A')")

	err = mgr.Uninstall("myapp")
	if err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}

	// Verify data is preserved
	var name string
	err = mgr.db.QueryRow("SELECT name FROM myapp_items WHERE id = 'item1'").Scan(&name)
	if err != nil {
		t.Fatalf("data should be preserved after uninstall: %v", err)
	}
	if name != "Widget A" {
		t.Errorf("expected 'Widget A', got '%s'", name)
	}

	// Verify app directory still exists
	appDir := filepath.Join(tempDir, "apps", "myapp")
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		t.Error("expected app directory to be preserved after uninstall")
	}
}

func TestPurge_RemovesEverything(t *testing.T) {
	mgr, tempDir := newTestManager(t)

	manifest := `{
		"id": "myapp",
		"name": "My App",
		"version": "1.0.0",
		"tools": [{"name": "widget", "type": "wasm", "path": "wasm/widget.wasm"}],
		"migrations": ["db/001_create_items.sql"]
	}`

	archive := buildTestArchive(t, manifest, map[string]string{
		"wasm/widget.wasm":        "fake",
		"db/001_create_items.sql": "CREATE TABLE IF NOT EXISTS myapp_items (id TEXT PRIMARY KEY, name TEXT);",
	})

	_, err := mgr.Install(archive)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	err = mgr.Purge("myapp")
	if err != nil {
		t.Fatalf("Purge failed: %v", err)
	}

	// Verify table was dropped
	var tableName string
	err = mgr.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='myapp_items'").Scan(&tableName)
	if err == nil {
		t.Error("expected myapp_items table to be dropped after purge")
	}

	// Verify app directory removed
	appDir := filepath.Join(tempDir, "apps", "myapp")
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Error("expected app directory to be removed after purge")
	}

	// Verify migration records cleaned
	var migCount int
	mgr.db.QueryRow("SELECT COUNT(*) FROM _tzro_migrations WHERE app_id = 'myapp'").Scan(&migCount)
	if migCount != 0 {
		t.Errorf("expected 0 migration records after purge, got %d", migCount)
	}

	// Verify app record removed
	var appCount int
	mgr.db.QueryRow("SELECT COUNT(*) FROM _tzro_apps WHERE id = 'myapp'").Scan(&appCount)
	if appCount != 0 {
		t.Errorf("expected 0 app records after purge, got %d", appCount)
	}
}

func TestList_ReturnsAllInstalledApps(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Install two apps
	manifest1 := `{
		"id": "app1",
		"name": "App One",
		"version": "1.0.0",
		"capabilities": ["network_outbound"],
		"tools": [{"name": "tool1", "type": "wasm", "path": "wasm/tool1.wasm"}]
	}`
	manifest2 := `{
		"id": "app2",
		"name": "App Two",
		"version": "2.0.0",
		"capabilities": ["database_write", "compute"],
		"tools": [{"name": "tool2", "type": "wasm", "path": "wasm/tool2.wasm"}]
	}`

	archive1 := buildTestArchive(t, manifest1, map[string]string{"wasm/tool1.wasm": "fake"})
	archive2 := buildTestArchive(t, manifest2, map[string]string{"wasm/tool2.wasm": "fake"})

	mgr.Install(archive1)
	mgr.Install(archive2)

	apps, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}

	// Verify both apps are present (order by installed_at DESC)
	foundApp1, foundApp2 := false, false
	for _, app := range apps {
		if app.ID == "app1" {
			foundApp1 = true
			if app.Status != "active" {
				t.Errorf("expected app1 status 'active', got '%s'", app.Status)
			}
		}
		if app.ID == "app2" {
			foundApp2 = true
			if len(app.Capabilities) != 2 {
				t.Errorf("expected 2 capabilities for app2, got %d", len(app.Capabilities))
			}
		}
	}
	if !foundApp1 || !foundApp2 {
		t.Errorf("expected both apps to be listed: foundApp1=%v, foundApp2=%v", foundApp1, foundApp2)
	}

	// Uninstall one and verify list still contains both
	mgr.Uninstall("app1")
	apps, _ = mgr.List()
	for _, app := range apps {
		if app.ID == "app1" && app.Status != "inactive" {
			t.Errorf("expected app1 to be 'inactive' after uninstall, got '%s'", app.Status)
		}
	}

	// Cleanup
	tools.Unregister("app1_tool1")
	tools.Unregister("app2_tool2")
}

func TestMCPRegistry_RegisterDaemon(t *testing.T) {
	reg := &mcp.MCPRegistry{}
	reg.InitForTesting()

	err := reg.RegisterDaemon("test_daemon", mcp.MCPServerConfig{
		Command: "echo",
		Args:    []string{"hello"},
	})
	if err != nil {
		t.Fatalf("RegisterDaemon failed: %v", err)
	}

	// Verify daemon exists
	_, found := reg.GetDaemon("test_daemon")
	if !found {
		t.Error("expected daemon to be found after registration")
	}

	// Duplicate registration fails
	err = reg.RegisterDaemon("test_daemon", mcp.MCPServerConfig{Command: "echo"})
	if err == nil {
		t.Error("expected error for duplicate daemon registration")
	}
}

func TestMCPRegistry_UnregisterDaemon(t *testing.T) {
	reg := &mcp.MCPRegistry{}
	reg.InitForTesting()

	reg.RegisterDaemon("to_remove", mcp.MCPServerConfig{Command: "echo"})

	err := reg.UnregisterDaemon("to_remove")
	if err != nil {
		t.Fatalf("UnregisterDaemon failed: %v", err)
	}

	_, found := reg.GetDaemon("to_remove")
	if found {
		t.Error("expected daemon to be removed after unregistration")
	}

	// Removing non-existent daemon fails
	err = reg.UnregisterDaemon("nonexistent")
	if err == nil {
		t.Error("expected error for removing non-existent daemon")
	}
}
