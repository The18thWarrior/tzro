# Local Wiki Index

Welcome to the persistent repository knowledge base for the `tzro` project.

- **Chronological Log**: [Log](log.md)

---

## Features & PRDs

_Map of system features, product requirements, and specs._

- [Durable DAG Benchmarking Suite](features/benchmarking-suite.md) - Evaluate model planning and parameter execution against BFCL and ComplexFuncBench datasets. (Sources: 2 | Last Updated: 2026-05-24)
- [Code Quality & Architectural Refactoring](features/code-quality-refactors.md) - Decompose monolithic runner.go and memory.go files into highly cohesive sub-modules and clean up hardcoded edge cases. (Sources: 1 | Last Updated: 2026-05-28)
- [Synchronous DAG Execution Hooks](features/dag-execution-hooks.md) - Middleware layer for synchronous task intercepting, validation, output mutation, and durable pausing. (Sources: 1 | Last Updated: 2026-05-31)
- [Dynamic Local Planning and Routing](features/local-planning-routing.md) - Enable hybrid planning using local and cloud routers guided by privacy and complexity policies. (Sources: 1 | Last Updated: 2026-06-08)
- [Reactive Agent Daemons](features/reactive-daemons.md) - Expand background scheduler daemons to run autonomous local LLM tool execution loops. (Sources: 1 | Last Updated: 2026-06-08)
- [Agent Inter-Process Communication (IPC)](features/agent-ipc.md) - Bidirectional agent messaging bus enabling sub-task delegation and resource context yielding. (Sources: 1 | Last Updated: 2026-06-08)
- [Agent App Packaging Standard](features/agent-app-packaging.md) - Unified .tzroapp zip package format containing prompts, WASM/MCP tools, SQL migrations, and permission requests. (Sources: 1 | Last Updated: 2026-06-08)
- [MCP Singleton Guard](features/mcp-singleton-guard.md) - PID lockfile preventing duplicate tzro-mcp instances when multiple IDE language servers spawn MCP children. (Sources: 1 | Last Updated: 2026-06-09)
- [Response Resolver](features/response-resolver.md) - Three-tier output resolution cascade (recursive key search + KV-line + semantic fallback) for DynamicBindings. Output-side counterpart to the Semantic Validator. (Sources: 1 | Last Updated: 2026-06-10)
- [Dual-Audience Hardening](features/dual-audience-hardening.md) - Secure local-first loopback, MCP-to-daemon delegation proxy, and complete Package Manager CLI/MCP integration. (Sources: 1 | Last Updated: 2026-06-15)
- [Data Profiler & Cache Bridge Node](../working-specs/data-profiler-and-cache-bridge-node.md) - Content-aware tabular file profiling in read_file with deterministic Cache Bridge Node injection for CSV, TSV, Excel, and large JSON. (Sources: 1 | Last Updated: 2026-07-13)

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
- [Disk-Backed Query Cache Architecture](architecture/disk-backed-jq-cache.md) - Deep subsystem for context compaction, page-sliced pagination, and SQL query execution against ephemeral materialized tables.
- [SQL Query Language for Cached Data — Spec](../working-specs/sql-query-language-for-cached-data.md) - Implementation spec for replacing jq with SQL. Covers ephemeral materialized tables, 4-layer safety, tool changes, prompt updates, and migration.
- [Task-to-Workflow Promotion Engine](architecture/task-workflow-promotion.md) - Deep subsystem that dynamically elevates Single Task DAGs to persistent Multi-Task Workflows.
- [Tool Source Paradigms](architecture/tool-source-paradigms.md) - Analysis of the four tool sources (Builtin, WASM, OpenAPI, MCP), their overlap, and why each exists.
- [Agentic Harness Integration](architecture/agentic-harness-integration.md) - Analysis of MCP Server, Native Plugin, and Sidecar paradigms for orchestrating client-side execution steps.
- [Edge-Cloud Co-Orchestration Beyond DAGs](architecture/edge-cloud-co-orchestration.md) - Multi-agent blackboard systems, speculative decoding, GAPG routing, and distributed state reuse.
- [SubagentChannel v3](architecture/subagent-channel-v3.md) - Concurrency safety, structured payloads, error backpressure, SSE adapter, plugin adapter, and dashboard streaming endpoint.

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
- [ADR-0017: MCP Resource Subscriptions](../adr/0017-mcp-resource-subscriptions.md) - Exposes hierarchical task and node outputs over stdio JSON-RPC via dynamically sourced pub/sub event subscriptions.
- [ADR-0018: Native Plugin Local Inference Isolation](../adr/0018-native-plugin-local-inference-isolation.md) - Mandates local worker execution for native plugins unless an existing local API (Ollama, LM Studio) is provided by the user.
- [ADR-0019: Probe Node and Thought Chain Execution](../adr/0019-probe-node-and-thought-chain-execution.md) - Goal-directed DAG nodes with bounded internal Thought Chains for reactive exploration.
- [ADR-0020: Confidence Tier and Corrective Micro-Skills](../adr/0020-confidence-tier-and-corrective-micro-skills.md) - Per-node pre-flight self-assessment with failure-derived anti-pattern SOP extraction.
- [ADR-0021: Segmented Multi-Turn Prompt for KV Cache Sharing](../adr/0021-segmented-multi-turn-prompt-kv-cache-sharing.md) - 4-message prompt structure and Messages interface for cross-node KV prefix reuse.
- [ADR-0022: Background Agent Abstraction and Observer Refactor](../adr/0022-background-agent-abstraction-and-observer-refactor.md) - Agent interface, BackgroundAgent base struct, and Observer refactored to embed it.
- [ADR-0023: Sentinel Agent and Proactive Activity Channel](../adr/0023-sentinel-agent-and-proactive-activity-channel.md) - Proactive intelligence agent with retrieval-grounded synthesis, workspace scanning, activity reports, and dual-path alert delivery.
- [ADR-0024: Edge Thought and Activation Threshold](../adr/0024-edge-thought-and-activation-threshold.md) - Neural edge traversal with dynamic graph mutation. Deprecates Probe Node and Thought Chain.
- [ADR-0025: Attention and Proactivity Scheduler](../adr/0025-attention-and-proactivity-scheduler.md) - Background daemon coordinator with proactivity ladder, foreground preemption, resource budgets, and approval-gated actions.
- [ADR-0026: No Agent IPC Bus](../adr/0026-no-agent-ipc-bus.md) - Deliberate rejection of ReAct-style inter-agent messaging; DAG edges, MCP Host, and shared state cover all coordination needs.
- [ADR-0027: Dynamic Workflow Orchestration Over Reactive Daemons](../adr/0027-dynamic-workflow-orchestration-over-reactive-daemons.md) - Collapsed "Reactive Agent Daemons" into LLM-driven extension of existing Workflow orchestrator. No new abstraction.
- [ADR-0028: Semantic Validator Seam](../adr/0028-semantic-validator-seam.md) - Deprecated deep GBNF constraints in favor of XML generation with deterministic Semantic Validator coercion. Supersedes ADR-0002.
- [ADR-0029: Response Resolver and Semantic Binding Fallback](../adr/0029-response-resolver-and-semantic-binding-fallback.md) - Two-tier output resolution (recursive key search + Local Model semantic fallback) for DynamicBindings. Output-side counterpart to the Semantic Validator.
- [ADR-0030: Proactive Binding Splice for Deterministic Resolutions](../adr/0030-proactive-binding-splice-for-deterministic-resolutions.md) - Pre-inference optimization stripping high-confidence resolved bindings from schema before Semantic Validator inference.
- [ADR-0031: Agent App Packaging and Package Manager](../adr/0031-agent-app-packaging-and-package-manager.md) - Composable `.tzroapp` packaging format with app-scoped namespacing, incremental MCP registration, developer-trusted capability declarations, and soft-disable uninstall lifecycle.
- [ADR-0032: AgenticOS JumpDrive Website Positioning](../adr/0032-agenticos-jumpdrive-website-positioning.md) - Website positioning and messaging strategy.
- [ADR-0033: Daemon Re-exec Restart via MCP](../adr/0033-daemon-re-exec-restart-via-mcp.md) - Graceful daemon restart via MCP tool with re-exec pattern.
- [ADR-0034: Three-Bucket Metric Separation](../adr/0034-three-bucket-metric-separation.md) - Separates DAG structural, pipeline compaction, and local offloading savings into independently measurable benchmark buckets.
- [ADR-0035: Complete Edge Thought Migration and Codegen Quality Pipeline](../adr/0035-complete-edge-thought-migration-and-codegen-quality-pipeline.md) - Completes ADR-0024 production wiring and builds codegen validation, output constraints, and complexity routing on top.
- [ADR-0036: Edge Thought Driven Codegen Repair](../adr/0036-edge-thought-driven-codegen-repair.md) - Edge Thought evaluation for codegen quality repair via dynamic node spawning.
- [ADR-0037: Recall Node for Discovery-Synthesis Alignment](../adr/0037-recall-node-for-discovery-synthesis-alignment.md) - Decouples synthesis from exploration by injecting Recall nodes downstream of Probes with Map-Reduce recall.
- [ADR-0042: Map-Reduce Recall and Shallow Planning](../adr/0042-map-reduce-recall-and-shallow-planning.md) - Multi-pass Recall synthesis and code-blind Strategist to reduce wall clock time and token waste.
- [ADR-0043: Two-Tier Context Budget](../adr/0043-two-tier-context-budget.md) - Probe step generation cap (max_tokens via context key) and per-node accumulated context truncation to prevent local model speed collapse from oversized prompts.
- [ADR-0044: Synthesis-Aware Context Assembly and Tiered Budgets](../adr/0044-synthesis-aware-context-assembly-and-tiered-budgets.md) - Splits context assembly into synthesis-path (untruncated validators, no ceiling) and mid-DAG-path (tiered allocation, dynamic ceiling). Supersedes ADR-0043 Mechanism B.
- [ADR-0049: Data Profiler and Cache Bridge Node](../adr/0049-data-profiler-and-cache-bridge-node.md) - Content-aware tabular file profiling in read_file, path-referenced caching, and two-layer deterministic Cache Bridge Node injection. Narrows ADR-0019 bypass to non-tabular files.
- [ADR-0051: SQL Query Language for Cached Data](../adr/0051-sql-query-language-for-cached-data.md) - Replaces jq with SQL as the query language for cached tabular data. Ephemeral materialized tables in a separate query database with 4-layer safety sandboxing.
- [ADR-0052: CompactPreserve Semantics for Analyze Nodes](../adr/0052-compact-preserve-semantics-for-analyze-nodes.md) - Defines CompactPreserve to preserve tool output data verbatim while still LLM-compressing reasoning text. Fixes data loss in analyze node synthesis.
- [ADR-0053: Analytical Evidence for Data Analysis](../adr/0053-analytical-evidence-for-data-analysis.md) - Structured raw data from successful sql_cached_data calls materialized alongside terminal_synthesis. Primary ground-truth output for data analysis tasks.
- [ADR-0054: Self-Contained Task Short-Circuit and Task Lifecycle Table](../adr/0054-self-contained-task-short-circuit-and-task-lifecycle-table.md) - Caller-hint `selfContained` flag bypasses planner for tool-less prompts. New `tasks` table tracks task lifecycle and surfaces planning failures. Tactical bridge until ADR-0048 template selection is implemented.
- [ADR-0055: Structured Execution Envelope](../adr/0055-structured-execution-envelope.md) - Deterministic JSON Execution Envelope assembled by the executor at task completion, wrapping synthesis text with structured metadata (tools, files, node counts, duration). Persisted on new `StructuredOutput` field, hoisted to `result` key in all MCP surfaces.

## Ingested Sources

_Immutable third-party references, notes, and raw inputs._

- [LLM Wiki Reference (Karpathy)](../agents/wiki.md) - Design guidelines and templates for local wiki maintenance.
- [Edge-Cloud LLM Task Offloading Research](sources/edge-cloud-task-offloading.md) - Bleeding-edge architectures for edge-cloud LLM task offloading beyond Directed Acyclic Graphs.
