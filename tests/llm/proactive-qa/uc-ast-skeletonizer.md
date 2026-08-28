# Use Case: AST Skeletonizer and Body Expansion

**Actor**: AI coding agent reading large source files
**Route**: CLI — `tzro skeleton <file>` and `tzro expand <hash>`
**Backend**: Tree-sitter AST parser + local SQLite content-hash store
**Priority**: P0

---

## Intent

An agent wants to understand the interface and structure of a large source file without dumping the entire file into context. The skeleton command strips function/method bodies and replaces them with cryptographic hash tags, achieving 70–90% token reduction. When the agent needs to edit a specific function body, it retrieves only those lines using the expand command.

## Preconditions

- Tzro binary is installed and on PATH
- Target file exists and is in a supported language (Go, TypeScript, Python, etc.)
- SQLite content-hash store is writable (auto-created at `~/.tzro/store.db`)

## Success Criteria

- [ ] `tzro skeleton file.go` outputs the file with function bodies replaced by `// [body elided: #hash]` comments
- [ ] The skeleton preserves imports, type definitions, struct fields, and function signatures
- [ ] Compression stats are printed to stderr (original size, skeleton size, savings ratio, elided block count)
- [ ] `tzro expand <hash>` retrieves the original function body with file path and line numbers
- [ ] Round-trip integrity: expanding all hashes from a skeleton reconstructs the original file
- [ ] The content-hash store deduplicates identical function bodies across files
- [ ] Skeleton output is valid syntax in the source language (parseable, not garbled)
- [ ] Hash format uses short hex prefix (`#abcd1234`) for readability

## Edge Cases to Probe

- Skeleton a file with no functions (only type definitions) — should return the file unchanged
- Skeleton a binary or non-code file — should fail with a clear error
- Expand a hash that doesn't exist in the store — should return a clear error message
- Skeleton a file with deeply nested closures or anonymous functions

## Anti-Patterns to Watch For

- [ ] Skeleton strips comments or documentation strings that are outside function bodies
- [ ] Expand returns stale content after the source file has been modified
- [ ] Hash collisions cause wrong function body to be returned
- [ ] Skeleton crashes on files with syntax errors instead of falling back gracefully
- [ ] Store grows unboundedly without any cleanup mechanism
