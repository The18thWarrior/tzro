# Wiki Operation Log

Chronological append-only record of wiki operations and major agent engineering activities.

---

## [2026-05-25T17:49:00Z] feature | Implement Verbose Mode and Clean Stderr Logging for CLI Benchmarks

- **Activity**: Added a new `--verbose` (`-v`) flag to the offline benchmark CLI suite, and decoupled all internal process logging to ensure that the command line's standard output (`stdout`) produces strictly valid minified JSON without diagnostic noise.
- **Key Decisions**:
  - Redirected all internal GGUF sidecar model management messages and progress indicators from `stdout` to standard error (`stderr`).
  - Decoupled all intermediate execution step outputs, parallel level runs, and GBNF tool parameter extractions in the core `Executor` from `stdout` to `stderr`.
  - Bound the human-readable tabular results and analytics summaries to `stderr` exclusively when the `--json` and `--verbose` flags are utilized together, ensuring that standard file redirection (`>`) receives clean parseable JSON.
- **Files Created/Modified**:
  - [MODIFY] [runner.go](../../internal/benchmark/runner.go) (Redirected sidecar prints to stderr)
  - [MODIFY] [benchmark.go](../../internal/cli/benchmark.go) (Added `--verbose` flag and plumbed output stream routing)
  - [MODIFY] [cli_test.go](../../internal/cli/cli_test.go) (Added TestCLI_BenchmarkFlagResolution)
  - [MODIFY] [executor.go](../../internal/executor/executor.go) (Redirected executor execution logs to stderr)
  - [MODIFY] [log.md](log.md) (Logged this entry)

## [2026-05-24T22:58:00Z] analysis | Compiled and Translated 800 Raw Local BFCL Dataset Cases

- **Activity**: Wrote and executed a robust format compiler script `convert_bfcl.py` to parse all 4 raw local dataset files (BFCL multi-turn base, multiple, parallel, and parallel-multiple) checked out under `testdata/bfcl/`.
- **Key Findings**:
  - Dynamically cleaned and mapped raw parameter definitions containing `"type": "dict"` to JSON Schema GBNF-compliant `"type": "object"`.
  - Reconstructed turn-based conversational loops and parallel schema tool call definitions, producing a unified full-scale dataset of 800 offline test cases ready to execute natively within the tzro CLI.
- **Files Created/Modified**:
  - [NEW] [convert_bfcl.py](internal/benchmark/testdata/convert_bfcl.py) (Local dataset format compiler)
  - [NEW] [bfcl_full_samples.json](internal/benchmark/testdata/bfcl_full_samples.json) (Unified 800 offline test cases)
  - [MODIFY] [log.md](log.md) (Logged execution activity)

## [2026-05-24T22:25:00Z] analysis | PRD: Durable DAG Benchmarking Suite

- **Activity**: Formulated the PRD and mapped out the dynamic tool-mocking and simulation framework to evaluate the Go-powered tzro execution engine against the Berkeley Function Calling Leaderboard (BFCL) and ComplexFuncBench datasets.
- **Key Findings**:
  - Re-routing multi-turn and multi-step conversational benchmarks to a structured DAG planning engine enables evaluating whether compiling tasks into graphs is superior to conversational loops.
  - Designing a dynamic Go-based tool registry adapter allows loading benchmark tool schemas dynamically without cluttering production integrations.
- **Files Created/Modified**:
  - [NEW] [.scratch/benchmarking-suite/PRD.md](../../.scratch/benchmarking-suite/PRD.md) (Feature PRD)
  - [NEW] [benchmarking-suite.md](features/benchmarking-suite.md) (Feature Summary page)
  - [MODIFY] [index.md](index.md) (Added link under Features & PRDs)
  - [MODIFY] [log.md](log.md) (Appended log entry)

## [2026-05-24T18:08:00Z] diagnose | Fix Missing Embedding SQLite Column & Cloud Schema Format HTTP 400

- **Activity**: Investigated and diagnosed two critical chat bugs reported by a user: 1) SQLite query failures due to a missing `embedding` column on existing databases; 2) Cloud API `HTTP 400` errors on structured outputs when calling OpenAI-compatible / Gemini endpoints.
- **Key Findings**:
  - Pre-existing SQLite database files on disk were created before local ONNX vector memory tables support, so `CREATE TABLE IF NOT EXISTS` did not add the new `embedding` column. Added a dynamic PRAGMA-based SQLite alter-table migration.
  - Public cloud APIs (Gemini & OpenAI) do not accept standard/llama-server specific custom schema payloads under `"json_object"` `response_format`. Omitted the schema from the payload when communicating with remote APIs.
- **Files Created/Modified**:
  - [NEW] [missing-embedding-column-and-cloud-schema-400.md](bugs/missing-embedding-column-and-cloud-schema-400.md) (Bug post-mortem wiki page)
  - [MODIFY] [memory.go](../../internal/memory/memory.go) (Added SQLite column existence check & ALTER migration)
  - [MODIFY] [cloud_model.go](../../internal/inference/cloud_model.go) (Omitted custom schema key from remote response_format payloads)
  - [MODIFY] [memory_test.go](../../internal/memory/memory_test.go) (Added TestSqliteDatabase_SchemaMigration to verify schema evolution)
  - [MODIFY] [index.md](index.md) (Linked bug in index list)

