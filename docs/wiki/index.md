# Local Wiki Index

Welcome to the persistent repository knowledge base for the `tzro` project.

- **Chronological Log**: [Log](log.md)

---

## Features & PRDs

_Map of system features, product requirements, and specs._

- [Durable DAG Benchmarking Suite](features/benchmarking-suite.md) - Evaluate model planning and parameter execution against BFCL and ComplexFuncBench datasets. (Sources: 2 | Last Updated: 2026-05-24)
- [Code Quality & Architectural Refactoring](features/code-quality-refactors.md) - Decompose monolithic runner.go and memory.go files into highly cohesive sub-modules and clean up hardcoded edge cases. (Sources: 1 | Last Updated: 2026-05-28)
- [Synchronous DAG Execution Hooks](features/dag-execution-hooks.md) - Middleware layer for synchronous task intercepting, validation, output mutation, and durable pausing. (Sources: 1 | Last Updated: 2026-05-31)

## Bugs & Post-Mortems

_Analyses of critical bugs, diagnostic loops, and prevention measures._

- [Benchmark Dataset Ground-Truth Corruption & Multi-Turn Label Shifting](bugs/benchmark-dataset-corruption-and-label-shifting.md) - Expose a systematic 1-turn lag in multi-turn test annotations and scrambled single-turn ground truths in the BFCL dataset. (Verified: 2026-05-25)
- [Missing Embedding SQLite Column & Cloud Schema Format HTTP 400](bugs/missing-embedding-column-and-cloud-schema-400.md) - Resolve SQL errors for missing DB vector columns and HTTP 400 errors for non-standard cloud response format payloads. (Verified: 2026-05-24)
- [Local Sidecar Inactive / Benchmark API Key Loading Bug](bugs/local-sidecar-inactive-benchmark-fallback-bug.md) - Fix configuration loading in CLI process for offline benchmarks and optimize test suite datasets to run unit tests in seconds instead of minutes. (Verified: 2026-05-25)
- [Cloud Planner Timeout & Heuristic Fallback Pollution](bugs/cloud-planner-timeout-and-heuristic-fallback-pollution.md) - Analyze cloud planner timeout bottlenecks and the static heuristic builder injecting unregistered tools during failures. (Verified: 2026-05-25)
- [Cooperative Engine Benchmark Evaluation (2026-05-27 Run 1)](bugs/benchmark-analysis-2026-05-27.md) - Deep diagnostic audit of 11 failures, establishing schema coercion and sequential de-noising action plans. (Verified: 2026-05-27)
- [Cooperative Engine Benchmark Evaluation (2026-05-27 Run 2)](bugs/benchmark-analysis-2026-05-27-1146.md) - Diagnostic analysis of Run 2 achieving a 68.00% success rate (+12.00% absolute increase) after deploying Schema-Aware Parameter Validation in harness. (Verified: 2026-05-27)
- [Cooperative Engine Benchmark Evaluation (2026-05-27 Run 3)](bugs/benchmark-analysis-2026-05-27-1205.md) - Full-scale 100-case diagnostic analysis of Run 3 maintaining a 64.00% success rate at 4x dataset scale. (Verified: 2026-05-27)
- [Cooperative Engine Benchmark Evaluation (2026-05-27 Run 4)](bugs/benchmark-analysis-2026-05-27-1304.md) - Full-scale 400-case diagnostic analysis of Run 4 achieving a 65.50% success rate at production scale. (Verified: 2026-05-27)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 10:58)](bugs/benchmark-analysis-2026-05-30-1058.md) - 5-case diagnostic of SCT bridge interpolation failures causing 40% pass rate; root cause is exec node output storage corruption. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 11:23)](bugs/benchmark-analysis-2026-05-30-1123.md) - 5-case diagnostic after RawOutput and coerceNumericArguments P0 fixes, achieving 60% pass rate. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 11:32)](bugs/benchmark-analysis-2026-05-30-1132.md) - First full-scale 100-case benchmark revealing category-dependent parameter extraction failures at 21% pass rate. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 13:00)](bugs/benchmark-analysis-2026-05-30-1300.md) - Post-coercion-fix 100-case benchmark showing marginal +1% improvement (22% pass rate); confirms post-extraction coercion ceiling reached. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 17:10)](bugs/benchmark-analysis-2026-05-30-1710.md) - Post-accumulated-context-architecture 10-case benchmark achieving 90% pass rate (+68% improvement); bimodal local/cloud execution split with Tier-2 sidecar recycle. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-30 Run 17:35)](bugs/benchmark-analysis-2026-05-30-1735.md) - Full-scale 100-case diagnostic validation of accumulated context architecture showing +41% net improvement (63% pass rate) but identifying dynamic parameter resolution offset shifts. (Verified: 2026-05-30)
- [Cooperative Engine Benchmark Evaluation (2026-05-31 Run 00:04)](bugs/benchmark-analysis-2026-05-31-0004.md) - Full-scale 100-case diagnostic validation of GBNF parameter matching achieving 65.0% pass rate (+2% improvement) but identifying the "\_exec" suffix mismatch and background thread sidecar orphanage bugs. (Verified: 2026-05-31)
- [Cooperative Engine Benchmark Evaluation (2026-05-31 Run 08:24)](bugs/benchmark-analysis-2026-05-31-0824.md) - Diagnostic analysis of GBNF parameter matching and transient network planning failures achieving a 61.0% pass rate. (Verified: 2026-05-31)
- [Cooperative Engine Benchmark Evaluation (2026-05-31 Run 11:16)](bugs/benchmark-analysis-2026-05-31-1116.md) - Full-scale 100-case diagnostic validation of GBNF parameter matching achieving 68.0% pass rate (+7.0% improvement) but identifying the "\_exec" suffix template mismatch. (Verified: 2026-05-31)
- [Cooperative Engine Benchmark Evaluation (2026-05-31 Run 13:25)](bugs/benchmark-analysis-2026-05-31-1325.md) - Full-scale 100-case diagnostic evaluation achieving 63.0% pass rate under cooperative model mode, triaging the \_exec suffix mismatch and topological concurrency ordering races. (Verified: 2026-05-31)
- [Cooperative Engine Benchmark Evaluation (2026-05-31 Run 15:15)](bugs/benchmark-analysis-2026-05-31-1515.md) - Full-scale 100-case diagnostic validation achieving 100.0% overall pass rate by resolving the "\_exec" suffix template mismatch and persistent sidecar daemon context orphanage bugs. (Verified: 2026-05-31)

