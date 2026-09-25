package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v\nOutput: %s", args, dir, err, string(out))
	}
	return string(out)
}

func TestGitHook_InstallRepeatAndUninstall(t *testing.T) {
	wsDir := t.TempDir()
	runGitCmd(t, wsDir, "init")
	runGitCmd(t, wsDir, "config", "user.email", "test@example.com")
	runGitCmd(t, wsDir, "config", "user.name", "Test User")

	// 1. Initial installation
	res, err := InstallGitHook(wsDir, "pre-commit", false)
	if err != nil {
		t.Fatalf("InstallGitHook failed: %v", err)
	}
	if res.Status != "installed" {
		t.Errorf("expected status 'installed', got %q", res.Status)
	}

	hookPath := res.HookPath
	fi, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook file does not exist: %v", err)
	}
	// Check executable permissions
	if fi.Mode()&0111 == 0 {
		t.Errorf("hook file is not executable: mode=%v", fi.Mode())
	}

	content, _ := os.ReadFile(hookPath)
	if !strings.Contains(string(content), TzroHookBeginMarker) {
		t.Errorf("hook file missing TzroHookBeginMarker")
	}

	// 2. Repeat installation (idempotent)
	res2, err := InstallGitHook(wsDir, "pre-commit", false)
	if err != nil {
		t.Fatalf("repeat InstallGitHook failed: %v", err)
	}
	if res2.Status != "updated" {
		t.Errorf("expected status 'updated' on repeat, got %q", res2.Status)
	}

	// 3. Uninstall
	unres, err := UninstallGitHook(wsDir, "pre-commit")
	if err != nil {
		t.Fatalf("UninstallGitHook failed: %v", err)
	}
	if unres.Status != "removed" {
		t.Errorf("expected status 'removed', got %q", unres.Status)
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Errorf("hook file was not deleted after uninstall")
	}
}

func TestGitHook_ExistingHookAndForceBackupRestore(t *testing.T) {
	wsDir := t.TempDir()
	runGitCmd(t, wsDir, "init")

	hooksDir, err := ResolveGitHooksDir(wsDir)
	if err != nil {
		t.Fatalf("ResolveGitHooksDir failed: %v", err)
	}
	_ = os.MkdirAll(hooksDir, 0755)

	targetPath := filepath.Join(hooksDir, "pre-commit")
	customHook := "#!/bin/sh\necho 'custom user hook'\nexit 0\n"
	_ = os.WriteFile(targetPath, []byte(customHook), 0755)

	// 1. Install without force: must leave intact and skip
	res, err := InstallGitHook(wsDir, "pre-commit", false)
	if err != nil {
		t.Fatalf("InstallGitHook returned error: %v", err)
	}
	if res.Status != "skipped" {
		t.Errorf("expected status 'skipped' when existing hook found, got %q", res.Status)
	}
	if res.ManualAdvice == "" {
		t.Errorf("expected manual advice for integration")
	}
	currContent, _ := os.ReadFile(targetPath)
	if string(currContent) != customHook {
		t.Errorf("existing hook was modified without force")
	}

	// 2. Install WITH force: creates backup and replaces
	resForce, err := InstallGitHook(wsDir, "pre-commit", true)
	if err != nil {
		t.Fatalf("InstallGitHook with force failed: %v", err)
	}
	if resForce.Status != "installed" {
		t.Errorf("expected status 'installed' with force, got %q", resForce.Status)
	}
	backupPath := targetPath + ".tzro.backup"
	bContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("expected backup file at %s: %v", backupPath, err)
	}
	if string(bContent) != customHook {
		t.Errorf("backup content mismatch: got %q, expected %q", string(bContent), customHook)
	}

	// 3. Uninstall: must restore backup
	unres, err := UninstallGitHook(wsDir, "pre-commit")
	if err != nil {
		t.Fatalf("UninstallGitHook failed: %v", err)
	}
	if unres.Status != "restored" {
		t.Errorf("expected status 'restored', got %q", unres.Status)
	}
	restoredContent, _ := os.ReadFile(targetPath)
	if string(restoredContent) != customHook {
		t.Errorf("restored content mismatch: got %q", string(restoredContent))
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Errorf("backup file should be removed after restore")
	}
}

func TestGitHook_ConfiguredHooksPathAndWorktree(t *testing.T) {
	wsDir := t.TempDir()
	runGitCmd(t, wsDir, "init")
	runGitCmd(t, wsDir, "config", "user.email", "test@example.com")
	runGitCmd(t, wsDir, "config", "user.name", "Test User")

	// 1. Test core.hooksPath configuration
	customHooksDir := filepath.Join(wsDir, ".custom_hooks")
	_ = os.MkdirAll(customHooksDir, 0755)
	runGitCmd(t, wsDir, "config", "core.hooksPath", ".custom_hooks")

	resolved, err := ResolveGitHooksDir(wsDir)
	if err != nil {
		t.Fatalf("ResolveGitHooksDir failed with core.hooksPath: %v", err)
	}
	cleanCustom := filepath.Clean(customHooksDir)
	if filepath.Clean(resolved) != cleanCustom {
		t.Errorf("ResolveGitHooksDir mismatch: got %q, want %q", resolved, cleanCustom)
	}

	// Install to configured hooksPath
	res, err := InstallGitHook(wsDir, "pre-commit", false)
	if err != nil {
		t.Fatalf("InstallGitHook to custom hooks path failed: %v", err)
	}
	if res.HookPath != filepath.Join(cleanCustom, "pre-commit") {
		t.Errorf("hook installed in wrong path: %s", res.HookPath)
	}

	// 2. Test linked worktree
	// Reset core.hooksPath
	runGitCmd(t, wsDir, "config", "--unset", "core.hooksPath")
	// Commit initial state so we can create a worktree branch
	_ = os.WriteFile(filepath.Join(wsDir, "README.md"), []byte("# Hello\n"), 0644)
	runGitCmd(t, wsDir, "add", "README.md")
	runGitCmd(t, wsDir, "commit", "-m", "init")

	wtDir := filepath.Join(t.TempDir(), "worktree1")
	runGitCmd(t, wsDir, "worktree", "add", wtDir)

	// Verify .git in worktree is a file pointing to gitdir
	wtGitEntry := filepath.Join(wtDir, ".git")
	fi, err := os.Stat(wtGitEntry)
	if err != nil {
		t.Fatalf("worktree .git stat failed: %v", err)
	}
	if fi.IsDir() {
		t.Fatalf("worktree .git should be a file, not a directory")
	}

	wtHooksDir, err := ResolveGitHooksDir(wtDir)
	if err != nil {
		t.Fatalf("ResolveGitHooksDir in linked worktree failed: %v", err)
	}
	if !strings.Contains(wtHooksDir, "hooks") {
		t.Errorf("expected hooks path to contain 'hooks', got %s", wtHooksDir)
	}
}

func TestGitHook_ScriptAdvisoryExecution(t *testing.T) {
	// The generated hook script must execute cleanly (exit 0) even when tzro is not on PATH
	script := GeneratePreCommitHookScript()

	tmpDir := t.TempDir()
	hookFile := filepath.Join(tmpDir, "pre-commit")
	if err := os.WriteFile(hookFile, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", hookFile)
	cmd.Dir = tmpDir
	// Remove tzro from PATH in environment to simulate missing binary
	cmd.Env = []string{"PATH=/bin:/usr/bin"}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("advisory hook failed when tzro missing from PATH: %v\nOutput: %s", err, string(out))
	}

	if !strings.Contains(string(out), "tzro binary not found") {
		t.Errorf("expected missing binary warning in output, got: %s", string(out))
	}
}
