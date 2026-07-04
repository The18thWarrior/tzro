# tzro Context

A durable, local-first agentic operating system — a portable runtime that carries everything an AI agent needs to be productive: a scheduler, persistent memory, a tool registry, a local model, a knowledge graph, a skill library, and a durable execution substrate. Activates through MCP or direct Go framework embedding.

## Language

**Intent**:
The classified objective type of a natural language request, identifying if it requires standard messaging, scheduled execution, or long-term coordination.
_Avoid_: Prompt type, raw user command

**Task**:
A compiled sequence of execution steps and dependency edges representing a single multi-step operational objective.
_Avoid_: Process, operation, batch job

**Workflow**:
A persistent, goal-oriented orchestrator that coordinates multiple dependent **Tasks** to achieve an objective. May be user-spawned (long-running business goals) or system-spawned (short-lived diagnostics triggered by a **Background Agent**). Supports both static orchestration (pre-defined task graph) and dynamic orchestration (LLM-driven, where the **Local Model** decides the next **Task** after each completion).
_Avoid_: Pipeline, campaign, automation track

**Complexity Tier**:
The execution strategy rating (**T0 Direct**, **T1 Planned**, **T2 Supervised**) assigned to a prompt to determine planning and oversight resources.
_Avoid_: Performance score, cost group

**Confidence Tier**:
A per-node pre-flight self-assessment where the **Local Model** evaluates whether it can extract the required parameters from the accumulated context and tool schema before committing to a full inference call. Returns `sufficient` (proceed locally) or `insufficient` (escalate to **Cloud Model**). Operates at execution time on individual DAG nodes, unlike **Complexity Tier** which classifies user intent at task intake.
_Avoid_: Complexity Tier (intake-level routing), Speed Floor (hardware throughput), difficulty score

**Abstract Graph**:
A non-executed JSON schema blueprint generated during planning that maps step nodes and sequential dependencies.
_Avoid_: Execution sequence, flowchart JSON

**Kahn Compiler**:
The compiler engine that translates simplified strategic Abstract Graphs into fine-grained execution nodes (semantic validator, deterministic, and synthesis) and runs Kahn's topological sort algorithm to organize them into parallel, cycle-free layers.
_Avoid_: Graph builder, sort pipeline

**Hybrid Branch Evaluator**:
A two-tier conditional execution engine that resolves branch skip decisions first via fast deterministic JSONPath comparisons and falls back to semantic Local Model inference if comparison fails, preventing incorrect path skipping.
_Avoid_: Simple compiler, condition parser, LLM evaluator

**Probe Node**:
An autonomous execution node type that runs a bounded, multi-step **Thought Chain** exploration loop (research, directory traversal, file reading) using the **Local Model**. Persists its intermediate reasoning and tool outputs to the `thought_chain` table for durability and later recall.
_Avoid_: Action Node (single-step), research agent, sub-task

**Recall Node**:
A specialized execution node injected automatically after a **Probe Node** by the **Kahn Compiler**. Offloads the responsibility of synthesis from the explorer to a synthesizer agent. Utilizes a **Map-Reduce Recall** strategy: it first maps the execution history to identify "Signal" vs "Sludge," then performs targeted extraction on the "Signal" chunks, and finally synthesizes an aligned response. This prevents the "Synthesis Cliff" and eliminates massive prefill latency caused by one-shot raw history processing.
_Avoid_: Synthesis Node (one-shot summarizer), summary step, final report, Rolling Compaction (destructive)

**Local Model**:
The default-path local LLM workhorse handling all structured work: intent classification, tool call construction, step execution, conversation compaction, and error recovery. Cloud is only invoked when the **Local Model** lacks the knowledge or latency profile required. Backed by a pluggable **Inference Backend**.
_Avoid_: Local Step Executor (too narrow), system LLM, cloud coder

**Inference Backend**:
A pluggable provider abstraction that decouples structured LLM inference calls from the process that hosts the model. Configured at the config level via a backend type and endpoint URL. Implementations include the embedded llama-server sidecar, remote OpenAI-compatible servers (LMStudio, Ollama, vLLM), or a harness callback routing inference through an external agent framework.
_Avoid_: Model provider, LLM client, API wrapper

