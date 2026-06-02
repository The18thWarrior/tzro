# Pristal Architecture Alignment: Database, Compiler, Memory, and Executor Upgrades

We align the `tzro` durable local-first execution framework with the Pristal Architecture (Pristal v2) by upgrading its database, compiler, memory, and execution engines. These changes decouple the engine from hardcoded SQLite drivers, eliminate connection thrashing, split monolithic execution into fine-grained Strategist-Compiler-Translator (SCT) phases, implement proactive resource garbage collection, and support dynamic parallel branch skipping.

## Key Design Rules

1. **Dynamic Dialect Seams (Database Manager & Dialect Adapter)**:
   - We replace the SQLite-coupled persistence layer with a dynamic **Database Manager** (`DatabaseManager` struct under `memory.DB`).
   - We introduce the **Dialect Adapter** (`DialectAdapter`) interface to abstract driver names, schema migrations, and database introspection.
   - To resolve SQL differences (especially upsert and merge logic) without adding heavy ORM dependencies, the `DialectAdapter` serves as a Dialect Query Registry, returning parameterized query templates for statements that vary across drivers (e.g., `UpsertNodeStateQuery()`).

2. **Thread-Safe Connection Pool Caching**:
   - Rather than closing and reopening files on every tool database operation, standalone tools fetch connections from a thread-safe global connection cache (`localConnectionPool`).
   - Connections are initialized immediately in Write-Ahead Logging (WAL) mode with busy timeouts to avoid `database is locked` conflicts.
   - Connections set `MaxIdleConns` and `ConnMaxIdleTime` to ensure Go's `database/sql` driver automatically reclaims unused file descriptors without requiring custom LRU eviction loops.

3. **Fine-Grained SCT Graph Compilation**:
   - We separate the planning and execution domains using the Strategist-Compiler-Translator (SCT) split.
   - The **Cloud Model** (The Strategist) compiles a high-level **Abstract Graph** representing strategic tool steps, keeping planner prompts simple and decoupled from GBNF schemas.
   - The local **Kahn Compiler** translates this high-level plan into a low-level execution graph on-device. It expands each strategic node into paired `gbnf_bridge` (for safe logit-level grammar parameter extraction) and `deterministic` (for tool execution) nodes, and injects a terminal `synthesis` node.

4. **Two-Tier KV Cache Garbage Collection**:
   - To prevent context memory leakage and slot thrashing on resource-constrained local inference servers:
     - **Tier 1 (Active Slot Erasure)**: Clears context slot tokens via the server's slots control API immediately upon task completion.
     - **Tier 2 (Process Restart)**: Monitors Resident Set Size (RSS) memory of the local inference process after task completion. If RSS memory exceeds threshold limits (e.g., 2GB) and the server is idle, the sidecar process is gracefully terminated and recycled to return pre-allocated C++ buffers to the OS.

5. **Hybrid Branch Evaluator & Dynamic Edge Pruning**:
   - To enable topological branch skipping under Kahn level execution, the engine evaluates node conditions using a **Hybrid Branch Evaluator**:
     - First, it evaluates conditions via a fast, deterministic JSONPath/Key-Value comparison.
     - If the deterministic comparison returns false or cannot be parsed, it falls back to a semantic **Local Model** decision seam using GBNF-constrained JSON outputs.
   - If a branch is skipped, the executor recursively propagates the `skipped` state down all dependent child nodes (`propagateSkip`), preventing execution deadlocks or invalid parallel executions.

## Considered Options

- **Dialect Query Registry vs. Generic SQL Builder**: Implementing custom query templates in the Dialect Adapter was selected over adding a dynamic SQL builder (like `goqu` or `squirrel`). Query registry keeps `database/sql` code direct, simple, and dependency-free.
- **Post-Planning Compiler Expansion vs. Direct LLM SCT Generation**: Translating high-level abstract graphs into GBNF-bridge and execution nodes on-device was selected over prompting the cloud model to generate them directly. On-device expansion keeps cloud prompts extremely stable and isolates local GBNF constraints from cloud planners.
- **Hybrid Branch Evaluator vs. Pure Semantic Evaluator**: A hybrid approach (fast deterministic JSONPath checking with semantic Qwen/Llama fallback) was chosen over pure semantic model evaluation. The hybrid approach resolves exact criteria instantly without inference latency, while the semantic fallback prevents false-negative branch prunings.
