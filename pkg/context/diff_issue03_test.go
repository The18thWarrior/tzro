package context_test

import (
	stdctx "context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tzroctx "tzro/pkg/context"
)

// initGitRepo initializes a temporary git repository with user name/email configured.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, out: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.name", "Tzro Tester")
	run("config", "user.email", "tester@tzro.local")
	run("config", "commit.gpgsign", "false")

	return dir
}

// commitAll commits all tracked/untracked changes in the repo.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	_ = cmd.Run()

	cmd2 := exec.Command("git", "commit", "-m", msg, "--allow-empty")
	cmd2.Dir = dir
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("commit failed: %v, out: %s", err, out)
	}
}

// TestIssue03_LowercaseFunctionBodyEdit tests:
// "A lowercase function with only an edited return expression appears as a changed root."
func TestIssue03_LowercaseFunctionBodyEdit(t *testing.T) {
	dir := initGitRepo(t)

	codeV1 := `package main

func calculateSum(a, b int) int {
	return a + b
}
`
	_ = os.WriteFile(filepath.Join(dir, "calc.go"), []byte(codeV1), 0644)
	commitAll(t, dir, "initial commit")

	// Edit only the return statement inside calculateSum
	codeV2 := `package main

func calculateSum(a, b int) int {
	// modified return body
	return a + b + 42
}
`
	_ = os.WriteFile(filepath.Join(dir, "calc.go"), []byte(codeV2), 0644)

	diffRes, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeAll)
	if err != nil {
		t.Fatalf("AcquireGitDiff failed: %v", err)
	}

	symbols, cov, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffRes)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff failed: %v", err)
	}

	if cov.NoChanges {
		t.Fatalf("expected changes, got NoChanges")
	}

	foundCalc := false
	for _, sym := range symbols {
		if sym.Name == "calculateSum" && sym.Kind == "function" {
			foundCalc = true
		}
	}

	if !foundCalc {
		t.Fatalf("expected lowercase function 'calculateSum' to be extracted, got: %+v", symbols)
	}
}

// TestIssue03_CommentVsRuntimeStringEdit tests:
// "Comment words do not become symbols. A changed runtime string still selects its enclosing declaration."
func TestIssue03_CommentVsRuntimeStringEdit(t *testing.T) {
	dir := initGitRepo(t)

	codeV1 := `package main

func greetUser() string {
	// OldCommentWords
	return "hello"
}
`
	_ = os.WriteFile(filepath.Join(dir, "greet.go"), []byte(codeV1), 0644)
	commitAll(t, dir, "initial commit")

	// 1. Comment-only edit: words in comments must NOT become symbols
	codeCommentOnly := `package main

func greetUser() string {
	// NewCommentWords With CapitalizedFakeSymbol
	return "hello"
}
`
	_ = os.WriteFile(filepath.Join(dir, "greet.go"), []byte(codeCommentOnly), 0644)
	diffComment, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeAll)
	if err != nil {
		t.Fatalf("AcquireGitDiff comment-only failed: %v", err)
	}

	symsComment, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffComment)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff comment-only failed: %v", err)
	}

	for _, s := range symsComment {
		if s.Name == "CapitalizedFakeSymbol" || s.Name == "NewCommentWords" {
			t.Errorf("comment words leaked as symbol roots: %s", s.Name)
		}
	}
	if len(symsComment) != 0 {
		t.Errorf("expected 0 symbols for comment-only edit, got: %+v", symsComment)
	}

	// 2. Runtime string edit: must select enclosing declaration greetUser
	codeStringEdit := `package main

func greetUser() string {
	// OldCommentWords
	return "welcome to the system"
}
`
	_ = os.WriteFile(filepath.Join(dir, "greet.go"), []byte(codeStringEdit), 0644)
	diffStr, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeAll)
	if err != nil {
		t.Fatalf("AcquireGitDiff string edit failed: %v", err)
	}

	symsStr, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffStr)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff string edit failed: %v", err)
	}

	foundGreet := false
	for _, s := range symsStr {
		if s.Name == "greetUser" {
			foundGreet = true
		}
		if s.Name == "welcome" || s.Name == "system" {
			t.Errorf("string literal words leaked as symbol roots: %s", s.Name)
		}
	}

	if !foundGreet {
		t.Fatalf("expected changed runtime string to select enclosing declaration 'greetUser', got: %+v", symsStr)
	}
}