---

## [2026-05-24T17:22:00Z] analysis | Document Tool Source Paradigms — WASM vs MCP Overlap Analysis

- **Activity**: Analyzed the functional overlap between the four tool source paradigms (Builtin, WASM, OpenAPI, MCP) in response to the question "do WASM tools and Docker MCPs functionally overlap?" Researched all three subsystems (`internal/wasm/`, `internal/mcp/`, `internal/tools/`) in parallel, compared their trust/weight/capability profiles, and documented the conclusion that they are complementary layers, not competing options.
- **Key Findings**:
  - WASM targets **pure compute with zero ambient authority** (no FS, no network, no env vars, sub-millisecond startup).
  - MCP targets **rich external I/O** via persistent child processes with auto-recovery (Slack, DBs, filesystems).
  - OpenAPI targets **declarative HTTP integrations** parsed from specs with auth injection.
  - Builtin targets **trusted platform primitives** as in-process Go functions.
  - Minimal overlap exists only for trivial self-contained tools; WASM is strictly better for that case.
  - All four share the same unified dispatch path through `tools.Registry` — the agent never knows the source.
- **Files Created/Modified**:
  - [NEW] [tool-source-paradigms.md](architecture/tool-source-paradigms.md) (Architecture wiki page)
  - [MODIFY] [index.md](index.md) (Added link under Architecture & Concepts)
  - [MODIFY] [log.md](log.md)

---

## [2026-05-24T17:12:00Z] grill-with-docs | Resolve Pre-compiled llama-server Sidecar Distribution

- **Activity**: Grilling session resolved the binary distribution model for the `llama-server` sidecar process.
- **Key Decisions**:
  - Adopt **Pre-compiled Static Binary Downloads** (Option 2) in the `install.sh` bootstrapper.
  - Skip dynamic local compiling (which is slow and compiler-dependent) to guarantee a sub-10 second, zero-friction install flow. The installer will automatically query host architecture and download pre-built, platform-optimized static binaries (Apple Silicon Metal-enabled, Intel CPU, Linux AVX2/CPU) directly to `~/.tzro/bin/`.

## [2026-05-24T17:11:00Z] grill-with-docs | Defer Multi-User REST/SSE Security in Favor of Local-First Simplicity

- **Activity**: Grilling session challenged the complexity of multi-user REST/SSE auth gateways for the GA developer release, deciding to keep the core engine tightly focused on local-first single-user developer execution.
- **Key Decisions**:
  - Defer the entire **Multi-User REST/SSE Gateway Security** sprint item (JWT, OIDC) to the post-GA roadmap.
  - Prioritize standard local-first single-user workstation execution. Secure local communication via standard POSIX loopback binding (`127.0.0.1`) and filesystem permission boundaries rather than opinionated HTTP authentication models, preserving zero-setup onboarding.

## [2026-05-24T15:35:00Z] grill-with-docs | Deprecate OS Keychain in Favor of Environment-Delegated Secrets

- **Activity**: Grilling session challenged the necessity of native OS-level keyring integrations for developer environments, choosing to simplify the credential boundary.
- **Terminology Resolved**:
  - **Delegated Secret** added: A sensitive runtime credential (e.g. API key, access token) that is referenced dynamically via an environment variable prefix (such as `$`) in configuration JSONs and resolved on-demand from the host environment, keeping configurations clean and credential-free.
- **Key Decisions**:
  - Deprecate the complex native OS keychain/keyring libraries entirely to avoiddynamic locking prompts, dynamic linking bugs, and CI/CD pipeline breakage.
  - Establish **Delegated Secrets** as the standard secret management paradigm. Extend the existing runtime environment resolution in the config parser to recursively resolve `$VAR` references for all critical parameters, including the main `CloudAPIKey` inside `.tzro/config.json`.

## [2026-05-24T15:16:00Z] grill-with-docs | Design Multi-Tier Hybrid Vector Search for Graph-RAG

- **Activity**: Grilling session designed the high-performance local vector similarity query engine in SQLite.
- **Terminology Resolved**:
  - **Hybrid Vector Search** added: A multi-tier retrieval process that runs keyword query filtering first to generate a candidate node pool, followed by local ONNX cosine similarity matching to rank starting nodes for Graph-RAG neighborhood traversal.
- **Key Decisions**:
  - Adopt **Multi-Tier Search** (Option 3) to execute vector matching on-device. Standard SQLite FTS5 indexes prune the graph to up to 100 candidate starting nodes. Then, standard Go memory cosine-similarity ranks the candidates using the preloaded `all-MiniLM-L6-v2` 30MB ONNX model embeddings, preserving cross-platform zero-dependency compiler simplicity.

## [2026-05-24T14:43:00Z] grill-with-docs | Resolve Docker Environment Inheritance for Containerized MCP Hosts

- **Activity**: Grilling session resolved the secure environment inheritance model for containerized MCP Hosts.
- **Terminology Resolved**:
  - **Containerized MCP Host** added: An **MCP Host** that runs inside an isolated, resource-constrained container (e.g. Docker) rather than directly on the host OS, utilizing strict host environment variable declaration.
