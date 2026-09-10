# Use Case: Unified Local Evidence Search

**Actor**: AI coding agent or developer searching across heterogeneous project sources
**Route**: CLI — `tzro search "<query>" [--sources code,docs,specs,logs,artifacts]`
**Backend**: Multi-source evidence search engine (`pkg/search/search.go`), SQLite FTS5, and AST Span extractor
**Priority**: P1

---

## Intent

An agent needs to locate relevant information spread across code, architecture decision records (ADRs), product requirements documents (PRDs), stored execution logs, session manifests, and tabular data. Running `tzro search` searches across all these diverse evidence sources simultaneously, deduplicating identical blobs via content-addressing and extracting precise AST spans in under 10ms.

## Preconditions

- Tzro binary is installed and on PATH
- Project directory contains indexed code, documentation (`docs/`), or SQLite artifacts
- SQLite store is initialized with FTS5 or lexical fallback

## Success Criteria

- [ ] `tzro search "<query>"` searches across source code, markdown documentation, architecture specs, and artifacts
- [ ] Results group evidence by source kind (Code, Docs, ADRs, Artifacts, Logs)
- [ ] Code search hits include extracted AST spans with start and end line numbers
- [ ] Identical content across multiple locations is deduplicated by content hash
- [ ] Results include BM25 relevance scores and provenance markers
- [ ] Query executes in <10ms for typical medium-to-large codebases
- [ ] Agent can restrict search domain using source filters (e.g. `--sources docs,specs`)
- [ ] Total returned payload is formatted in dense, high-signal Markdown (<500 tokens)

## Edge Cases to Probe

- Query matching only documentation without code matches
- Query containing punctuation, quotes, or boolean operators (`AND`, `OR`, `NOT`)
- Searching in a project with no documentation or markdown files
- Search matches inside generated files or minified bundles (should respect ignore rules)
- Case-sensitive vs case-insensitive matching across file boundaries

## Anti-Patterns to Watch For

- [ ] Search returns raw file paths with no content preview or AST context
- [ ] Search includes `.git/`, `node_modules/`, or vendor directories in results
- [ ] Search hangs or crashes when SQLite FTS5 extension is unavailable
- [ ] Result list overflows agent context with hundreds of unranked lines
