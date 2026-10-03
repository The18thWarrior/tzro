# Use Case: Predictive Test Selection

**Actor**: AI coding agent or engineer who has made code changes and needs to identify and run only the affected tests
**Route**: CLI — `tzro test --impact [--staged|--unstaged|--all] [--dry-run]`
**Backend**: TestSelector (`pkg/context/test_selection.go`), DiffParser (`pkg/context/diff.go`), multi-language adapters
**Priority**: P0

---

## Intent

After making code changes, the user wants to run only the tests that are actually affected by their modifications rather than the full test suite. tzro analyzes the blast radius of changed symbols, identifies relevant test files across Go, TypeScript (Jest/Vitest), Python (pytest), and Rust, and executes them with compacted output.

## Preconditions

- Tzro binary is installed and on PATH
- Working directory is inside a git repository with source files and tests
- Git working directory has staged or unstaged changes, or both

## Success Criteria

- [ ] `tzro test --impact --staged` identifies tests affected by staged git changes
- [ ] `tzro test --impact --unstaged` identifies tests affected by unstaged working directory changes
- [ ] `tzro test --impact --all` combines both staged and unstaged analysis
- [ ] Changed symbols are mapped to their enclosing AST declarations across Go, TypeScript, Python, and Rust
- [ ] Comment-only diff hunks are filtered out and do not trigger test selection
- [ ] Transitive callers of changed symbols are traced to discover indirectly affected tests
- [ ] Test runners are auto-dispatched: `go test` for Go, `vitest`/`jest` for TypeScript, `pytest` for Python
- [ ] `--dry-run` lists affected tests without executing them
- [ ] When build configs or shared fixtures change, the selector falls back to a broader suite
- [ ] Test output is compacted through the evidence compactor with exit-code confidence

## Edge Cases to Probe

- Running with no changes (clean working directory) — should report no affected tests
- Changing a widely-imported type used by dozens of test files
- Modifying a conftest.py or test fixture that indirectly affects many tests
- Deleting a function that was previously tested — should detect missing test target
- Mixed-language changes (Go + TypeScript) in a single commit

## Anti-Patterns to Watch For

- [ ] Running the entire test suite when only 2 files changed
- [ ] Missing test files because of relative import resolution failures
- [ ] Selecting tests for comment-only changes that have no behavioral impact
- [ ] Failing to detect Python conftest.py or Go TestMain fixtures as broadening triggers
- [ ] Raw test output dumped without compaction, consuming excessive tokens