- **Key Decisions**:
  - Adopt **Strict Declaration** (Option 2) for passing environment variables to containerized MCP Hosts. The engine resolves host-level environment references first and injects only these declared variables into the docker run command parameters, maintaining absolute sandboxing security without exposing host ambient keys.

## [2026-05-24T03:08:00Z] tdd | Implement Task-to-Workflow Promotion Engine

- **Activity**: Implemented the complete "Task-to-Workflow Promotion Engine" under strict TDD. Integrated semantic temporal wait triggers, Human-in-the-Loop check-point detection, 2-hop BFS toolCap neighborhood constraints (>12 tools/skills) on the SQLite Relational Knowledge Graph, and structured workflow task decomposition logic. Added database initialization resilience checks to `GetSkills`, `GetWorkflows`, and `GetWorkflowTasks` to guarantee stability in uninitialized testing environments.
- **Testing & Verification**:
  - Implemented comprehensive unit tests (`TestTaskToWorkflowPromotionEngine_TemporalAndHITL` and `TestTaskToWorkflowPromotionEngine_ToolCap`) in `internal/classifier/classifier_test.go`.
  - Verified 100% test success rate across the entire repository test suite (`go test ./...` completely green).
- **Key Files Created/Modified**:
  - [NEW] [promotion.go](../../internal/classifier/promotion.go) (Unified promotion criteria & BFS neighborhood counting)
  - [MODIFY] [classifier.go](../../internal/classifier/classifier.go) (Integrated promotion intercept points inside Classify and ClassifyComplexity)
  - [MODIFY] [classifier_test.go](../../internal/classifier/classifier_test.go) (Comprehensive TDD suite)
  - [MODIFY] [memory.go](../../internal/memory/memory.go) (Added DB nil checks to GetSkills, GetWorkflows, and GetWorkflowTasks)
  - [NEW] [task-workflow-promotion.md](architecture/task-workflow-promotion.md) (Wiki documentation)

---

## [2026-05-23T19:50:00-07:00] grill-with-docs | Establish Multi-Tier Task-to-Workflow Promotion Engine

- **Activity**: Continued grilling session established three core dimensions for the Task-to-Workflow Promotion Engine: Tool Cap Scale (12-tool cap), Temporal Scale (durability/deferral wait-states), and Operational Scale (state resiliency and human-in-the-loop validation checkpoints).
- **Key Decisions**:
  - Implement a dynamic `Promotion Engine` inside the intent classifier that intercepts prompts matching any of the three boundaries.
  - Automatically promote temporal delay requests (e.g. cron, "wait 3 days") to Workflows, preventing long-running thread blocks.
  - Automatically promote requests specifying human dry-run checkpoints to Workflows, allowing state resume-from-checkpoint in SQLite.
- **Files Created**:
  - [NEW] [handoff-tzro-task-vs-workflow.md](/tmp/handoff-tzro-task-vs-workflow.md) (Dedicated threshold handoff)

---

## [2026-05-23T18:45:00-07:00] grill-with-docs | Design WASM Sandboxed Micro-Skills & Relational Tool Graph Routing

- **Activity**: Grilling session resolved 3 critical architectural decisions for secure local-first execution. Defined the domain term **Sandboxed Micro-Skill**, aligned on dynamic first-class WASM tool registration in `.tzro/wasm/`, designed a Relational Tool Graph helper selection using 2-hop BFS, and established Task-to-Workflow Promotion criteria for safe 10-15 tool caps.
- **Terminology Resolved**:
  - **Sandboxed Micro-Skill** added: A compiled WebAssembly binary containing specialized logic executed safely on-device with strict, isolated resource limits.
- **Key Decisions**:
  - Store `.wasm` binaries and GBNF `.json` schemas on disk under `.tzro/wasm/` for performance and easy compilation.
  - Dynamically register each WASM skill as a first-class tool in the dynamic registry, abstracting execution details from the compiler.
  - Model all tools/skills as nodes in the Relational Knowledge Graph, using neighborhood BFS to dynamically load intermediate dependencies.
  - Establish a 12-tool cap breakpoint: if tools exceed the cap, promote the user request to a Workflow composed of decoupled sub-tasks.
- **Files Modified**:
  - [MODIFY] [CONTEXT.md](../../CONTEXT.md) (added glossary term for Sandboxed Micro-Skill)
  - [MODIFY] [log.md](log.md)

---

## [2026-05-23T14:15:00-07:00] tdd | Fix non-deterministic column sorting in cache compaction pipeline

- **Activity**: Resolved a flaky test in the JQ caching compaction pipeline (`internal/cache/cache.go`) under strict TDD. Go's randomized map iteration caused column header and row cell order to be non-deterministic in `tabularJSONToTSV`, breaking standard expected test assertions. Sorted header slices alphabetically (`sort.Strings(headers)`) prior to TSV construction to guarantee 100% stable, deterministic column representations.
- **Testing & Verification**:
  - Updated `TestProcess_IntegrationPruning` in `internal/cache/cache_test.go` to match the alphabetically ordered header `"Age\tName"`.
  - Ran `go test ./...` validating that all test suites across the repository are completely green.
