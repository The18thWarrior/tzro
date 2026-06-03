# tzro Context

A durable, local-first agentic execution engine designed to coordinate complex multi-system automations securely on resource-constrained hardware.

## Language

**Intent**:
The classified objective type of a natural language request, identifying if it requires standard messaging, scheduled execution, or long-term coordination.
_Avoid_: Prompt type, raw user command

**Task**:
A compiled sequence of execution steps and dependency edges representing a single multi-step operational objective.
_Avoid_: Process, operation, batch job

**Workflow**:
A persistent orchestrator that schedules, triggers, and coordinates multiple dependent **Tasks** over days or weeks to achieve high-level business goals.
_Avoid_: Pipeline, campaign, automation track

**Complexity Tier**:
The execution strategy rating (**T0 Direct**, **T1 Planned**, **T2 Supervised**) assigned to a prompt to determine planning and oversight resources.
_Avoid_: Performance score, cost group

**Abstract Graph**:
A non-executed JSON schema blueprint generated during planning that maps step nodes and sequential dependencies.
_Avoid_: Execution sequence, flowchart JSON

**Kahn Compiler**:
The compiler engine that translates simplified strategic Abstract Graphs into fine-grained execution nodes (GBNF-bridge, deterministic, and synthesis) and runs Kahn's topological sort algorithm to organize them into parallel, cycle-free layers.
_Avoid_: Graph builder, sort pipeline

**Hybrid Branch Evaluator**:
A two-tier conditional execution engine that resolves branch skip decisions first via fast deterministic JSONPath comparisons and falls back to semantic Local Model inference if comparison fails, preventing incorrect path skipping.
_Avoid_: Simple compiler, condition parser, LLM evaluator

**Local Model**:
The default-path local LLM workhorse handling all structured work: intent classification, tool call construction, step execution, conversation compaction, and error recovery. Cloud is only invoked when the **Local Model** lacks the knowledge or latency profile required. Backed by a pluggable **Inference Backend**.
_Avoid_: Local Step Executor (too narrow), system LLM, cloud coder

**Inference Backend**:
A pluggable provider abstraction that decouples structured LLM inference calls from the process that hosts the model. Configured at the config level via a backend type and endpoint URL. Implementations include the embedded llama-server sidecar, remote OpenAI-compatible servers (LMStudio, Ollama, vLLM), or a harness callback routing inference through an external agent framework.
_Avoid_: Model provider, LLM client, API wrapper

**Cloud Model**:
The exception-path remote LLM invoked only when the **Local Model** lacks sufficient knowledge, reasoning depth, or latency profile. Used for DAG planning, conversational responses requiring world knowledge, and **T2 Supervised** guardrail oversight.
_Avoid_: Cloud API, remote agent, fallback model

**GBNF Constraint**:
Logit-level grammar constraints forced onto local worker models to guarantee 100% syntactically valid JSON tool parameters.
_Avoid_: Output parser, regex validator

**Observer Agent**:
A non-blocking background auditor that monitors event channels, evaluates task health, and performs lifecycle deactivations.
_Avoid_: Cron manager, heartbeat daemon

**Procedural Micro-Skill**:
A highly structured Markdown SOP extracted from successful trajectories and injected to prevent zero-shot API hallucinations.
_Avoid_: Dynamic prompt context, RAG document

**Sandboxed Micro-Skill**:
A compiled WebAssembly binary containing specialized logic executed safely on-device with strict, isolated resource limits.
_Avoid_: WASM plugin, executable skill, CGO connector

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
An alternative runtime personality where the tzro engine presents its capabilities (planning, execution, memory, knowledge graph, skills) as MCP tool schemas over stdio, allowing external agent frameworks to consume tzro as a tool server. Can operate simultaneously with the **MCP Host** role — a single tzro process may both serve tools outward and consume tools inward.
_Avoid_: MCP Bridge, MCP Gateway, MCP Proxy

**Containerized MCP Host**:
An **MCP Host** that runs inside an isolated, resource-constrained container (e.g. Docker) rather than directly on the host OS, utilizing strict host environment variable declaration.
_Avoid_: Docker host, containerized tool, virtualized daemon

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
