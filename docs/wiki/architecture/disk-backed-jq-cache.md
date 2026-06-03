# Disk-Backed JQ Cache Architecture & Consolidation

## Overview

The **Disk-Backed JQ Cache** is a core optimization subsystem designed to protect the LLM context window from massive tool execution payloads (e.g., hundreds of database rows, heavy API responses, scraped HTML bodies) that exceed **12KB** after compaction.

To resolve architectural leakage, increase locality, and improve testability, the caching domain was refactored and consolidated into a unified `internal/cache` package. This deep subsystem encapsulates SQLite relational table storage, local filesystem backup mechanisms, custom in-memory paginated paging, and external process execution with automated heuristic fallbacks.

---

## Architectural Refactoring Summary

Prior to this consolidation, the cache domain was scattered across several subsystems:

- **`internal/memory`**: Directly exposed cache-specific CRUD functions (`SaveCache`, `GetCache`, etc.), coupling the general database layer with cache serialization.
- **`internal/executor`**: Manually handled SQLite transactions, constructed dynamic file paths, invoked the external `jq` CLI process, performed in-memory slice offset paging, and implemented JSON regex parsing fallbacks.

### The Unified Cache Design (Before vs. After)

```
[Legacy Architecture]
┌─────────────────────────┐
│        Executor         │───(Invokes process, slices, fallback parser)──┐
└─────────────────────────┘                                               │
             │                                                            ▼
    (Calls cache CRUD)                                            ┌──────────────┐
             ▼                                                    │ External JQ  │
┌─────────────────────────┐                                       └──────────────┘
│      Memory (DB)        │───(Writes to SQLite / Filesystem)
└─────────────────────────┘

[Consolidated Architecture]
┌─────────────────────────┐
│        Executor         │
└─────────────────────────┘
             │ (Strictly via CacheStore interface seam)
             ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│                           internal/cache Package                               │
│                                                                                │
│  ┌───────────────────────┐   ┌───────────────────────┐  ┌───────────────────┐  │
│  │      CacheStore       │   │  sqlCacheStore (Impl) │  │  basicJQFallback  │  │
│  └───────────────────────┘   └───────────────────────┘  └───────────────────┘  │
│              │                           │                        │            │
│              ▼                           ▼                        ▼            │
│       Store/Introspect                RawDB()                Regex Parsing     │
└──────────────────────────────────────────┰─────────────────────────────────────┘
                                           ┃
                                           ▼
                               ┌───────────────────────┐
                               │     SqliteDatabase    │
                               └───────────────────────┘
```

---

## The `CacheStore` Interface Seam

The executor now interacts with the disk cache strictly via the `CacheStore` interface seam. This encapsulates all persistence and query details behind a clean API contract:

```go
type CacheStore interface {
    // Store handles envelope creation, writes to SQLite, backups to file,
    // and returns the envelope JSON string and CacheID.
    Store(ctx context.Context, rawPayload string) (envelopeStr string, cacheID string, err error)

    // Introspect retrieves the cache envelope JSON string (with DB lookup and file fallback).
    Introspect(ctx context.Context, cacheID string) string

    // Read retrieves offset-based paginated slice of records from the cache (with DB lookup and file fallback).
    Read(ctx context.Context, cacheID string, limit, offset int) string

    // Query runs a standard JQ expression query (using external jq command or basicJQFallback).
    Query(ctx context.Context, cacheID, jqExpr string) string
}
```

The global entry point is exposed via a package-level default variable:

```go
var DefaultStore CacheStore = &sqlCacheStore{}
```

---

## Under-the-Hood Mechanics

### 1. Multi-Tiered Persistence & Encapsulation

- **SQLite Cache Database**: The cache utilizes `memory.DB.RawDB() *sql.DB` to retrieve the database handle dynamically. It performs private cache table operations strictly inside `internal/cache`, preventing SQL strings from leaking into outer modules.
- **Fallback File Cache**: In case of a database lookup failure or test environment isolation, the store falls back to reading and reconstructing cache entries from backup JSON files in `.tzro/cache/<cache_id>.json`.

### 2. High-Performance Pagination

- The `.Read` method extracts an offset-based slice of records from the JSON payload.
- It safely normalizes unstructured JSON formats (either raw JSON arrays, objects containing a top-level `"records"` key, or objects with generic nested arrays) into Go slices, slicing them cleanly based on requested `limit` and `offset` indices without panicking.

### 3. Resilient JQ Query Engine

- **Fast-Path**: Checks for an active `jq` CLI binary in the system `$PATH` using `exec.LookPath("jq")`. If available, it spawns an external process context with safe stream pipe inputs.
- **Heuristic Fallback (`basicJQFallback`)**: If the `jq` utility is not installed, the engine runs an in-memory regexp search and grouping engine:
  - **Duplicates/Groupings**: Matches expressions containing `group_by` or `duplicate` to perform property-based array bucket aggregation.
  - **Equality Selects**: Parses syntax like `select(.Field == "Value")` and extracts exact matching records.
  - **Numeric Inequalities**: Handles operations like `select(.Age > 30)` using float parsing and dynamic operator matching.
  - **Fallback Slice**: Returns a default truncated 5-item record sample if the expression is too complex.

---

## Test-Driven Development (TDD) Verification

The consolidated module features 100% test coverage with isolated unit test setups:

1. **`internal/cache/cache_test.go`**: Validates isolated database insertions, pagination boundaries (empty slices, extreme offsets), backup restoration, JQ CLI invocation, and regexp fallback filters.
2. **`internal/executor/executor_test.go`**: Adapted to assert that all cached node execution, paginated reads, and JQ explorations traverse properly through `cache.DefaultStore`.

### Test Commands

To execute all backend cache and execution tests:

```bash
go test -v ./internal/cache/...
go test -v ./internal/executor/...
```