- **Key Files Modified**:
  - [MODIFY] [cache.go](../../internal/cache/cache.go) (Imported `sort` and sorted TSV headers)
  - [MODIFY] [cache_test.go](../../internal/cache/cache_test.go) (Updated test assertion to expect deterministic headers)

---

## [2026-05-22 23:45] tdd | Implement unified CLI client and interactive Bubble Tea TUI dashboard

- **Activity**: Implemented the complete developer CLI (`tzro`), standard REST/SSE daemon endpoints, and fullscreen Bubble Tea TUI dashboard under strict TDD. Relocated daemon launching to `cmd/tzrod` (split binary pattern) and established read-only offline SQLite constraints to preserve StreamBus telemetry logging and Observer Agent visibility.
- **Testing & Verification**:
  - Implemented extensive unit and integration tests across two packages: `internal/cli/client_test.go`, `internal/cli/cli_test.go`, `internal/tui/app_test.go`, `internal/tui/telemetry_test.go`, and `internal/tui/graph_test.go`.
  - Resolved Go compile package import cycles by decoupling structural interface types into a dependency-free package at `internal/tui/client.go`.
  - Verified 100% green status across all new test suites under strict TDD vertical slices.
- **Key Files Created/Modified**:
  - [NEW] [main.go](../../cmd/tzrod/main.go) (Isolated server daemon launcher)
  - [NEW] [main.go](../../cmd/tzro/main.go) (Unified CLI and full TUI launcher)
  - [DELETE] [main.go](../../main.go) (Original root launcher)
  - [NEW] [client.go](../../internal/tui/client.go) (Decoupled client interfaces, breaking import cycles)
  - [NEW] [client.go](../../internal/cli/client.go) & [client_test.go](../../internal/cli/client_test.go) (REST and read-only offline SQLite client implementations)
  - [NEW] [root.go](../../internal/cli/root.go) & [cli_test.go](../../internal/cli/cli_test.go) (Cobra flag parsers, JSON formatters, and client heuristics loader)
  - [NEW] [subcommands](../../internal/cli/) (Implementations for task, memory, skill, mcp, sidecar, stream, chat, notify commands)
  - [NEW] [app.go](../../internal/tui/app.go) & [app_test.go](../../internal/tui/app_test.go) (Core Bubble Tea navigation loop styled with violet HSL select indicators)
  - [NEW] [telemetry.go](../../internal/tui/telemetry.go) & [telemetry_test.go](../../internal/tui/telemetry_test.go) (Leak-free background SSE scanner relay utilizing context cancels)
  - [NEW] [graph.go](../../internal/tui/graph.go) & [graph_test.go](../../internal/tui/graph_test.go) (Kahn DAG Column visualizer and Tree layout renderer with Tab visual toggle)
  - [MODIFY] [server.go](../../internal/server/server.go) (Added POST /api/memories endpoint support in server daemon)
- **Design Outcomes**:
  - Achieved complete split command segregation, compiling isolated client `bin/tzro` and server `bin/tzrod` binaries.
  - Enforced bulletproof read-only safety constraints in offline mode, protecting event channels and writer contentions.
  - Enabled deep scripting capabilities via minified JSON piping commands, tabular console text formats, and POSIX-aligned exit codes.
  - Created a visually rich, context-safe fullscreen TUI dashboard supporting layout toggles (Tab/v key) and SSE cancellation.

---

## [2026-05-22 23:30] grill-with-docs | Brainstorm CLI & TUI developer tools architecture and constraints

- **Activity**: Brainstorming and grilling session resolved 4 critical design and architectural choices for implementing a developer CLI (`tzro`) and Bubble Tea-powered TUI dashboard in `tzro`.
- **Terminology Resolved**:
  - **Split Pattern**: Complete separation of the daemon server (`tzrod`) and client CLI/TUI (`tzro`) executables.
  - **Client-Server REST/SSE Mediation**: Enforcing daemon-mediated writes for absolute state logging, telemetry stream integrity, and preventing SQLite concurrent lock contentions.
- **Key Decisions**:
  - Transition to standard command sub-directories under `cmd/tzrod/` (daemon) and `cmd/tzro/` (client).
  - Implement read-only offline database inspection (`--offline`), but strictly block any local write operations, forcing all mutations (like memories, workflow triggers) through the daemon REST/SSE APIs.
  - Implement a clean client abstraction `TZROClient` to seamlessly route operations depending on state.
  - Architect Bubble Tea TUI with a single, context-scoped SSE StreamBus reader subscription to prevent thread starvation and memory leaks.
  - Construct a dual-layout visual toggle for the Kahn DAG Task executor display (Column layout vs Indented row-hierarchy tree) triggered by the `Tab` or `v` key.
  - Implement command line tabular layouts, pervasive `--json` scripting flags, and POSIX exit codes mapped to `ToolResult` envelopes for shell scripting capabilities.
- **ADRs**:
  - Created [ADR-0013](../adr/0013-unified-daemon-mediated-state-mutations.md) - Unified Daemon-Mediated State Mutations.
