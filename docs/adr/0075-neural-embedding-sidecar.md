# ADR-0075: Neural Embedding Sidecar for Schema-Aware Column Selection

## Status

Accepted

## Context

The 4B local model's GBNF-constrained extraction of `selectColumns` in
QueryIntent is unreliable. For `lead_lookup_by_company`, the model consistently
extracts `[name, AccountId, Status]` instead of the correct `[name, email]`.
This causes `query_builder` to generate SQL missing the `email` column, forcing
the synthesis model to hallucinate email addresses from compacted JSON it cannot
read accurately.

Red-team loop data (17 cycles) shows:
- `lead_lookup_by_company` averages 1.40/5 quality
- The model cannot reliably map natural language phrases ("email address") to
  schema column names ("email")
- Filter column extraction works well; only select column extraction fails

The existing `PureGoEmbeddingEngine` uses a 30-term fixed vocabulary with
bag-of-words TF vectorization. It handles domain-specific terms (e.g.,
"salesforce", "hubspot") but cannot perform semantic matching for arbitrary
column names and natural language phrases.

## Decision

### 1. Dedicated Embedding Sidecar

A third `llama-server` process running All-MiniLM-L6-v2-Q8 (~23MB GGUF) with
`--embedding --pooling mean`. Separate from the worker (4B generative) and
router (1B classification) sidecars.

- **Auto-download**: When `embeddingModelPath` is empty in config, the model is
  downloaded from HuggingFace on first use.
- **Eager start**: Launches alongside worker/router at daemon boot. Non-fatal
  failure falls back to bag-of-words.
- **Minimal footprint**: ~100MB RAM, 2 threads, 512 context. The model file is
  23MB vs 4GB for the worker model.

### 2. Hybrid Embedding Cache

Two-tier cache keyed by SHA-256 of input text:
- **Hot tier**: `sync.Map` for in-process lookups (zero-latency)
- **Cold tier**: SQLite `embedding_cache.db` for cross-restart persistence
- **Model invalidation**: `model_id` column in SQLite; cached vectors from a
  different model are discarded on access

### 3. Schema-Aware Column Selection

`ResolveSelectColumns()` in `query_intent.go`:
1. Enriches each schema column with sample values (`"email: dalves@walmart.com"`)
2. Batch-embeds `[goal, enrichedCol1, enrichedCol2, ...]` in one HTTP call
3. Ranks columns by cosine similarity to the goal
4. Returns columns above `columnScoreThreshold` (configurable, default 0.3)

Injection point: after GBNF `ExtractQueryIntent()` returns, overwrite
`intent.SelectColumns` with the embedding result. Filter/group/aggregate
operations remain GBNF-extracted (which works reliably).

### 4. Universal Embedding Upgrade

All existing `PureGoEmbeddingEngine` callers upgraded to neural:
- `embeddings.DefaultEngine` package-level variable set at startup
- Package-level `CosineSimilarity(s1, s2)` tries neural first, falls back to
  bag-of-words on error
- Memory DB initialization uses `DefaultEngine` when available

## Consequences

- The embedding sidecar is a third long-running process. On a 16GB machine with
  the worker (4GB), router (200MB), and embedding (100MB) sidecars, total memory
  for local inference is ~4.3GB. Acceptable.
- All memory search, skill matching, and corrective skill deduplication benefit
  from neural semantic similarity instead of bag-of-words.
- Column selection for data analysis tasks becomes deterministic and accurate,
  addressing the #1 failure mode in the datanal benchmark.
- `columnScoreThreshold` requires empirical tuning. Similarity scores are logged
  to stderr for calibration during benchmark runs.
- Auto-download precedent: this is the first model that downloads automatically.
  Worker and router models require explicit configuration. Acceptable because
  the embedding model is a utility dependency (23MB), not a primary inference
  model (4GB).
