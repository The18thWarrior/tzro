package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata/project")
	if err != nil {
		t.Fatalf("failed to resolve testdata path: %v", err)
	}
	return abs
}

func outsideDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata/outside")
	if err != nil {
		t.Fatalf("failed to resolve outside path: %v", err)
	}
	return abs
}

// Tracer bullet: a path inside the allowed root resolves successfully
func TestPathValidator_AcceptsPathInsideRoot(t *testing.T) {
	root := testdataDir(t)
	v := NewPathValidator([]string{root})

	resolved, err := v.ValidatePath(filepath.Join(root, "readme.txt"))
	if err != nil {
		t.Fatalf("expected valid path, got error: %v", err)
	}
	if resolved != filepath.Join(root, "readme.txt") {
		t.Errorf("expected resolved path %s, got %s", filepath.Join(root, "readme.txt"), resolved)
	}
}

func TestPathValidator_RejectsTraversalOutsideRoot(t *testing.T) {
	root := testdataDir(t)
	v := NewPathValidator([]string{root})

	// Try to escape via ../
	_, err := v.ValidatePath(filepath.Join(root, "..", "outside", "secret.txt"))
	if err == nil {
		t.Fatal("expected error for path traversal outside root, got nil")
	}
}

func TestPathValidator_RejectsSymlinkOutsideRoot(t *testing.T) {
	root := testdataDir(t)
	outsidePath := outsideDir(t)

	// Create a symlink inside the project pointing outside
	symlinkPath := filepath.Join(root, "escape_link")
	_ = os.Remove(symlinkPath) // clean up any prior run
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Skipf("cannot create symlinks on this OS: %v", err)
	}
	defer os.Remove(symlinkPath)

	v := NewPathValidator([]string{root})

	_, err := v.ValidatePath(filepath.Join(root, "escape_link", "secret.txt"))
	if err == nil {
		t.Fatal("expected error for symlink escaping root, got nil")
	}
}

func TestPathValidator_MultipleAllowedRoots(t *testing.T) {
	root := testdataDir(t)
	outside := outsideDir(t)

	// Both are allowed roots — access to both should succeed
	v := NewPathValidator([]string{root, outside})

	_, err := v.ValidatePath(filepath.Join(root, "readme.txt"))
	if err != nil {
		t.Fatalf("expected valid path inside first root, got error: %v", err)
	}

	_, err = v.ValidatePath(filepath.Join(outside, "secret.txt"))
	if err != nil {
		t.Fatalf("expected valid path inside second root, got error: %v", err)
	}
}

func TestPathValidator_ResolvesRelativePaths(t *testing.T) {
	root := testdataDir(t)
	v := NewPathValidator([]string{root})

	// Subdirectory relative to root
	resolved, err := v.ValidatePath(filepath.Join(root, "nested", "deep", "file.txt"))
	if err != nil {
		t.Fatalf("expected valid nested path, got error: %v", err)
	}
	expected := filepath.Join(root, "nested", "deep", "file.txt")
	if resolved != expected {
		t.Errorf("expected %s, got %s", expected, resolved)
	}
}

func TestPathValidator_RejectsNonExistentPath(t *testing.T) {
	root := testdataDir(t)
	v := NewPathValidator([]string{root})

	_, err := v.ValidatePath(filepath.Join(root, "does_not_exist.txt"))
	if err == nil {
		t.Fatal("expected error for non-existent path, got nil")
	}
}

func TestPathValidator_RejectsEmptyAllowedRoots(t *testing.T) {
	v := NewPathValidator(nil)

	_, err := v.ValidatePath("/some/path")
	if err == nil {
		t.Fatal("expected error when no allowed roots configured, got nil")
	}
}
