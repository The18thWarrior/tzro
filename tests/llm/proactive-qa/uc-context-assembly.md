# Use Case: Task Context Assembly

**Actor**: AI coding agent initiating a feature implementation, bug fix, or refactor
**Route**: CLI — `tzro context "<task description>" --budget <n>`
**Backend**: Task Context Pack Engine (`pkg/context/context.go`), TS import resolver, Tree-sitter AST, and BM25 ranking
**Priority**: P0

---

## Intent

An agent needs to gather all necessary code definitions, TypeScript import alias resolutions, AST call graphs, and nearby tests relevant to a task description without burning 5–10 exploration turns or exceeding token context limits. Running `tzro context` assembles a ranked, token-budgeted context pack with verified provenance and AST skeletons in a single sub-second turn.

## Preconditions

- Tzro binary is installed and on PATH
- Current working directory is within a valid git repository with source files
- SQLite store is available for symbol index and artifact storage

## Success Criteria

- [ ] `tzro context "<task>" --budget 2000` generates a complete context pack strictly under the requested token budget
- [ ] Context pack contains prioritized symbol declarations, function signatures, and method bodies
- [ ] TypeScript path aliases (`@/*`, `~/*` from `tsconfig.json`) are accurately resolved to absolute workspace paths
- [ ] Direct callers and callees relevant to the query are extracted via the AST call graph
- [ ] Related test files are identified and paired with their implementation modules
- [ ] Every included evidence snippet includes source file paths, line ranges, and provenance metadata
- [ ] Output includes an assembly trace ID allowing offline inspection via `tzro inspect explain <trace_id>`
- [ ] Large files are automatically skeletonized with elided bodies indexed to cryptographic hashes
- [ ] Redundant files and lower-ranked symbols are gracefully omitted when approaching budget limits

## Edge Cases to Probe

- Very small budget (e.g. `--budget 200`) where only the top-ranked symbol signature can fit
- Unmatched queries where no obvious symbol matches exist in the codebase
- Monorepos with multiple nested `tsconfig.json` files and complex path mappings
- Codebases containing non-standard or mixed languages (Go, TypeScript, Python)
- Query containing syntax keywords or punctuation (e.g. `ValidateToken(ctx, token) error`)

## Anti-Patterns to Watch For

- [ ] Context pack total tokens exceed the requested `--budget` cap
- [ ] Full 1,000+ line files are dumped verbatim into the pack instead of skeletonized or sliced AST spans
- [ ] Unresolved TypeScript `@/` import aliases that leave the agent with broken module paths
- [ ] Output lacks provenance or file coordinate headers, leaving the agent confused about where code lives
- [ ] Irrelevant files dominate the top budget slots due to raw string matching rather than BM25 + AST relevance