## Architecture & Concepts

_Glossary terms, data models, ADR summaries, and architectural diagrams._

- [Repository Domain Context](../../CONTEXT.md) - Durable local-first agentic execution language glossary.
- [Technical Design](../technical-design.md) - Overview of the tzro durable local execution system.
- [MCP Setup & Integration Guide](../mcp-setup-guide.md) - Configure tzro as a stdio-based MCP server in Claude Desktop, Cursor, etc.
- [X Execution Framework](../dynamic-execution-framework.md) - Complete specs of the X execution layers and architecture.
- [Llama Server Sidecar](../llama-server-sidecar.md) - Spec of llama.cpp local server runtime interface.
- [Cooperative Local Cloud DAG Execution](../cooperative-local-cloud-dag-execution.md) - Hybrid model of local and remote coordination.
- [Hybrid Cloud Local Worker Execution](../hybrid-cloud-local-worker-execution.md) - Remote task dispatching mechanisms.
- [Disk-Backed JQ Cache Architecture](architecture/disk-backed-jq-cache.md) - Deep subsystem for context compaction, page-sliced pagination, and JQ process queries.
- [Task-to-Workflow Promotion Engine](architecture/task-workflow-promotion.md) - Deep subsystem that dynamically elevates Single Task DAGs to persistent Multi-Task Workflows.
- [Tool Source Paradigms](architecture/tool-source-paradigms.md) - Analysis of the four tool sources (Builtin, WASM, OpenAPI, MCP), their overlap, and why each exists.

### Architecture Decision Records (ADRs)

- [ADR-0001: Durable Go DAG Executor](../adr/0001-durable-go-dag-executor.md) - Defines the core DAG task executor engine.
- [ADR-0002: Local GBNF Constraints](../adr/0002-local-gbnf-constraints.md) - Outlines local worker grammar-constrained output.
- [ADR-0003: Proactive Observer Agent](../adr/0003-proactive-observer-agent.md) - Audit and monitor long-running workflow state machine.
- [ADR-0004: Event-Driven Micro-Skills](../adr/0004-event-driven-micro-skills.md) - Structured markdown SOP injection framework.
- [ADR-0005: 5-Layer Context Compaction & JQ Cache](../adr/0005-5-layer-context-compaction-and-jq-cache.md) - Flattening and pruning verbose payloads.
- [ADR-0006: Hybrid Memory & Relational Knowledge Graph](../adr/0006-hybrid-memory-and-relational-knowledge-graph.md) - Neighborhood Multi-Hop search database.
- [ADR-0007: MCP Dynamic Proxy](../adr/0007-mcp-dynamic-proxy.md) - stdio process mapping of tools.
- [ADR-0008: Mode-Dependent KV Cache Quantization](../adr/0008-mode-dependent-kv-cache-quantization.md) - KV cache quality tuning per model mode.
- [ADR-0009: StreamBus for Inference Fan-Out](../adr/0009-stream-bus-for-inference-fan-out.md) - Separate pub/sub for token streaming, not Observer channel.
- [ADR-0010: Local-Default Cloud-Exception Routing](../adr/0010-local-default-cloud-exception-routing.md) - Local Model is default, Cloud Model is exception path.
- [ADR-0011: Deep Task Engine Seam and Domain Separation](../adr/0011-deep-task-engine-seam-and-domain-separation.md) - Establishes unified task.Execute entrypoint and decouples workflow orchestration from planning/compilation.
- [ADR-0012: Durable Proactive Notification System](../adr/0012-durable-proactive-notification-system.md) - Design for persisted, real-time lifecycle notifications with target deep-linking and debouncing.
- [ADR-0013: Unified Daemon-Mediated State Mutations](../adr/0013-unified-daemon-mediated-state-mutations.md) - Enforces pure client-server daemon communication for all state mutations to preserve telemetry and prevent write conflicts.
- [ADR-0014: Stateful Graph-Aligned Multi-Turn Benchmarks](../adr/0014-stateful-graph-aligned-multi-turn-benchmarks.md) - Transitions multi-turn benchmarks to preserve original turns, utilize in-memory virtual filesystem observers, and execute multiset graph matching.
- [ADR-0015: Pristal Architecture Alignment](../adr/0015-pristal-architecture-alignment.md) - Aligns database, compilation, memory cache, and execution pruning engines with Pristal v2 standards.
- [ADR-0016: Pluggable Inference Backend](../adr/0016-pluggable-inference-backend.md) - Splits LocalModelManager into a pluggable Inference Backend interface and Sidecar Manager, enabling tzro to target LMStudio, Ollama, or harness-provided models.

## Ingested Sources

_Immutable third-party references, notes, and raw inputs._

- [LLM Wiki Reference (Karpathy)](../agents/wiki.md) - Design guidelines and templates for local wiki maintenance.