- **Files Touched**:
  - [NEW] [0013-unified-daemon-mediated-state-mutations.md](../adr/0013-unified-daemon-mediated-state-mutations.md)
  - [MODIFY] [index.md](index.md)
  - [MODIFY] [log.md](log.md)

---

## [2026-05-22 22:30] grill-with-docs | Design Durable Proactive Notification System

- **Activity**: Grilling session resolved 5 core architectural and design decisions for creating a well-architected notification system in tzro. Established hybrid SQLite persistence and StreamBus push-delivery, custom categorization rules (`info`, `warning`, `error`, `action_required`), deep-linking via a nullable `target_id`, and batch rollup/deduplication heuristics within the background Observer Agent.
- **Terminology Resolved**:
  - **Durable Notification** added: A structured alert record persisted in SQLite and dispatched over the StreamBus, allowing background Tasks, Workflows, or the Observer Agent to communicate asynchronous lifecycle states, warnings, or action requests to the user across restarts and page refreshes.
- **Key Decisions**:
  - Store notifications durably in SQLite using `durable_notifications` table with fields for linkages (`task_id`, `workflow_id`, `target_id`) and status tracking.
  - Hybrid notification delivery: Publish in SQLite and immediately broadcast over the `StreamBus` with source `"notification"` for reactive SSE UI tracking.
  - Support high-aesthetic, interactive glassmorphic Bell icon panel and pulsating unread counts in frontend dashboard.
  - Implement dynamic frequency deduplication inside Go publisher and consolidated task rollups inside the Observer Agent to mitigate notification fatigue.
- **ADRs**:
  - Created [ADR-0012](../adr/0012-durable-proactive-notification-system.md) - Durable Proactive Notification System.
- **Files Touched**:
  - [MODIFY] [CONTEXT.md](../../CONTEXT.md) (added glossary term for Durable Notification)
  - [NEW] [0012-durable-proactive-notification-system.md](../adr/0012-durable-proactive-notification-system.md)
  - [MODIFY] [index.md](index.md)

---

## [2026-05-22 22:04] tdd | Implement 16 Standalone Agentic Tools and Local Database Workspace

- **Activity**: Integrated 16 platform-agnostic standalone agentic tools under strict TDD. Exposed discovery, web search, Graph-RAG (Neighborhood multi-hop nodes/edges), long-term memories (Add/Recall/Forget facts), Kahn orchestrator tasks, and 6 sandboxed local SQLite CRUD databases.
- **Testing & Verification**:
  - Implemented standalone registry tests and a complete database workspace CRUD integration suite in `internal/tools/standalone_tools_test.go`.
  - Ran global repository tests (`go test -v ./...`), showing 100% green status across all packages (telemetry, inference local_model, memory SQL nodes, skills synthesis, task kahn compilations, and standalone tools).
- **Key Files Created/Modified**:
  - [NEW] [envelope.go](../../internal/tools/envelope.go) (Standardized ToolResult execution telemetry schemas and wrappers)
  - [NEW] [standalone_tools.go](../../internal/tools/standalone_tools.go) (16 tools logic with strict read-only SELECT and non-empty WHERE safety validation rules)
  - [NEW] [standalone_tools_test.go](../../internal/tools/standalone_tools_test.go) (Comprehensive registry discovery and CRUD database workspace TDD integration tests)
  - [MODIFY] [tools.go](../../internal/tools/tools.go) (Static registration hook addition inside Init)
- **Design Outcomes**:
  - Converted the agent from a stateless text pipeline into a powerful structured workspace engine.
  - Mitigated context windward bloat via local database staging and JQ caching capabilities.
  - Enforced bulletproof safety rails on all database modifications and freeform SQL executes.

---

## [2026-05-22 15:23] improve-codebase-architecture | Separate Workflow Orchestration and Task Planning Domains

- **Activity**: Separated the Workflow Orchestration domain from the Task Planning/Compilation domain. Relocated all LLM DAG planning (`planTaskDAG`, `planWithCloud`), heuristic graph compilation (`buildHeuristicGraph`), and Kahn topological sorting logic out of the workflow orchestrator and the HTTP chat server (`internal/server/server.go`) into a unified `internal/task` package. Established a deep Task Engine seam: `task.Execute` is the single high-leverage entrypoint that orchestrates the entire planning, compiling, and parallel levels-based execution flow. Simplified `orchestrator.go` to strictly manage task running execution states and dependencies without compiling or scheduling graphs directly.
- **Testing & Verification**:
  - Wrote comprehensive unit tests in `internal/task/task_test.go` checking the heuristic planner, Kahn topological sorting levels, and cloud fallback behaviors.
  - Ran full Go backend test suite (`go test ./...`) showing 100% green status across all packages (`internal/task`, `internal/workflow`, `internal/server`, `internal/executor`).
  - Successfully compiled the workspace binary via `go build -o tzro_engine main.go` without any errors.
