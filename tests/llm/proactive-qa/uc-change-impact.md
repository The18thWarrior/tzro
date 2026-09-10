# Use Case: Pre-Edit Change Impact Analysis

**Actor**: AI coding agent or engineer planning modifications to shared modules, types, or utilities
**Route**: CLI — `tzro impact [files...]` or `tzro impact` (uncommitted git changes)
**Backend**: Change-Impact Graph Analyzer (`pkg/context/impact.go`) and AST Call Graph
**Priority**: P1

---

## Intent

Before modifying code, an agent needs to determine the blast radius of changes: which functions call the modified code, which downstream modules depend on it, and what existing unit and integration tests cover the affected surface. Running `tzro impact` computes the structural call-graph and dependency network, giving the agent exact test targets to run before and after making changes.

## Preconditions

- Tzro binary is installed and on PATH
- Working directory is inside a git repository with source files and AST symbols
- Git working directory contains uncommitted changes OR user specifies one or more target filepaths

## Success Criteria

- [ ] Running `tzro impact` with no arguments detects uncommitted git modifications and computes blast radius
- [ ] Running `tzro impact <path/to/file>` analyzes target file and reports direct callers and consumers
- [ ] Direct callers across the repository are accurately mapped with file path and line numbers
- [ ] Dependent downstream files are enumerated with dependency depth
- [ ] Existing test suites covering the modified files or their callers are surfaced as recommended test runs
- [ ] Output categorizes impact into risk tiers (High / Medium / Low blast radius)
- [ ] Output is formatted in concise Markdown suitable for immediate inclusion in agent plans
- [ ] Analysis completes in under 100ms for typical projects

## Edge Cases to Probe

- Running `tzro impact` on a clean git working directory with no arguments (should report clean state or prompt for target)
- Running impact on a newly created, untracked file
- Circular dependency chains between modules
- Modifying a central interface or utility used by hundreds of files
- Files in non-standard directories or excluded by `.gitignore`

## Anti-Patterns to Watch For

- [ ] Unbounded traversal causing infinite loops on circular import graphs
- [ ] Missing direct callers because of relative import path resolution errors
- [ ] Failing to identify co-located unit test files (`_test.go`, `.test.ts`, `.spec.ts`)
- [ ] Dumping entire file contents of callers instead of concise symbol references and signatures
