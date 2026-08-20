package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveWorkspace_FullChain tests the complete workspace resolution cascade:
// MCP roots → env var → default, and integration with Registry and DB path.
func TestResolveWorkspace_FullChain_EnvVar(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	// Simulate: no MCP roots available, fall back to env var
	wsRoot := filepath.Join(base, "myproject")
	_ = os.MkdirAll(wsRoot, 0755)

	wsID := ID(wsRoot)
	info, err := reg.Register(wsID, wsRoot)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Verify the chain: ID → Register → DBPath
	dbPath := reg.DBPath(wsID)
	expectedDB := filepath.Join(base, wsID, "tzro.db")
	if dbPath != expectedDB {
		t.Errorf("DBPath = %q, want %q", dbPath, expectedDB)
	}

	if info.RootPath != wsRoot {
		t.Errorf("RootPath = %q, want %q", info.RootPath, wsRoot)
	}
}

// TestResolveWorkspace_FullChain_Default tests the default/legacy fallback.
func TestResolveWorkspace_FullChain_Default(t *testing.T) {
	base := t.TempDir()
	reg := NewRegistry(base)

	// No roots, no env → default workspace
	wsID := ID("") // returns DefaultID
	if wsID != DefaultID {
		t.Fatalf("Expected DefaultID, got %q", wsID)
	}

	info, err := reg.Register(wsID, "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	dbPath := reg.DBPath(wsID)
	expectedDB := filepath.Join(base, DefaultID, "tzro.db")
	if dbPath != expectedDB {
		t.Errorf("DBPath = %q, want %q", dbPath, expectedDB)
	}

	if info.ID != DefaultID {
		t.Errorf("ID = %q, want %q", info.ID, DefaultID)
	}
}
