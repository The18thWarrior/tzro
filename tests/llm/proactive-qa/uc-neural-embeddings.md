# Use Case: Neural Embedding Sidecar

**Actor**: Developer running tasks through the tzro engine via CLI or MCP.
**Route**: CLI (`tzro chat`) / MCP (`tzro_run`, `tzro_memory_query`, `tzro_rag_context`)
**Backend**: http://localhost:36888
**Priority**: P1

---

## Intent

A developer uses tzro's memory, knowledge graph, or RAG features and expects high-quality semantic similarity powered by a dedicated neural embedding model running locally. The embedding sidecar manages a lightweight GGUF embedding model (All-MiniLM-L6-v2) as a separate llama-server process, providing vector embeddings for memory queries, knowledge graph traversal, and RAG context retrieval — all at zero cloud cost with an in-memory LRU cache for repeated queries.

## Preconditions

- The `tzro` daemon is running.
- The llama-server binary is available on PATH.
- Network access is available for initial model download (first run only).

## Success Criteria

- [ ] The embedding sidecar starts as a separate llama-server process on a dedicated port, independent of the router and worker sidecars.
- [ ] If the embedding model file does not exist locally, it is auto-downloaded from Hugging Face on first use.
- [ ] The sidecar can adopt an already-running llama-server embedding process (matching port and model) without restarting it.
- [ ] Embedding requests produce fixed-dimension float32 vectors suitable for cosine similarity comparison.
- [ ] An in-memory LRU cache prevents redundant embedding computations for repeated text inputs.
- [ ] Cache hits return immediately without any inference call to the sidecar.
- [ ] The sidecar status is reported accurately: Stopped, Starting, Active, or Adopted.
- [ ] The sidecar process is cleanly terminated when the daemon shuts down.
- [ ] Memory queries (`tzro_memory_query`) use neural embeddings for semantic similarity ranking when the sidecar is active.
- [ ] When the embedding sidecar is unavailable, the system falls back to text-based similarity without crashing.

## Edge Cases to Probe

- Starting the daemon without network access and no cached embedding model — verify clean error message about model download failure.
- Embedding sidecar process crashes mid-query — verify the calling code gets a timeout error and the sidecar is marked as Stopped.
- Two concurrent embedding requests for the same text — verify cache deduplication (only one inference call).
- Very long text input (>8000 tokens) — verify the sidecar handles truncation gracefully.
- Model file is corrupted on disk — verify the sidecar reports a clean startup error.

## Anti-Patterns to Watch For

- [ ] The embedding sidecar shares the same llama-server process as the router or worker, causing resource contention.
- [ ] Cache entries are never evicted, causing unbounded memory growth.
- [ ] The auto-download blocks the daemon startup, preventing other tasks from running.
- [ ] Embedding vectors are silently returned as zero vectors when the sidecar is unhealthy.
- [ ] The sidecar port conflicts with the router or worker sidecar ports.