**Strategic Planner (The Strategist)**:
The component (often the **Cloud Model**) responsible for compiling a user's intent into an **Abstract Graph**. To minimize latency, the Strategist is "code-blind"—it receives only the **Tool Inventory**, **Micro-Skills**, and a **Shallow Directory Tree** (names only, no signatures, max depth 2) as scaffolding. If the Strategist requires deeper codebase knowledge to plan surgical paths, it **must** delegate that discovery to a **Probe Node**.
_Avoid_: Planner, DAG Generator

**GBNF Constraint**:
Logit-level grammar constraints forced onto local worker models. Previously used for deep JSON schemas, now restricted to shallow structural enforcement (e.g., ensuring valid XML wrapper tags) to maximize generation speed while delegating schema coercion to the **Semantic Validator**.
_Avoid_: Output parser, regex validator

**Semantic Validator**:
A deterministic boundary seam that parses loose, high-speed XML outputs from the **Local Model** and coerces them into the strict JSON parameters required by tool schemas. Concentrates type coercion, default imputation, and fuzzy matching in one place to prevent grammar-masking bottlenecks during inference.
_Avoid_: JSON parser, output fixer

**Response Resolver**:
A transparent post-execution step within action nodes that normalizes raw tool outputs into a flattened property map, making them resolvable by downstream **DynamicBindings** references. Uses a three-tier cascade: recursive JSON key search (exact match at any depth), fuzzy key search (suffix/substring containment), and semantic matching via the **Local Model** as fallback. Each resolution carries a confidence tier (`recursive_key`, `fuzzy_key`, `semantic_fallback`) used by the **Proactive Binding Splice** to determine whether to bypass inference. The output-side counterpart to the **Semantic Validator** (input-side).
_Avoid_: Output Schema Registry, Tool Output Schema, output parser

**Proactive Binding Splice**:
A pre-inference optimization where high-confidence **Response Resolver** outputs (`recursive_key`, `fuzzy_key`) are stripped from the tool schema before the **Semantic Validator** runs inference, then spliced back into the final JSON after extraction. Prevents the **Local Model** from generating values that are already deterministically known, eliminating an entire class of parameter mismatch failures.
_Avoid_: Post-extraction override, reactive cleanup, prompt injection hint


**Agent**:
The minimal contract for any autonomous process hosted by the tzro engine. Has a name, a lifecycle (`Start`/`Stop`), and runs within the daemon process. Concrete agent types specialize the trigger mechanism and capabilities.
_Avoid_: Model, executor, tool, Probe Node (bounded DAG node, not a long-lived process)

**Background Agent**:
An **Agent** subtype that runs continuously inside the daemon on its own trigger schedule (event-driven, periodic, or both). Has access to the **Local Model** via an LLMClient interface, the TelemetryManager event stream, the memory store, and the **Durable Notification** output channel. The **Observer Agent** and **Sentinel Agent** are the first two **Background Agents**.
_Avoid_: Agent (too broad), daemon thread, cron job

**Observer Agent**:
A **Background Agent** that fires reactively on debounced telemetry events (event count threshold or inactivity window). Performs post-execution reflection — memory synthesis and knowledge graph extraction from completed task trajectories.
_Avoid_: Sentinel Agent (proactive), cron manager, heartbeat daemon

**Sentinel Agent**:
A **Background Agent** that fires proactively on a periodic heartbeat timer and ingested activity reports. Evaluates ambient system state, correlates user activity patterns against memory and the knowledge graph, and produces structured alerts via **Durable Notifications** communicated upstream to the harness through MCP resource change notifications.
_Avoid_: Observer Agent (reactive), cron job, monitoring service

**Procedural Micro-Skill**:
A highly structured Markdown SOP injected into the **Local Model**'s context pipeline to prevent zero-shot API hallucinations. May be runtime-extracted from successful trajectories or developer-authored and shipped with an **Agent App**.
_Avoid_: Dynamic prompt context, RAG document