- **Key Files Created/Modified**:
  - [NEW] [task.go](../../internal/task/task.go) & [task_test.go](../../internal/task/task_test.go) (Unified planning compilation and deep execution seam)
  - [MODIFY] [orchestrator.go](../../internal/workflow/orchestrator.go) (Decoupled execution loop, deleted duplicate planners/sorters, resolved shadowing bugs)
  - [MODIFY] [server.go](../../internal/server/server.go) (Refactored chat handler to use task.Plan, deleted legacy helper functions)
  - [NEW] [0011-deep-task-engine-seam-and-domain-separation.md](../adr/0011-deep-task-engine-seam-and-domain-separation.md) (ADR documenting architectural outcomes)
- **Design Outcomes**:
  - Eliminated planning code duplication across the HTTP server and workflow coordinator.
  - Significantly reduced architectural coupling: `internal/workflow` no longer imports or directly executes through `compiler` or `executor`, relying completely on the deep `task.Execute` interface.
  - Enabled isolated, robust unit testing of DAG planning and Kahn topological sorting in the new `task` package.

---

## [2026-05-22 15:20] improve-codebase-architecture | Consolidate Disk-Backed JQ Cache module

- **Activity**: Consolidated the Disk-Backed JQ Cache module to resolve architectural leakage, increase locality, and improve testability. Decoupled process-based JQ CLI execution, paging algorithms, and fallback filters out of `internal/executor/executor.go` and into a new unified `internal/cache` package. Defined the `CacheStore` interface (seam) for cache interactions, hiding all private SQLite storage and fallback filesystem persistence details.
- **Testing & Verification**:
  - Implemented robust unit and integration tests inside `internal/cache/cache_test.go` utilizing an isolated test SQLite connection and verifying store, pagination, file backup fallbacks, and JQ CLI execution with basic regexp fallbacks.
  - Adapted executor cache tests inside `internal/executor/executor_test.go` to test strictly through the `CacheStore` default interface.
  - Ran workspace-wide tests via `go test ./...` and confirmed all suites passed successfully with 100% green status.
- **Key Files Created/Modified**:
  - [NEW] [cache.go](../../internal/cache/cache.go) & [cache_test.go](../../internal/cache/cache_test.go) (Unified cache package & TDD tests)
  - [MODIFY] [executor.go](../../internal/executor/executor.go) & [executor_test.go](../../internal/executor/executor_test.go) (Executor simplification & test adaptation)
  - [MODIFY] [memory.go](../../internal/memory/memory.go) (Exposing RawDB() handle & legacy cache method removal)
- **Design Outcomes**:
  - Achieved strong architectural encapsulation: the executor acts strictly via the `CacheStore` seam without exposing underlying DB tables, SQL queries, or direct filesystem paths.
  - Improved modular locality and depth, keeping process executions, paging slices, and custom JQ fallbacks completely private within the cache package.
  - Enhanced unit testability of the cache subsystem in complete isolation from the executor runtimes.

---

## [2026-05-22 13:30] tdd | Advanced Graph-RAG neighborhood traversal and context injection

- **Activity**: Implemented advanced multi-hop neighborhood search traversal with customizable node/edge filters in `internal/memory/memory.go`, and integrated the resulting Graph-RAG subgraphs directly into systems prompts across the executor and server packages.
- **Testing & Verification**:
  - Wrote a comprehensive test suite in `internal/memory/memory_test.go` covering all filter parameters (`WithNodeTypes`, `WithEdgeTypes`, `WithMinNodeWeight`, `WithMinEdgeWeight`, `WithLimit`, `WithDirection`) and Graph-RAG extraction/word-boundary boundary conditions.
  - Ran the complete test suite successfully (`go test ./...` passed with 100%).
- **Key Files Created/Modified**:
  - [MODIFY] [memory.go](../../internal/memory/memory.go) (Custom BFS filters, functional options, word-boundary scans, context Markdown serialization)
  - [MODIFY] [memory_test.go](../../internal/memory/memory_test.go) (Comprehensive option filter & context extraction test suite)
  - [MODIFY] [executor.go](../../internal/executor/executor.go) (Local/Cloud executor system prompt context injection)
  - [MODIFY] [server.go](../../internal/server/server.go) (Conversational fast-path & Strategic cloud planner system prompt context injection)
- **Design Outcomes**:
  - 100% backward-compatible variadic options pattern API preserving all client calls.
  - Fully robust and deterministic case-insensitive, word-bounded Graph-RAG extraction preventing prompt pollution.
  - Zero panics via defensive DB initialization checks in public reading paths.

---

## [2026-05-22 13:15] triage | Technical Architecture Evaluation

- **Activity**: Generated a comprehensive high-level technical overview of the `tzro` application's local-first architecture and completed an evaluation against the X execution framework specification for a technical audience.
- **Testing & Verification**:
  - Ran the entire Go backend unit-testing suite (`go test ./...`); all packages successfully compiled and passed, validating the health of the Kahn Graph Compiler, classification, stream routing, memory persistence, and local inference server processes.
