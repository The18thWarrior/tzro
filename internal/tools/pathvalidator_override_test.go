package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetAllowedPathsOverride_OverridesDefault(t *testing.T) {
	// Clean state
	SetAllowedPathsOverride(nil)
	defer SetAllowedPathsOverride(nil)

	tmp := t.TempDir()
	t.Setenv("TZRO_DIR", tmp)

	// Without override, GetAllowedPaths returns TZRO_DIR
	paths := GetAllowedPaths()
	found := false
	realTmp, _ := filepath.EvalSymlinks(tmp)
	for _, p := range paths {
		if p == realTmp || p == tmp {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Before override: expected TZRO_DIR %q in paths %v", tmp, paths)
	}

	// Set override
	wsRoot := filepath.Join(tmp, "workspace-root")
	_ = os.MkdirAll(wsRoot, 0755)
	SetAllowedPathsOverride([]string{wsRoot})

	paths = GetAllowedPaths()
	realWsRoot, _ := filepath.EvalSymlinks(wsRoot)
	if len(paths) != 1 {
		t.Fatalf("After override: expected 1 path, got %d: %v", len(paths), paths)
	}
	if paths[0] != realWsRoot && paths[0] != wsRoot {
		t.Errorf("After override: expected %q, got %q", wsRoot, paths[0])
	}

	// Clear override
	SetAllowedPathsOverride(nil)
	paths = GetAllowedPaths()
	found = false
	for _, p := range paths {
		if p == realTmp || p == tmp {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("After clearing override: expected TZRO_DIR %q in paths %v", tmp, paths)
	}
}

func TestSetAllowedPathsOverride_MultiplePaths(t *testing.T) {
	defer SetAllowedPathsOverride(nil)

	tmp := t.TempDir()
	pathA := filepath.Join(tmp, "a")
	pathB := filepath.Join(tmp, "b")
	_ = os.MkdirAll(pathA, 0755)
	_ = os.MkdirAll(pathB, 0755)

	SetAllowedPathsOverride([]string{pathA, pathB})
	paths := GetAllowedPaths()
	if len(paths) != 2 {
		t.Fatalf("Expected 2 paths, got %d: %v", len(paths), paths)
	}
}

func TestPathValidator_UsesOverride(t *testing.T) {
	defer SetAllowedPathsOverride(nil)

	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "ws")
	_ = os.MkdirAll(wsRoot, 0755)
	_ = os.WriteFile(filepath.Join(wsRoot, "hello.txt"), []byte("hi"), 0644)

	otherRoot := filepath.Join(tmp, "other")
	_ = os.MkdirAll(otherRoot, 0755)
	_ = os.WriteFile(filepath.Join(otherRoot, "secret.txt"), []byte("no"), 0644)

	SetAllowedPathsOverride([]string{wsRoot})

	// Dynamic validator should pick up the override
	v := NewPathValidator(nil)

	// Path inside workspace root should succeed
	_, err := v.ValidatePath(filepath.Join(wsRoot, "hello.txt"))
	if err != nil {
		t.Errorf("ValidatePath inside workspace root should succeed: %v", err)
	}

	// Path outside workspace root should fail
	_, err = v.ValidatePath(filepath.Join(otherRoot, "secret.txt"))
	if err == nil {
		t.Error("ValidatePath outside workspace root should fail")
	}
}