**Corrective Micro-Skill**:
An anti-pattern SOP auto-extracted from the diff between a failed **Local Model** extraction and a successful **Cloud Model** re-execution of the same node. Injected into the **Local Model**'s context pipeline via the existing skill index to teach the Tactician to self-correct on specific failure patterns (e.g., quoting conventions, ID format expectations) without weight updates. Complements **Procedural Micro-Skill** (success-derived) with failure-derived corrections.
_Avoid_: Procedural Micro-Skill (success-derived), historical success rate, retraining

**Sandboxed Micro-Skill**:
A compiled WebAssembly binary containing specialized logic executed safely on-device with strict, isolated resource limits.
_Avoid_: WASM plugin, executable skill, CGO connector

**Agent App**:
A self-contained, installable capability extension distributed as a `.tzroapp` archive. Bundles one or more tools (**Sandboxed Micro-Skill**, **MCP Host** sidecar, or both), optional pre-authored **Procedural Micro-Skills**, optional SQLite migrations, and a capability manifest into a single distributable unit. Identified by a locally-unique short slug. Tools are namespaced as `{appId}_{toolName}`. Must contain at least one tool — toolless packages are not Agent Apps. Composable — multiple Agent Apps coexist additively on a single tzro instance.
_Avoid_: Plugin, extension, module, add-on, flavor

**Package Manager**:
A daemon-resident service and CLI subcommand (`install`, `uninstall`, `list`, `purge`) that manages the **Agent App** lifecycle. On install: extracts the `.tzroapp` archive, validates the manifest, runs SQLite migrations (tracked via `_tzro_migrations`), registers tools incrementally, and triggers the **Attention Queue** consent flow for capabilities mapped to **Proactivity Ladder** tiers above L1. On uninstall: soft-disables the app (deregisters tools, stops **MCP Host** daemons) but preserves data. Explicit `purge` destroys data and drops tables.
_Avoid_: App Store, registry, installer wizard

**Edge Thought**:
A compact reasoning state generated on a DAG edge traversal by the **Local Model**, summarizing what execution has learned so far and how confident it is that the task goal can be achieved. Generated when the executor traverses an edge whose target node has a non-zero **Activation Threshold**. Persisted to SQLite for durability. Serves as the primary reasoning context for downstream nodes, with raw upstream data included on-demand for structured parameter extraction.
_Avoid_: Short-term memory, session context, accumulated context (raw data, not reasoning)

**Activation Threshold**:
A per-node sufficiency gate (0.0–1.0) that determines whether an **Edge Thought**'s goal confidence warrants dynamic graph mutation. When the incoming Edge Thought's confidence falls below the target node's Activation Threshold, the **Local Model** spawns a new node to perform additional work before the target executes. A threshold of 0.0 disables Edge Thought generation and spawn evaluation entirely. Set by the **Cloud Model** at planning time or defaulted by the **Kahn Compiler** based on node type.
_Avoid_: New Thought Threshold, firing threshold, trigger condition

**Probe Node** _(deprecated)_:
Superseded by **Edge Thought** and **Activation Threshold**, which generalize the Probe's reactive behavior to all DAG nodes. Existing DAGs emitting `type: "probe"` are silently treated as action nodes with a high Activation Threshold and allocated mutation budget.
_Avoid_: Use Edge Thought and Activation Threshold instead

**Thought Chain** _(deprecated)_:
Superseded by **Edge Thought**. The Thought Chain's internal step loop is replaced by dynamic node spawning via the Activation Threshold, where each tool call becomes a checkpointed DAG node rather than a hidden internal step.
_Avoid_: Use Edge Thought instead

**Compaction Pipeline**:
A 5-layer compression process that flattens and translates verbose API outputs before injection to prevent model memory overload.
_Avoid_: Text parser, JSON clean filter

**Two-Tier Cache GC**:
An automated resource recovery mechanism that clears idle context slots immediately upon Task completion (Tier 1) and gracefully recycles the local inference sidecar process if RSS memory usage exceeds limits during idle windows (Tier 2).
_Avoid_: Memory cleaner, server killer, process resetter

