# tzro Domain Language

Canonical terminology extracted from [CONTEXT.md](file:///Users/jp/Desktop/Repos/tzro/CONTEXT.md). Always use these terms — avoid the listed anti-patterns.

## Core Concepts

| Term | Definition | Avoid |
|------|-----------|-------|
| **Intent** | The classified objective type of a natural language request | Prompt type, raw user command |
| **Task** | A compiled sequence of execution steps and dependency edges | Process, operation, batch job |
| **Workflow** | A persistent orchestrator coordinating multiple dependent Tasks over days/weeks | Pipeline, campaign, automation track |
| **Complexity Tier** | Execution strategy rating: **T0 Direct**, **T1 Planned**, **T2 Supervised** | Performance score, cost group |

## Compilation & Execution

| Term | Definition | Avoid |
|------|-----------|-------|
| **Abstract Graph** | Non-executed JSON blueprint mapping step nodes and dependencies | Execution sequence, flowchart JSON |
| **Kahn Compiler** | Translates Abstract Graphs into fine-grained execution nodes and runs topological sort into parallel layers | Graph builder, sort pipeline |
| **Hybrid Branch Evaluator** | Two-tier conditional engine: fast JSONPath comparisons first, semantic fallback if needed | Simple compiler, condition parser |
| **GBNF Constraint** | Logit-level grammar constraints guaranteeing valid JSON tool parameters | Output parser, regex validator |

## Inference

| Term | Definition | Avoid |
|------|-----------|-------|
| **Local Model** | Default-path local LLM handling intent classification, tool calls, step execution, compaction, and error recovery | Local Step Executor, system LLM |
| **Inference Backend** | Pluggable provider abstraction (embedded llama-server, Ollama, LM Studio, vLLM, or harness callback) | Model provider, LLM client |
| **Cloud Model** | Exception-path remote LLM for planning, world knowledge, and T2 guardrail oversight | Cloud API, remote agent, fallback model |

## Memory & Knowledge

| Term | Definition | Avoid |
|------|-----------|-------|
| **Relational Knowledge Graph** | Local node-edge memory database traversed via Neighborhood Multi-Hop search | Vector space, semantic memory |
| **Hybrid Vector Search** | Keyword filtering → ONNX cosine similarity → Graph-RAG neighborhood traversal | Flat vector index, direct embedding search |
| **Procedural Micro-Skill** | Structured Markdown SOP extracted from successful trajectories | Dynamic prompt context, RAG document |
| **Sandboxed Micro-Skill** | Compiled WASM binary with isolated resource limits | WASM plugin, executable skill |

## Infrastructure

| Term | Definition | Avoid |
|------|-----------|-------|
| **MCP Host** | Inbound tool integration layer spawning external child processes over stdio | Custom connector, API gateway |
| **MCP Server Mode** | Runtime personality presenting tzro capabilities as MCP tool schemas over stdio | MCP Bridge, MCP Gateway |
| **Native Plugin Mode** | In-process module/plugin within an external agent harness (Hermes, Antigravity SDK) | In-process worker, direct connector |
| **Compaction Pipeline** | 5-layer compression flattening verbose API outputs before injection | Text parser, JSON clean filter |
| **Observer Agent** | Non-blocking background auditor monitoring event channels and task health | Cron manager, heartbeat daemon |
| **Delegated Secret** | Runtime credential referenced via `$` prefix, resolved from host environment | Keyring credential, encrypted token |
| **Database Manager** | Unified relational storage engine for persistence, migration, and vector retrieval | SqliteDatabase, JSON DB |
