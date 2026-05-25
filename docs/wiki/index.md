# Local Wiki Index

Welcome to the persistent repository knowledge base for the `tzro` project. 

- **Chronological Log**: [Log](log.md)

---

## Features & PRDs
*Map of system features, product requirements, and specs.*
- [Durable DAG Benchmarking Suite](features/benchmarking-suite.md) - Evaluate model planning and parameter execution against BFCL and ComplexFuncBench datasets. (Sources: 2 | Last Updated: 2026-05-24)

## Bugs & Post-Mortems
*Analyses of critical bugs, diagnostic loops, and prevention measures.*
- [Missing Embedding SQLite Column & Cloud Schema Format HTTP 400](bugs/missing-embedding-column-and-cloud-schema-400.md) - Resolve SQL errors for missing DB vector columns and HTTP 400 errors for non-standard cloud response format payloads. (Verified: 2026-05-24)
- [Local Sidecar Inactive / Benchmark API Key Loading Bug](bugs/local-sidecar-inactive-benchmark-fallback-bug.md) - Fix configuration loading in CLI process for offline benchmarks and optimize test suite datasets to run unit tests in seconds instead of minutes. (Verified: 2026-05-25)


## Architecture & Concepts
*Glossary terms, data models, ADR summaries, and architectural diagrams.*
- [Repository Domain Context](../../CONTEXT.md) - Durable local-first agentic execution language glossary.
- [Technical Design](../technical-design.md) - Overview of the tzro durable local execution system.
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


## Ingested Sources
*Immutable third-party references, notes, and raw inputs.*
- [LLM Wiki Reference (Karpathy)](../agents/wiki.md) - Design guidelines and templates for local wiki maintenance.
