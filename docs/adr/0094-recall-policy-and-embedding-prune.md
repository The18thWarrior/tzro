# RecallPolicy Field and Embedding-Based Chunk Dedup in Sectioned Synthesis

Status: accepted. Extends ADR-0091 (List-and-Write Topology). Supersedes the fan-reduce shortcut in the Recall Node.

The `fanReduceSynthesis` function in `recall_compaction.go` violated the Recall Node's glossary contract ("No LLM compaction calls") by running 83 sequential LLM inference calls inside a node defined as deterministic structural compaction. It also produced a lossy merge step (399K → 8K chars) that destroyed factual grounding (1.0/5.0 in benchmark T3 adr_summary). The function belonged architecturally in the synthesis pipeline, not the Recall Node — it performed goal-directed synthesis, duplicating work that the downstream Sectioned Synthesis (ADR-0084) already handles natively via dynamic outlines and per-section generation.

## Decision

### 1. `RecallPolicy` Field on `GraphNode`

A declarative field controlling Recall Node injection by the Kahn Compiler:
- `"auto"` (default): inject when upstream output exceeds downstream context budget (existing ADR-0091 behavior)
- `"always"`: unconditional injection
- `"skip"`: no injection — downstream node handles raw output decomposition natively

Set at the template level in the Plan Template Registry or by the Strategic Planner during mutation. The `ListAndWrite` template sets `RecallPolicy: "skip"` on its `explore` (list) node because Sectioned Synthesis handles large-context decomposition natively.

### 2. Embedding-Based Chunk Dedup in Sectioned Synthesis

When `ExecuteDocGenSectionedSynthesis` receives raw List output exceeding the configurable budget (default 80K chars), a deterministic pre-processing step prunes chunks before outline planning:

1. Split raw output into file-level chunks (reusing `splitListOutputIntoFileChunks`, extracted to a shared utility)
2. Embed all chunks + goal in a single `EmbedBatch` call (~100ms)
3. Rank chunks by goal relevance (cosine similarity to goal vector)
4. Drop chunks below relevance floor (default 0.20)
5. Greedy dedup: walk by relevance descending, skip chunks with cosine similarity > threshold (default 0.85) to any already-kept chunk
6. Cap at character budget

This is pure deterministic scaffolding (Go + Neural Embedding Sidecar) — no LLM calls. Falls back gracefully when the Embedding Sidecar is unavailable.

### 3. `fanReduceSynthesis` Deletion

The function and its fan-reduce routing in `recall.go` are deleted. For non-docgen tasks that still inject Recall via `RecallPolicy: "auto"`, the existing deterministic structural compaction (`buildCompactedRecallContext`) handles budget reduction within the 32K budget — as the Recall Node glossary always specified.

## Considered Options

- **Improve fan-reduce merge quality (larger token budget, better prompting):** Rejected — the merge was inherently lossy (83 fan outputs → 2048 tokens). No prompt improvement can avoid massive information loss at that compression ratio.
- **Runtime bypass in Recall Node (passthrough mode):** Rejected — a Recall Node that passes through raw content without compaction violates the glossary. If Recall isn't needed, don't inject it.
- **Keep fan-reduce for non-docgen Recall paths:** Rejected — fan-reduce violates the Recall Node glossary ("No LLM compaction calls") regardless of task type.

## Consequences

- The `ListAndWrite` DAG shape changes from `List → Recall → Synthesis → Sink` to `List → Synthesis → Sink` when `RecallPolicy: "skip"`.
- Binding resolution in `sct_compiler.go` must handle the missing Recall node — `write_file` binds directly to the List node's output instead of `{id}_recall.output`.
- Three new configuration fields: `embeddingPruneRedundancyThreshold` (0.85), `embeddingPruneRelevanceFloor` (0.20), `embeddingPruneBudgetChars` (80000).
- `splitListOutputIntoFileChunks` moves from `recall_compaction.go` to `chunk_utils.go` — both Recall and Sectioned Synthesis import it.
- CONTEXT.md updated: new `RecallPolicy` glossary term; `Recall Node` and `Budget-Overflow Recall Injection` entries updated.

## References

- ADR-0084: Generalized Sectioned Map-Reduce Synthesis
- ADR-0091: Probe Removal and List-and-Write Topology
- ADR-0064: Two-Pass Extraction and Content-Aware Recall Compaction
- ADR-0075: Neural Embedding Sidecar
- SOLUTION_APPROACH.md: Principle 1 (Embeddings, not heuristics), Principle 3 (Model/Scaffolding Split)