**Session History Compaction**:
The selective pruning, truncation, or summarization of conversational prompt history within interactive multi-turn sessions to prevent local slot thrashing and attention bias.
_Avoid_: Context compaction, prompt clipping, prompt truncation

**Virtual Filesystem State**:
A simulated POSIX directory structure and active path context maintained in-memory by the offline benchmark runner to preserve stateful environmental continuity for the executing agent across conversation turns.
_Avoid_: Mock folder, hardcoded directory, local sandbox path

**Disk-Backed JQ Cache**:
An on-disk caching layer storing large compacted payloads and exposing a targeted JQ exploration guide interface to the executor.
_Avoid_: Local temp file, tool database

**Relational Knowledge Graph**:
A local relational node-edge memory database representing enterprise system entity links, traversed via Neighborhood Multi-Hop search.
_Avoid_: Vector space, semantic memory

**Hybrid Vector Search**:
A multi-tier retrieval process that runs keyword query filtering first to generate a candidate node pool, followed by local ONNX cosine similarity matching to rank starting nodes for Graph-RAG neighborhood traversal.
_Avoid_: Flat vector index, C-vec extension, direct embedding search

**MCP Host**:
The inbound tool integration layer that spawns external integration child processes over standard I/O (stdio) to supply tools to the execution engine. The engine is tool-source agnostic — it dispatches to tools regardless of whether they originate from built-in registrations, **MCP Host** child processes, or tools forwarded by an external harness via **MCP Server Mode**.
_Avoid_: Custom connector, API gateway, native integration

**MCP Server Mode**:
An alternative runtime personality where the tzro engine presents its capabilities (planning, execution, memory, knowledge graph, skills) as MCP tool schemas and dynamic resources (such as `tzro://tasks/{taskId}/output` and `tzro://tasks/{taskId}/nodes/{nodeId}/output`) over stdio, allowing external agent frameworks to consume tzro as a tool server. Can operate simultaneously with the **MCP Host** role — a single tzro process may both serve tools outward and consume tools inward.
_Avoid_: MCP Bridge, MCP Gateway, MCP Proxy

**Containerized MCP Host**:
An **MCP Host** that runs inside an isolated, resource-constrained container (e.g. Docker) rather than directly on the host OS, utilizing strict host environment variable declaration.
_Avoid_: Docker host, containerized tool, virtualized daemon

**Native Plugin Mode**:
An integration architecture where the tzro engine runs in-process as a module or plugin within an external agent harness (such as Hermes Agent or Google Antigravity SDK). In this mode, the local step executor can programmatically dispatch primitive tools directly to the host process in-process, bypassing the parent LLM's request-response loops and eliminating round-trip cloud overhead.
_Avoid_: In-process worker, direct connector, host module

**Delegated Secret**:
A sensitive runtime credential (e.g. API key, access token) that is referenced dynamically via an environment variable prefix (such as `$`) in configuration JSONs and resolved on-demand from the host environment, keeping configurations clean and credential-free.
_Avoid_: Keyring credential, keychain secret, encrypted token

**Durable Notification**:
A structured alert record persisted in SQLite and dispatched over the StreamBus, allowing background Tasks, Workflows, or the Observer Agent to communicate asynchronous lifecycle states, warnings, or action requests to the user across restarts and page refreshes.
_Avoid_: Toast, alert popup, chat message

**Dialect Adapter**:
An abstraction seam that decouples SQL syntax, connection drivers, table schema validation, and dialect-specific UPSERT templates, allowing tzro's relational memory and task checkpointing databases to run seamlessly against SQLite, PostgreSQL, MySQL, or MSSQL.
_Avoid_: Raw SQL driver, custom DB connector

**Database Manager**:
The unified relational storage engine orchestrating persistence, schema migration, vector memory retrieval, and transaction checkpointing across both local and dynamic remote databases.
_Avoid_: SqliteDatabase (too narrow), JSON DB, storage client