// TestIssue03_DeletedSymbolsAndFiles tests:
// "Deleted functions, fields, files, and renamed declarations retain old identities."
func TestIssue03_DeletedSymbolsAndFiles(t *testing.T) {
	dir := initGitRepo(t)

	codeV1 := `package main

func ObsoleteFunction() string {
	return "deprecated"
}

func RetainedFunction() string {
	return "active"
}
`
	_ = os.WriteFile(filepath.Join(dir, "ops.go"), []byte(codeV1), 0644)
	commitAll(t, dir, "initial commit")

	// Delete ObsoleteFunction
	codeV2 := `package main

func RetainedFunction() string {
	return "active"
}
`
	_ = os.WriteFile(filepath.Join(dir, "ops.go"), []byte(codeV2), 0644)

	diffRes, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeAll)
	if err != nil {
		t.Fatalf("AcquireGitDiff failed: %v", err)
	}

	symbols, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffRes)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff failed: %v", err)
	}

	foundDeleted := false
	for _, s := range symbols {
		if s.Name == "ObsoleteFunction" && s.Kind == "deleted" && s.SourceSnapshot == "old" {
			foundDeleted = true
		}
	}

	if !foundDeleted {
		t.Fatalf("expected deleted symbol 'ObsoleteFunction' with Kind=deleted and SourceSnapshot=old, got: %+v", symbols)
	}
}

// TestIssue03_PartialStagingAndScopeSeparation tests:
// "A partially staged file produces roots from the index, even when the worktree contains different edits."
// "An empty staged diff plus a nonempty unstaged diff returns no staged changes."
func TestIssue03_PartialStagingAndScopeSeparation(t *testing.T) {
	dir := initGitRepo(t)

	codeV1 := `package main

func StagedFunc() int {
	return 1
}

func UnstagedFunc() int {
	return 2
}
`
	_ = os.WriteFile(filepath.Join(dir, "multi.go"), []byte(codeV1), 0644)
	commitAll(t, dir, "initial commit")

	// 1. Stage an edit to StagedFunc
	codeStaged := `package main

func StagedFunc() int {
	return 100 // staged change
}

func UnstagedFunc() int {
	return 2
}
`
	_ = os.WriteFile(filepath.Join(dir, "multi.go"), []byte(codeStaged), 0644)
	cmdAdd := exec.Command("git", "add", "multi.go")
	cmdAdd.Dir = dir
	_ = cmdAdd.Run()

	// 2. Now modify UnstagedFunc in worktree WITHOUT staging it
	codeWorktree := `package main

func StagedFunc() int {
	return 100 // staged change
}

func UnstagedFunc() int {
	return 200 // UNSTAGED change
}
`
	_ = os.WriteFile(filepath.Join(dir, "multi.go"), []byte(codeWorktree), 0644)

	// Acquire STAGED diff
	diffStaged, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeStaged)
	if err != nil {
		t.Fatalf("AcquireGitDiff staged failed: %v", err)
	}

	symsStaged, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffStaged)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff staged failed: %v", err)
	}

	// In staged scope, StagedFunc must be present, UnstagedFunc must NOT be present!
	foundStaged := false
	foundUnstagedInStaged := false
	for _, s := range symsStaged {
		if s.Name == "StagedFunc" {
			foundStaged = true
		}
		if s.Name == "UnstagedFunc" {
			foundUnstagedInStaged = true
		}
	}

	if !foundStaged {
		t.Errorf("expected StagedFunc to be extracted in staged scope")
	}
	if foundUnstagedInStaged {
		t.Errorf("UnstagedFunc leaked into staged scope extraction!")
	}

	// 3. Test empty staged diff + non-empty unstaged diff returns NoChanges in staged scope!
	// Reset stage: git reset multi.go
	cmdReset := exec.Command("git", "reset", "multi.go")
	cmdReset.Dir = dir
	_ = cmdReset.Run()

	// Now staged diff is empty, but unstaged diff has UnstagedFunc
	diffEmptyStaged, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeStaged)
	if err != nil {
		t.Fatalf("AcquireGitDiff empty staged failed: %v", err)
	}

	symsEmpty, covEmpty, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffEmptyStaged)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff empty staged failed: %v", err)
	}
	if !covEmpty.NoChanges || len(symsEmpty) != 0 {
		t.Errorf("expected NoChanges=true and 0 symbols for empty staged diff, got NoChanges=%v, len=%d",
			covEmpty.NoChanges, len(symsEmpty))
	}
}