- **Key Files Created/Modified**:
  - [NEW] [technical_architecture_overview.md](file:///Users/jp/.gemini/antigravity/brain/a675dc94-eea9-4f45-adf4-188214cae823/technical_architecture_overview.md) (Detailed structural blueprint evaluation)
- **Design Outcomes**:
  - Confirmed 100% architectural alignment between our Go implementation and the eight primary pillars of the X execution specification.
  - Documented minor prospective optimizations (semantic deduplication, host memory high-water mark KV cache GC, and MCP child process auto-recovery) to guide future engineering work.

---

## [2026-05-22 08:00] tdd | LLM Classifier & Streaming Inference Implementation

- **Activity**: Implemented the full durable-execution LLM streaming and reactive architecture plan under strict TDD. Created thread-safe pub/sub bus, streaming inference wrappers, LLM intent/complexity classifier, persistent SSE route, and a completely push-based GUI using React EventSource.
- **Testing & Verification**:
  - Implemented extensive unit and integration tests across 6 backend packages.
  - Verified 100% test pass rate with simulated local sidecar downtime, concurrency checks, non-blocking overflow drops, and client context terminations.
  - Built and bundled production-ready Vite frontend statically to `./static/` with zero compiler warnings.
- **Key Files Created/Modified**:
  - [NEW] [bus.go](../../internal/stream/bus.go) & [bus_test.go](../../internal/stream/bus_test.go) (StreamBus core pub/sub)
  - [MODIFY] [local_model.go](../../internal/inference/local_model.go) & [local_model_test.go](../../internal/inference/local_model_test.go) (Inference stream parser)
  - [MODIFY] [executor.go](../../internal/executor/executor.go) & [executor_test.go](../../internal/executor/executor_test.go) (Streaming cloud node executor)
  - [MODIFY] [classifier.go](../../internal/classifier/classifier.go) & [classifier_test.go](../../internal/classifier/classifier_test.go) (LLM classifier and hybrid complexity routing)
  - [MODIFY] [server.go](../../internal/server/server.go) & [server_test.go](../../internal/server/server_test.go) (Persistent SSE events channel)
  - [MODIFY] [App.tsx](../../web/src/App.tsx) (EventSource reactive state-ref UI tracking, bouncing dot typers, observer audit badges, promotion banners)
- **Design Outcomes**:
  - 100% real-time push model replacing high-frequency REST polling.
  - Bulletproof React ref-sync preventing any stale closure runtime bugs.
  - Graceful multi-tier fallback (Local Model -> Heuristics -> Cloud Model) for zero downtime.

---

## [2026-05-22 07:47] grill-with-docs | LLM Classifier + Streaming Architecture

- **Activity**: Grilling session resolved 15 design decisions for LLM-powered classification and streaming inference output. Established the Local/Cloud routing principle, StreamBus pub/sub architecture, persistent SSE event pipe, and cooperative-mode complexity gate.
- **Terminology Resolved**:
  - "Local Step Executor" → **Local Model** (broadened: default workhorse for all structured work, not just step execution)
  - **Cloud Model** added (exception-path LLM for knowledge, latency, T2 oversight)
- **Key Decisions**:
  - Classification follows X framework spec (LLM-backed intent + heuristic-first complexity with LLM fallback)
  - Cooperative mode: Local classifies → T0 chat to Cloud (streamed) → T1+ promotes to Task (user notified)
  - JSON schema via `response_format` for structured output (both providers)
  - Persistent `GET /api/events` SSE pipe replaces all polling; REST endpoints stay for page-load hydration
  - StreamBus: separate pub/sub for token fan-out (not Observer channel)
  - Two inference variants: blocking (short/utility calls) + streaming (user-facing), both report to StreamBus
  - Sidecar-down fallback: heuristic → cloud on inconclusive
  - SSE envelope: `streamId/source/taskId/nodeId/type`
- **ADRs**:
  - Created [ADR-0009](../adr/0009-stream-bus-for-inference-fan-out.md) — StreamBus for inference fan-out
  - Created [ADR-0010](../adr/0010-local-default-cloud-exception-routing.md) — Local-default cloud-exception routing
- **Files Touched**:
  - [CONTEXT.md](../../CONTEXT.md) — added Cloud Model, broadened Local Model, extended example dialogue
  - [0009-stream-bus-for-inference-fan-out.md](../adr/0009-stream-bus-for-inference-fan-out.md)
  - [0010-local-default-cloud-exception-routing.md](../adr/0010-local-default-cloud-exception-routing.md)
  - [index.md](index.md)

---

## [2026-05-22 05:35] grill-with-docs | llama-server Sidecar Optimization

- **Activity**: Grilling session resolved 12 design decisions for optimizing the llama-server sidecar configuration. Implemented all changes across `internal/inference/local_model.go` and `internal/executor/executor.go`.
- **Decisions**: Platform-aware GPU offloading, 32K context window, mode-dependent KV cache quantization (q4_0 cooperative / q8_0 local), flash attention, n-gram speculative decoding (replacing draft model), cache reuse with per-call GC removal, universal sampling (temp 1.0 + min_p 0.1), slot save path for preemption, 16384 max prediction, split HTTP clients, random port allocation, accurate token counting from server response.
- **ADRs**:
  - Updated [ADR-0002](../adr/0002-local-gbnf-constraints.md) — n-gram speculation replaces 135M draft model
  - Created [ADR-0008](../adr/0008-mode-dependent-kv-cache-quantization.md) — mode-dependent KV cache quantization
- **Files Touched**:
  - [local_model.go](../../internal/inference/local_model.go)
  - [executor.go](../../internal/executor/executor.go)
  - [0002-local-gbnf-constraints.md](../adr/0002-local-gbnf-constraints.md)
  - [0008-mode-dependent-kv-cache-quantization.md](../adr/0008-mode-dependent-kv-cache-quantization.md)

---

## [2026-05-22 02:30] ingest | Local Wiki Integration
- **Activity**: Initialized the Local Wiki (LLM Wiki Style) under `docs/wiki/`. Established wiki structure, schemas, and templates. Updated all codebase engineering skills (`to-prd`, `diagnose`, `improve-codebase-architecture`, `grill-with-docs`, `to-issues`, `tdd`) to automatically populate, log, and maintain the wiki.
- **Files Touched**:
  - [AGENTS.md](../../AGENTS.md)
  - [wiki.md](../agents/wiki.md)
  - [local-wiki Skill](../local-wiki/SKILL.md)
  - [index.md](index.md)
  - [log.md](log.md)

---

## [2026-05-24 10:13] tdd | General Availability Roadmap Sprints 1, 2, and 3 Completed

- **Activity**: Successfully implemented the entire GA release roadmap Sprints 1, 2, and 3 under strict TDD vertical tracer bullet development, establishing complete workstation containerized subprocess sandboxing, high-concurrency Kahn executors, local ONNX Graph-RAG hybrid searches, environment secrets, and developer onboarding bootstrapper tools.
- **Testing & Verification**:
  - Implemented 100% green unit and integration tests across all packages, verified by running `go test -race ./...`.
  - Built comprehensive integration tests for the bash installer (`install_test.go`) mocking sidecar downloads and asserting POSIX layout & SQLite boots.
  - Built complete quickstart integration tests (`examples/quickstart/main_test.go`) utilizing a mock SSE streaming server.
- **Key Files Created/Modified**:
  - [MODIFY] [mcp.go](../../internal/mcp/mcp.go) & [mcp_test.go](../../internal/mcp/mcp_test.go) (Docker sandboxed stdio daemons)
  - [MODIFY] [executor.go](../../internal/executor/executor.go) & [executor_test.go](../../internal/executor/executor_test.go) (Parallel concurrency & WASM timeout fixes)
  - [NEW] [embeddings.go](../../internal/embeddings/embeddings.go) & [embeddings_test.go](../../internal/embeddings/embeddings_test.go) (ONNX vector calculations & TF-IDF fallback)
  - [MODIFY] [memory.go](../../internal/memory/memory.go) & [memory_test.go](../../internal/memory/memory_test.go) (Hybrid vector search SQLite FTS5 traversal)
  - [MODIFY] [config.go](../../internal/config/config.go) & [config_test.go](../../internal/config/config_test.go) (Dynamic environment delegated secrets)
  - [MODIFY] [task.go](../../internal/task/task.go) & [task_test.go](../../internal/task/task_test.go) (Task planner dynamic secret routing)
  - [NEW] [install.sh](../../install.sh) & [install_test.go](../../install_test.go) (One-Line Developer Bash Installer)
  - [NEW] [main.go](../../examples/quickstart/main.go) & [main_test.go](../../examples/quickstart/main_test.go) (Go Quickstart Boilerplate examples)
  - [NEW] [README.md](../../docs/sdk/README.md) (Public SDK & API JSON specifications manual)
- **Design Outcomes**:
  - Zero platform dependencies for local vector embeddings via pure-Go TF-IDF bag-of-words fallback.
  - Multi-tier hybrid vector search (FTS5 + in-memory cosine ranking) achieving sub-millisecond semantic traversals.
  - Secure containerized subprocess sandboxing isolating host ambient variables.
  - Bulletproof path bootstrap and automated sqlite db initialization via offline TUI CLI queries.

---

## [2026-05-25 03:50] diagnose | Local Sidecar Inactive / Benchmark API Key Loading Bug Fix

- **Activity**: Diagnosed and fixed benchmark execution failures caused by uninitialized configurations in CLI processes. Prevented configuration file pollution by establishing safe memory-only configuration overrides during test and mock runs, restoring original user config settings, and recompiling all binaries.
- **Testing & Verification**:
  - Verified 100% green test pass rate by running `go test ./...` which successfully compiled and executed all packages in ~11 seconds.
  - Successfully executed CLI-level benchmark queries verifying correct configuration loading and fallback.
- **Key Files Created/Modified**:
  - [MODIFY] [root.go](../../internal/cli/root.go) (Added PersistentPreRunE to load `.tzro/config.json`)
  - [MODIFY] [config.go](../../internal/config/config.go) (Added `Override` for safe in-memory configurations)
  - [MODIFY] [runner.go](../../internal/benchmark/runner.go) (Switched to in-memory overrides and optimized test datasets)
  - [MODIFY] [runner_test.go](../../internal/benchmark/runner_test.go) (Switched test overrides to `config.Override`)
  - [NEW] [local-sidecar-inactive-benchmark-fallback-bug.md](bugs/local-sidecar-inactive-benchmark-fallback-bug.md) (Detailed bug post-mortem)

