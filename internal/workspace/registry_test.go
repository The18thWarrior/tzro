package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistry_Register_CreatesDirectoryAndJSON(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	info, err := reg.Register("abc123", "/some/path")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Directory exists
	dirPath := filepath.Join(base, "abc123")
	if fi, err := os.Stat(dirPath); err != nil || !fi.IsDir() {
		t.Errorf("Expected directory %s to exist", dirPath)
	}

	// workspace.json exists
	jsonPath := filepath.Join(dirPath, "workspace.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("Expected workspace.json at %s", jsonPath)
	}

	// Info fields
	if info.ID != "abc123" {
		t.Errorf("ID = %q, want %q", info.ID, "abc123")
	}
	if info.RootPath != "/some/path" {
		t.Errorf("RootPath = %q, want %q", info.RootPath, "/some/path")
	}
	if info.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}

	// DBPath returns expected path
	dbPath := reg.DBPath("abc123")
	expected := filepath.Join(base, "abc123", "tzro.db")
	if dbPath != expected {
		t.Errorf("DBPath = %q, want %q", dbPath, expected)
	}
}

func TestRegistry_Register_Idempotent(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	info1, err := reg.Register("abc123", "/some/path")
	if err != nil {
		t.Fatalf("First register: %v", err)
	}

	info2, err := reg.Register("abc123", "/some/path")
	if err != nil {
		t.Fatalf("Second register: %v", err)
	}

	if info1.CreatedAt != info2.CreatedAt {
		t.Error("CreatedAt changed on idempotent register")
	}
}

func TestRegistry_Get_ExistingWorkspace(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	_, err := reg.Register("ws1", "/path/to/project")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	info, err := reg.Get("ws1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.RootPath != "/path/to/project" {
		t.Errorf("RootPath = %q, want %q", info.RootPath, "/path/to/project")
	}
}

func TestRegistry_Get_NonExistent_ReturnsError(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	_, err := reg.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent workspace")
	}
}

func TestRegistry_List_ReturnsAll(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	_, _ = reg.Register("ws1", "/path/1")
	_, _ = reg.Register("ws2", "/path/2")
	_, _ = reg.Register("ws3", "/path/3")

	list, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("List returned %d workspaces, want 3", len(list))
	}
}

func TestRegistry_Touch_UpdatesLastActive(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	_, err := reg.Register("ws1", "/path/1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	info1, _ := reg.Get("ws1")
	time.Sleep(10 * time.Millisecond)

	err = reg.Touch("ws1")
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}

	info2, _ := reg.Get("ws1")
	if info2.LastActive <= info1.LastActive {
		t.Errorf("LastActive not updated: %d <= %d", info2.LastActive, info1.LastActive)
	}
}

func TestRegistry_DefaultWorkspace(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	info, err := reg.Register(DefaultID, "")
	if err != nil {
		t.Fatalf("Register default: %v", err)
	}
	if info.ID != DefaultID {
		t.Errorf("ID = %q, want %q", info.ID, DefaultID)
	}

	dirPath := filepath.Join(base, DefaultID)
	if fi, err := os.Stat(dirPath); err != nil || !fi.IsDir() {
		t.Errorf("Expected default workspace directory at %s", dirPath)
	}
}