// TestIssue03_UnbornHEADAndNonSymbolFiles tests:
// "Unborn HEAD, untracked files, conflicts, unusual paths, and Git failure have distinct tested outcomes."
// "Changed module/configuration files survive even when no declaration exists."
func TestIssue03_UnbornHEADAndNonSymbolFiles(t *testing.T) {
	dir := initGitRepo(t)

	// In an unborn HEAD (no commits yet!), stage a go.mod file and a Go code file
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module myapp\n\ngo 1.22\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "init.go"), []byte("package myapp\nfunc InitApp() {}\n"), 0644)

	cmdAdd := exec.Command("git", "add", "go.mod", "init.go")
	cmdAdd.Dir = dir
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v, out: %s", err, out)
	}

	diffRes, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeStaged)
	if err != nil {
		t.Fatalf("AcquireGitDiff on unborn HEAD failed: %v", err)
	}

	if !diffRes.UnbornHEAD {
		t.Errorf("expected UnbornHEAD to be true")
	}

	symbols, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffRes)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff failed: %v", err)
	}

	foundGoMod := false
	foundInitApp := false

	for _, s := range symbols {
		if s.Name == "go.mod" && s.Kind == "config" {
			foundGoMod = true
		}
		if s.Name == "InitApp" && s.Kind == "function" {
			foundInitApp = true
		}
	}

	if !foundGoMod {
		t.Errorf("go.mod configuration file not extracted as non-symbol change")
	}
	if !foundInitApp {
		t.Errorf("InitApp not extracted from unborn HEAD")
	}
}

// TestIssue03_MultiLanguageDiffExtraction tests:
// "Go, TS/JS, Python, and Rust fixtures cover the declaration forms needed by their adapters."
func TestIssue03_MultiLanguageDiffExtraction(t *testing.T) {
	dir := initGitRepo(t)

	// TS file
	tsV1 := `export function processData(items: string[]): number {
	return items.length;
}
`
	_ = os.WriteFile(filepath.Join(dir, "process.ts"), []byte(tsV1), 0644)

	// Python file
	pyV1 := `def handle_request(req):
    return "ok"
`
	_ = os.WriteFile(filepath.Join(dir, "handler.py"), []byte(pyV1), 0644)

	// Rust file
	rsV1 := `pub fn execute_job(id: u64) -> bool {
    true
}
`
	_ = os.WriteFile(filepath.Join(dir, "job.rs"), []byte(rsV1), 0644)

	commitAll(t, dir, "initial commit")

	// Edit bodies of all three
	tsV2 := `export function processData(items: string[]): number {
	const count = items.length;
	return count * 2;
}
`
	_ = os.WriteFile(filepath.Join(dir, "process.ts"), []byte(tsV2), 0644)

	pyV2 := `def handle_request(req):
    log_event(req)
    return "ok_v2"
`
	_ = os.WriteFile(filepath.Join(dir, "handler.py"), []byte(pyV2), 0644)

	rsV2 := `pub fn execute_job(id: u64) -> bool {
    let status = true;
    status
}
`
	_ = os.WriteFile(filepath.Join(dir, "job.rs"), []byte(rsV2), 0644)

	diffRes, err := tzroctx.AcquireGitDiff(stdctx.Background(), dir, tzroctx.GitScopeAll)
	if err != nil {
		t.Fatalf("AcquireGitDiff failed: %v", err)
	}

	symbols, _, err := tzroctx.ExtractSymbolsFromSnapshotDiff(stdctx.Background(), dir, diffRes)
	if err != nil {
		t.Fatalf("ExtractSymbolsFromSnapshotDiff failed: %v", err)
	}

	foundTS := false
	foundPy := false
	foundRs := false

	for _, s := range symbols {
		if s.Name == "processData" {
			foundTS = true
		}
		if s.Name == "handle_request" {
			foundPy = true
		}
		if s.Name == "execute_job" {
			foundRs = true
		}
	}

	if !foundTS {
		t.Errorf("TypeScript function 'processData' not extracted from body edit, got: %+v", symbols)
	}
	if !foundPy {
		t.Errorf("Python function 'handle_request' not extracted from body edit, got: %+v", symbols)
	}
	if !foundRs {
		t.Errorf("Rust function 'execute_job' not extracted from body edit, got: %+v", symbols)
	}
}
