package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFromCwd_FindsGitRoot(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0755)
	_ = os.MkdirAll(filepath.Join(repo, "src", "pkg"), 0755)

	got := ResolveFromCwd(filepath.Join(repo, "src", "pkg"))
	// Canonicalize expected
	realRepo, _ := filepath.EvalSymlinks(repo)
	if got != realRepo && got != repo {
		t.Errorf("ResolveFromCwd = %q, want %q", got, repo)
	}
}

func TestResolveFromCwd_NoGitRoot(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "norepo", "src")
	_ = os.MkdirAll(dir, 0755)

	got := ResolveFromCwd(dir)
	realDir, _ := filepath.EvalSymlinks(dir)
	if got != realDir && got != dir {
		t.Errorf("ResolveFromCwd = %q, want %q (input dir itself when no .git)", got, dir)
	}
}

func TestResolveFromCwd_AtGitRoot(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(filepath.Join(repo, ".git"), 0755)

	got := ResolveFromCwd(repo)
	realRepo, _ := filepath.EvalSymlinks(repo)
	if got != realRepo && got != repo {
		t.Errorf("ResolveFromCwd = %q, want %q", got, repo)
	}
}
