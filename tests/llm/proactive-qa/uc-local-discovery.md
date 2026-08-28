# Use Case: Local Codebase Discovery

**Actor**: AI coding agent or developer exploring a codebase
**Route**: CLI — `tzro probe "<query>"`
**Backend**: Local ripgrep + Tree-sitter AST analysis
**Priority**: P0

---

## Intent

An agent or developer wants to quickly locate symbols, files, and code patterns in a codebase without consuming cloud API tokens. The probe command uses ripgrep for text matching and Tree-sitter for AST-aware scope analysis, returning results with exact line numbers and symbol signatures.

## Preconditions

- Tzro binary is installed and on PATH
- Current directory is inside a codebase with source files
- ripgrep (`rg`) is available on PATH

## Success Criteria

- [ ] `tzro probe "functionName"` returns matching files with line numbers
- [ ] Results include AST-aware context (function signatures, struct definitions)
- [ ] Output is formatted as markdown suitable for agent consumption
- [ ] Results are returned in sub-millisecond latency for typical codebases
- [ ] Zero cloud tokens are consumed during probe operations
- [ ] When a store is available, probe indexes results for future lookups
- [ ] Query handles special characters and regex-like patterns without crashing
- [ ] Results cap at a reasonable limit (e.g., 20 matches) to avoid context explosion

## Edge Cases to Probe

- Probe in an empty directory — should return empty results, not error
- Probe for a string that matches thousands of files — should truncate gracefully
- Probe with special characters (parentheses, brackets, dots) in the query
- Run probe without ripgrep installed — should fail with a helpful error message

## Anti-Patterns to Watch For

- [ ] Probe returns raw ripgrep output without AST enrichment
- [ ] Probe hangs on very large repositories (>100k files)
- [ ] Results contain binary file matches or `.git` directory content
- [ ] Probe silently falls back to no results instead of reporting errors
