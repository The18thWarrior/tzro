# Verified Architecture Notes (2026-09-08)
- Current CONTEXT.md, SOLUTION_APPROACH.md, and v2.0.0 release notes define deterministic Go Token Shield, not the retired DAG/local-LLM/MCP execution engine. Legacy .github instructions/skills may describe removed v1 tools; verify current code before planning around them.
- cmd/tzro/main.go implements BOTH ingest and query. Do not infer missing commands from partial file reads or delegate summaries.
- pkg/probe/probe.go currently uses filepath.WalkDir + case-folded literal bytes.Contains; it is not semantic search or ripgrep-backed despite prose claims.
- pkg/store/store.go currently creates ordinary symbol_index tables and SearchSymbols uses LIKE; comments saying FTS5 do not establish an FTS5 implementation.
- pkg/proxy/proxy.go forwards response bytes unchanged; its dlpMap parameter does not wire in Rehydrate. pkg/kvlock payload structs omit many provider fields. Verify fidelity before expanding proxy transformations.
- Historical inspiration evidence: ADR-0015 explicitly references Pristal; docs/wiki/log.md around lines 100-145 documents late-v1 benchmark/Probe reliability challenges and RadixAttention/prefill research. v2 release dated 2026-08-27 records architecture replacement.
- Research lesson: cross-check delegate claims against controlling code, especially command presence, performance percentages, history dates, and protocol behavior. Test definitions and release claims are not fresh execution results.
