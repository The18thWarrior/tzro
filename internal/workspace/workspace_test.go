package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestID_EmptyPath_ReturnsDefault(t *testing.T) {
	got := ID("")
	if got != DefaultID {
		t.Errorf("ID(\"\") = %q, want %q", got, DefaultID)
	}
}

func TestID_DeterministicHash(t *testing.T) {
	path := "/Users/jp/repos/project"
	id1 := ID(path)
	id2 := ID(path)

	if id1 != id2 {
		t.Errorf("ID is not deterministic: %q != %q", id1, id2)
	}
	if len(id1) != 12 {
		t.Errorf("ID length = %d, want 12", len(id1))
	}
	// Verify it's hex
	for _, c := range id1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("ID contains non-hex char: %c in %q", c, id1)
		}
	}
}

func TestID_DifferentPathsDifferentIDs(t *testing.T) {
	idA := ID("/path/a")
	idB := ID("/path/b")
	if idA == idB {
		t.Errorf("Different paths produced same ID: %q", idA)
	}
}

func TestID_CanonicalizesPath(t *testing.T) {
	// Create a real temp dir so filepath.Clean works on actual paths
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "foo")
	_ = os.MkdirAll(dir, 0755)

	dotdot := filepath.Join(tmp, "foo", "..", "foo")
	id1 := ID(dotdot)
	id2 := ID(dir)
	if id1 != id2 {
		t.Errorf("ID(%q) = %q, ID(%q) = %q — should be equal after canonicalization", dotdot, id1, dir, id2)
	}
}

func TestID_TrailingSlashIgnored(t *testing.T) {
	id1 := ID("/tmp/foo/")
	id2 := ID("/tmp/foo")
	if id1 != id2 {
		t.Errorf("Trailing slash matters: ID(/tmp/foo/) = %q, ID(/tmp/foo) = %q", id1, id2)
	}
}