**Delegation Mode**:
The cost-optimization strategy controlling how aggressively the cloud/frontier model offloads sub-tasks to the **Local Model** via direct completion and classification MCP tools. Three tiers: **Conservative** (DAG workflows only), **Balanced** (classification + extraction + formatting), **Aggressive** (everything except frontier reasoning). The mode tunes tool descriptions at registration time to steer the cloud model's delegation behavior without protocol changes.
_Avoid_: Cost mode, cheapness level, offload policy

**Offload Policy**:
The decision framework an external agent consuming tzro via **MCP Server Mode** applies to determine which phases of work to submit as **Tasks** versus execute directly in its own context window. Phases involving tool-heavy data collection, parallelizable operations, or large output consumption are offloaded; phases requiring frontier reasoning, code generation, or interactive user dialogue are retained.
_Avoid_: Delegation Mode (internal cloud→local routing), routing policy, execution strategy

**Privacy Level**:
The configuration governing whether tasks can utilize remote cloud models for planning or execution. Three tiers: **strict-local** (blocks all cloud interactions; planning and execution fail if the local model is insufficient), **hybrid** (default, planning and execution default to local but escalate to cloud when local capability is insufficient), and **cloud-preferred** (planning and complex execution prioritize cloud).
_Avoid_: LocalOnlyMode, privacy guard, isolation level

**Attention Scheduler**:
The background daemon coordinator that schedules, executes, and filters low-priority event-driven background daemons under preemption, budget, and safety constraints.
_Avoid_: Proactivity Scheduler, background loop agent

**Proactivity Ladder**:
The five-tier safety and visibility classification (L0 Observe, L1 Prepare, L2 Suggest, L3 Reversible Action, L4 External Side Effect) governing the permissions and approval gates of background proposed actions.
_Avoid_: Action tier, proactivity score, complexity tier

**Proposed Action**:
A structured proposal returned by a background daemon to the Attention Scheduler representing a recommended mutation, observation, or user alert, containing metadata for policy checks.
_Avoid_: Execution command, task, step

**Attention Queue**:
The user-visible interface (backed by persistent notifications) holding pending L2 suggestions and L3/L4 actions awaiting explicit user approval.
_Avoid_: Alert queue, message panel

**Tool Proactivity Level**:
A **Proactivity Ladder** tier annotation declared per tool at registration time, enabling the execution harness to deterministically gate tool dispatch against a **Workflow**'s approved ceiling. Built-in tools are hardcoded per tool. **MCP Host** tools default to L3 (unknown side effects). Harness-forwarded tools default to L1 (explicitly trusted by the external framework). Overridable in tool or server configuration.
_Avoid_: Tool permission, tool safety rating, capability flag

---

## Example Dialogue

> **Dev**: "I need to connect a custom HubSpot lead-scoring workflow to our messaging system. Should I write a new Go package under `services/go-api`?"
>
> **Domain Expert**: "No. We never write custom integrations. Instead, register the HubSpot integration as a new **MCP Host** using their standard stdio-based tool server. The **Kahn Compiler** will automatically map their tools into the compiled **Abstract Graph** when compiling the **Task**."
>
> **Dev**: "Got it. When the **Local Model** makes the API calls, how do we make sure it doesn't output invalid SOQL/JQL parameters?"
>
> **Domain Expert**: "We inject a targeted **Procedural Micro-Skill** matching that trigger, and force the output through a **GBNF Constraint** matching HubSpot's schemas. If the API returns a massive contact payload, the **Compaction Pipeline** will compress it or envelope it in the **Disk-Backed JQ Cache**."
>
> **Dev**: "When a user asks a general knowledge question — like 'what time zone does Salesforce use?' — does the **Local Model** answer that too?"
>
> **Domain Expert**: "No. The **Local Model** classifies the **Intent** and **Complexity Tier** first. If it's a simple conversational question — **T0 Direct** — the **Cloud Model** generates the reply because it has the world knowledge and lower latency. But if classification reveals the user is actually requesting multi-tool work despite the conversational tone, the **Complexity Tier** promotes it to a **Task** and the **Local Model** executes the compiled nodes."
