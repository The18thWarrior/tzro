# tzro — The Agentic OS in a Thumbdrive

![tzro App Icon](static/icon.png)

> **tzro** is a portable, local-first agentic operating system that carries everything an AI agent needs to be productive: a durable scheduler, persistent memory, a knowledge graph, a tool registry, a local model, a skill library, a package manager, background daemons, and a permission system — all in a single `tzro.db` you can carry between machines. Plug it into any agent host over MCP, or embed it directly into Go applications.

---

## 🧠 Why an Operating System, Not a Framework

The market is saturated with agent frameworks (LangChain, CrewAI, AutoGen, Semantic Kernel). Calling tzro a "framework" invites commodity comparisons. But tzro is not a library you import — it's a **runtime you plug your agent into**.

Like a classical operating system, tzro provides:

| OS Primitive | tzro Equivalent |
|:---|:---|
| **Kernel & Scheduler** | Kahn Compiler + Event-Driven Ready Queue |
| **Process Model** | Durable Tasks & Workflows (checkpointed to SQLite) |
| **Filesystem** | Relational Knowledge Graph + Hybrid Vector Memory |
| **Device Drivers** | MCP Host Gateway (stdio-based tool servers) |
| **Daemon Subsystem** | Observer Agent, Sentinel Agent, Attention Scheduler |
| **Package Manager** | `.tzroapp` archives with manifest, migrations, and tools |
| **Permission System** | Proactivity Ladder (L0–L4) with Attention Queue approval gates |
| **Syscall Interface** | 38 MCP tools exposing every OS capability over stdio |

The "Thumbdrive" metaphor captures three properties simultaneously: **instant activation** (one MCP config block), **completeness** (full OS inside), and **portability** (carry your `tzro.db` between machines).

---

## 📐 The Strategy-vs-Tactics Paradigm

Traditional agent loops suffer from brittle, high-latency, and expensive infinite-looping behaviors when forced to plan and execute in the same large context window. `tzro` resolves this by surgically separating cognitive loads into a rigid **Strategy vs Tactics** execution split.

```
                              USER GOAL PROMPT
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │          Intent Classifier               │ (T0 / T1 / T2 Routing)
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼ [Task / Workflow]
                ┌──────────────────────────────────────────┐
                │   Cloud Planner (The Strategist)         │ [Invoked ONCE]
                │   - Frontier Cloud Model (e.g. Gemini)   │ - Builds Abstract Graph JSON
                │   - Sets Activation Thresholds per node  │ - Allocates Mutation Budget
                └────────────────────┬─────────────────────┘
                                     │
                                     ▼
                ┌──────────────────────────────────────────┐
                │      Go Graph Compiler & Kahn Sorter     │ [Deterministic]
                │   - Performs cycle & edge validation     │ - Event-driven ready queue
                │   - Injects Semantic Validator nodes     │ - Incremental re-sort
                │   - Proactive Binding Splice             │ - Defaults thresholds by type
                └────────────────────┬─────────────────────┘
                                     │
                     ┌───────────────┴───────────────┐
                     ▼                               ▼
              ┌─────────────┐                 ┌─────────────┐
              │   Node A    │                 │   Node B    │
              └──────┬──────┘                 └──────┬──────┘
                     │ Edge Traversal                │
                     ▼                               ▼
              ┌─────────────────────────────────────────────┐
              │           Edge Thought Generator            │
              │  - Local Model evaluates goal confidence    │
              │  - confidence ≥ threshold → continue        │
              │  - confidence < threshold → spawn new node  │
              │  - goalAchieved → halt downstream           │
              └──────────────────────┬──────────────────────┘
                                     │
                                     ▼
  ┌────────────────────────────────────────────────────────────────────────┐
  │       Local Step Executor (The Tactician - Pluggable Backend)          │
  │  - Pluggable Inference Backend (llama-server, LMStudio, Ollama, vLLM)  │
  │  - XML generation + Semantic Validator coercion to JSON schemas        │
  │  - Dispatches local static tools & dynamic stdio-based MCP servers     │
  │  - Applies 5-layer context compaction or SQLite Disk-Backed JQ cache   │
  └────────────────────────────────────────────────────────────────────────┘
```

### 1. The Strategist (Cloud Planner)

- **Underlying Model:** High-capacity frontier cloud LLM (e.g., Gemini 3.5 Flash or GPT-4o-mini).
- **Execution Count:** Invoked exactly **once** at task startup.
- **Responsibilities:**
  - **Intent & Complexity Classification:** Analyzes the natural language goal prompt to classify its **Intent** and determine its **Complexity Tier** (T0 Direct, T1 Planned, T2 Supervised) to select the correct execution resources.
  - **SOP & Tool Discovery:** Ingests dynamic standard operating procedures (SOPs) from the synthesized skill index via **Hybrid Vector Search** (combining SQLite FTS5 keyword indexing and local ONNX cosine similarity ranking) to find relevant **Procedural Micro-Skills**.
  - **Abstract Graph Generation:** Compiles a cycle-free JSON schema blueprint (**Abstract Graph**) defining dependency edges, action nodes, and allowed tool white-lists for downstream execution.
  - **Variable Binding Declaration:** Declares double-braced variable mappings (`{{nodes.node_id.output.property}}`) to map variables forward through multi-step dependencies.
  - **Activation Threshold Assignment:** Sets per-node **Activation Thresholds** (0.0–1.0) to control dynamic graph expansion at runtime. Exploration-oriented nodes receive high thresholds (0.7–0.9), while deterministic nodes default to 0.0.
  - **Mutation Budget Allocation:** Declares a per-task **Mutation Budget** capping total runtime node spawns to prevent runaway expansion.
- **Token Efficiency:** Offloads physical tool executions and heavy data outputs entirely to the local Tactician. The Strategist only reviews procedural index metadata rather than raw data payloads, achieving an **80% prompt reduction** and avoiding cloud timeouts or context-window slot thrashing.

### 2. The Compiler & Executor (Go Systems Core)

- **Underlying Core:** Deterministic Go runtime inside `internal/compiler/` and `internal/executor/`.
- **Responsibilities:**
  - **Kahn Topological Sorting:** Detects graph cycles and topologically sorts nodes into concurrent groups utilizing Kahn's algorithm. Uses an **event-driven ready queue** (ADR-0024) where nodes fire as soon as their dependencies are satisfied.
  - **Incremental Re-Sort:** After runtime graph mutations (node spawns via Activation Thresholds), only pending/new nodes are re-sorted. Completed nodes are frozen, enabling efficient dynamic expansion.
  - **Deterministic SCT Graph Expansion:** Dynamically compiles the coarse strategic **Abstract Graph** generated by the Cloud Planner into a fine-grained Strategist-Compiler-Translator (SCT) execution graph before execution:
    - **Semantic Validator Nodes (`semantic_validator`):** For each strategic step, the compiler injects a Semantic Validator node. The Local Model generates tool parameters as loose XML, which the validator deterministically coerces into the strict JSON required by tool schemas — handling type coercion, default imputation, and fuzzy matching without grammar-masking bottlenecks (ADR-0028).
    - **Proactive Binding Splice:** High-confidence bindings resolved by the Response Resolver (`recursive_key`, `fuzzy_key`) are stripped from the tool schema before inference and spliced back after — the model never sees or generates values it can't get wrong (ADR-0030).
    - **Deterministic Tool Executions (`deterministic`):** Wires a child execution node dependent on the validator. This node executes the target tool (native Go, WASM micro-skill, or stdio MCP) using the coerced arguments without LLM intervention, ensuring secure, sandboxed execution.
    - **Response Resolver:** A transparent post-execution step that normalizes raw tool outputs into a flattened property map using a three-tier cascade: recursive JSON key search, fuzzy key search, and semantic fallback via the Local Model (ADR-0029).
    - **Terminal Synthesis Node (`synthesis`):** Injects a final terminal synthesis node (`terminal_synthesis`) connected to all leaf endpoints. It reads the complete execution history and compiles all outputs into a cohesive natural-language summary.
  - **Edge Thought Generation:** On edge traversal, generates compact reasoning states (**Edge Thoughts**) via the Local Model when the target node has a non-zero Activation Threshold. See [Neural Edge Traversal](#6-neural-edge-traversal--activation-thresholds) below.
  - **Dynamic Interpolation & Conditionals:** Performs regex-based variable interpolation at level-start borders and evaluates branch conditional expressions natively without LLM calls.

### 3. The Tactician (Local Step Executor)

- **Underlying Model:** Backed by a **pluggable Inference Backend** — the embedded `llama-server` sidecar running a lightweight GGUF model (e.g., Qwen-3.5-Instruct 4B), a remote OpenAI-compatible server (LMStudio, Ollama, vLLM), or a harness callback routing inference through an external agent framework.
- **Execution Count:** Invoked once per active action node step (such as `semantic_validator`, `synthesis`, and Edge Thought generation nodes).
- **Responsibilities:**
  - **XML-Based Structured Generation:** Generates tool parameters as loose XML tags (`<tool>...</tool><args>...</args>`) rather than constrained JSON, restoring full decoding speed while the Semantic Validator handles coercion to strict JSON schemas.
  - **Context-Aware Structured Extraction:** Performs structured parameter extraction using an accumulated execution context block, enabling the validator to extract values by key name directly from labeled upstream output segments rather than re-parsing prose.
  - **Confidence Tier Pre-Flight Gate:** Before committing to a full inference call, the Local Model self-assesses whether it can extract the required parameters from the accumulated context. Returns `sufficient` (proceed locally) or `insufficient` (escalate to Cloud Model).
  - **Deterministic Coercion Pipeline:** Passes extracted parameters through Go-native coercion layers (handling numeric literals, string values, and double-braced reference interpolation). This corrects negative numbers or empty string extractions when explicit literals are present in the instruction text, eliminating parameter hallucinations.
  - **5-Layer Context Compaction:** Processes tool outputs through a series of compression layers (binary pruning, HTML-to-Markdown, TSV tabular hoisting, flat KV compacting, and tree flattening) before feeding them back to the model, preventing KV slot thrashing.
  - **Disk-Backed SQLite JQ Cache:** Saves raw JSON outputs exceeding **12KB** to a local SQLite table, returning a Cache Envelope to the model along with standard `jq` query tools (`jq_cached_data`) to query the data natively on-disk, keeping the context window pristine.
  - **Interactive Slot Preemption:** Pairs with the Go `PreemptionManager` to save active KV context states to disk (`slot_0.bin`) during interactive user chats, enabling sub-second chat responses (~450ms) and restoring background task states without data loss or re-evaluation cycles.

---

## 🚀 Key Subsystems

1. **Durable Execution Engine:** Coordinates long-running Tasks and Workflows by checkpointing operational states and execution edges to SQLite. If the daemon restarts or crashes, tasks resume safely from their last completed node.
2. **Kahn Graph Compiler:** Translates simplified Abstract Graph JSONs into an event-driven ready queue. Independent nodes fire concurrently as soon as dependencies are satisfied. Supports **Incremental Re-Sort** after runtime graph mutations.
3. **Neural Edge Traversal:** Generates **Edge Thoughts** on edge traversal via the Local Model. Evaluates **Activation Thresholds** to dynamically spawn new nodes when goal confidence is insufficient — creating a quasi-neural network where the DAG responds to runtime discoveries.
4. **Semantic Validator & Response Resolver:** The Semantic Validator coerces loose XML model output into strict JSON tool schemas. The Response Resolver normalizes raw tool outputs for downstream binding resolution. The Proactive Binding Splice strips deterministically-known values from inference entirely (ADR-0028, 0029, 0030).
5. **Pluggable Inference Backend:** Decouples structured LLM inference from the hosting process. Supports the embedded `llama-server` sidecar, remote OpenAI-compatible servers (LMStudio, Ollama, vLLM), or harness callback routing through an external agent framework.
6. **Relational Knowledge Graph Memory:** Stores enterprise entities, facts, and links in a local relational network. Uses **Hybrid Vector Search** (combining SQLite FTS5 keyword indexing and local ONNX cosine similarity ranking) for Neighborhood Multi-Hop context retrieval.
7. **Background Agents:** Long-lived autonomous processes running inside the daemon:
   - **Observer Agent:** Fires reactively on debounced telemetry events. Performs post-execution reflection — memory synthesis and knowledge graph extraction from completed task trajectories.
   - **Sentinel Agent:** Fires proactively on a periodic heartbeat timer. Correlates user activity patterns against memory and the knowledge graph, producing structured alerts (critical / suggestion / ambient) via Durable Notifications.
8. **Dynamic Workflow Orchestration:** Workflows support both static orchestration (pre-defined task graph) and dynamic LLM-driven orchestration where the Local Model decides the next child Task after each completion. Background Agents can spawn Workflows through the Proactivity Ladder (ADR-0027).
9. **Proactivity Ladder & Attention Scheduler:** A five-tier safety classification (L0 Observe → L4 External Side Effect) governing background action permissions. The Attention Scheduler coordinates background daemons under preemption, budget, and safety constraints, with an Attention Queue for user approval gates (ADR-0025).
10. **Agent App Package Manager:** Self-contained `.tzroapp` archives bundle tools, micro-skills, and SQLite migrations into installable capability extensions. The Package Manager handles install, uninstall, purge, and incremental MCP registration without disrupting in-flight tasks (ADR-0031).
11. **Delegation Mode:** Controls how aggressively the cloud model offloads sub-tasks to the Local Model via MCP completion and classification tools. Three tiers: Conservative (DAG only), Balanced (classification + extraction + formatting), Aggressive (everything except frontier reasoning).
12. **Filesystem Exploration Tools:** Built-in `read_file`, `list_dir`, and `search_files` tools with safe path validation, enabling autonomous codebase exploration within configured workspace boundaries.
13. **Sandboxed WebAssembly Micro-Skills:** Compiles specialized procedural logic into isolated WASM binaries, executing them safely on-device with strict resource and filesystem limitations.
14. **Stdio MCP Host Gateway:** Spawns external third-party tool servers dynamically over standard I/O (stdio) with thread-safe process self-healing, automatic recovery, and env-delegated credentials. Supports containerized MCP hosts via Docker.
15. **MCP Server Mode:** Presents tzro's capabilities as 38 MCP tool schemas and dynamic resource subscriptions over stdio, allowing external agent frameworks (Claude Desktop, Cursor, Gemini CLI, Google Antigravity) to consume tzro as a tool server. Operates simultaneously with the MCP Host role.
16. **Native Plugin Mode:** Runs in-process as a module within external agent harnesses (Hermes Agent, Google Antigravity SDK), dispatching primitive tools directly to the host process without cloud round-trips.
17. **Daemon Re-exec Restart:** The `tzro_restart` MCP tool and `POST /api/restart` endpoint trigger an in-place `syscall.Exec` process restart. The PID and pidlock survive, the inference sidecar is adopted without reload, and in-flight tasks recover on boot (ADR-0033).

---

## 🛠 Zero-Setup Quickstart

### 1. Provision the Engine

Execute the bootstrapper script to provision directories, compile (or download) the engine binaries, link the llama-server sidecar, download the default GGUF model, and initialize SQLite schemas:

```bash
./install.sh
```

The installer detects your platform (macOS / Linux, AMD64 / ARM64), builds from source if Go is available, or fetches pre-compiled release binaries. It also provisions MCP configurations for supported AI editors (Claude Desktop, Cursor, Gemini CLI).

### 2. Add Binary to PATH

Add the compiled `.tzro` binary path to your shell configuration file (`~/.zshrc` or `~/.bashrc`):

```bash
export PATH="$PATH:$HOME/.tzro/bin"
source ~/.zshrc
```

### 3. Run the Fullscreen TUI Console

To navigate local databases, inspect previous task executions, and control active daemons, run:

```bash
tzro
```

For direct offline database navigation (read-only SQLite inspection mode), run:

```bash
tzro --offline
```

### 4. Launch the Web Control Center

Launch the backend server daemon and the Vite React frontend dev server:

```bash
# Start backend server daemon (listens on 127.0.0.1:36888)
tzrod

# Start Vite React dashboard (in the web directory)
cd web
npm install
npm run dev
```

The Web Dashboard will be live at `http://localhost:8000`.

### 🎛️ Model Context Protocol (MCP) Server Integration

To run `tzro` in server mode and integrate it as an MCP server with Claude Desktop, Cursor, Gemini CLI, or Google Antigravity, follow these steps:

1. **Build the Binary:**
   ```bash
   go build -o bin/tzro-mcp ./cmd/tzro-mcp
   ```
2. **Register in Client Configuration:**
   - **Claude Desktop (Mac/Windows JSON Config):**
     ```json
     {
       "mcpServers": {
         "tzro": {
           "command": "/absolute/path/to/tzro/bin/tzro-mcp",
           "args": [],
           "env": {
             "PORT": "8080"
           }
         }
       }
     }
     ```
   - **Cursor Settings:** Add a new `command` type MCP server pointing to the absolute path `/absolute/path/to/tzro/bin/tzro-mcp`.
   - **Gemini CLI:** Add the server configuration to `~/.gemini/mcp_config.json`:
     ```json
     {
       "mcpServers": {
         "tzro": {
           "command": "/absolute/path/to/.tzro/bin/tzro-mcp",
           "args": []
         }
       }
     }
     ```
   - **Google Antigravity:** Add the server configuration to `.agents/mcp_config.json` inside your active workspace, defining necessary environment parameters like `TZRO_DIR`.
3. **Verify via Handshake Test:**
   Run `./bin/tzro-mcp` manually from your shell and paste this initialization JSON-RPC block:
   ```json
   {
     "jsonrpc": "2.0",
     "id": 1,
     "method": "initialize",
     "params": {
       "protocolVersion": "2024-11-05",
       "capabilities": {},
       "clientInfo": { "name": "test-client", "version": "1.0.0" }
     }
   }
   ```
4. **Safeguard the Stdio Pipe:**
   Standard input and output are strictly reserved for JSON-RPC message framing. All debug logging and runtime warnings in `tzro` are redirected to `stderr`. Never print to `stdout` inside custom tools, middleware, or extensions.

For more details, refer to the full **[MCP Setup Guide](docs/mcp-setup-guide.md)**.

---

## 📡 MCP Syscall Interface — 38 Tools

tzro exposes its full OS capabilities as MCP tools over stdio. These are the syscalls that any agent host can invoke:

### Core Execution (Kernel)
| Tool | Purpose |
|:---|:---|
| `tzro_run` | Plan, compile, and execute a durable DAG from a natural language prompt |
| `tzro_status` | Check execution status, node states, and outcomes of a task |
| `tzro_resume` | Resume a paused/interrupted task (e.g., after human approval) |
| `tzro_list_tasks` | List recent tasks, optionally filtered by status |
| `tzro_workflow` | Create and manage persistent multi-task Workflow orchestrations |
| `tzro_schedule` | Schedule one-shot or recurring cron-based task execution |
| `tzro_restart` | Trigger an in-place daemon re-exec restart via `syscall.Exec` |

### Local Inference (Cost Arbitrage)
| Tool | Purpose |
|:---|:---|
| `tzro_completion` | Structured text generation on the local model. Supports optional JSON schema (GBNF grammar) constraints. Zero cost, zero latency to external APIs |
| `tzro_classification` | Force-classify text into one of a set of categories using GBNF grammar constraints (guarantees output matches enum) |

### Memory & Knowledge Graph (Filesystem)
| Tool | Purpose |
|:---|:---|
| `tzro_memory_query` | Query memories using hybrid semantic/text similarity |
| `tzro_memory_ingest` | Ingest a fact, preference, insight, or strategy memory |
| `tzro_rag_context` | Get graph-RAG context retrieved semantically for a query |
| `tzro_kg_neighborhood` | Traverse connected entities in the knowledge graph via multi-hop |
| `tzro_kg_add_entity` | Add or update nodes and edge relationships in the knowledge graph |

### Micro-Skills (Skill Library)
| Tool | Purpose |
|:---|:---|
| `tzro_skills_list` | List all registered micro-skills and SOPs |
| `tzro_skills_get` | Get full details of a specific SOP skill by ID |
| `tzro_skills_relevant` | Find relevant micro-skills via semantic search |
| `tzro_skills_add` | Register a new Standard Operating Procedure (SOP) micro-skill |

### Agent Apps (Package Manager)
| Tool | Purpose |
|:---|:---|
| `tzro_apps_list` | List installed Agent App packages |
| `tzro_apps_install` | Install a `.tzroapp` capability extension |
| `tzro_apps_uninstall` | Uninstall an Agent App (soft-disable; use `purge` for destructive cleanup) |

### Tool Management (Device Drivers)
| Tool | Purpose |
|:---|:---|
| `tzro_configure_tools` | Provision external MCP server hosts dynamically for planning |
| `tzro_web_search` | Execute multi-engine web search with tiered fallback |
| `tzro_register_client_tools` | Register dynamic tool definitions that the planner can use |
| `tzro_client_tool_list` | List pending tool execution requests awaiting client-side outcomes |
| `tzro_client_tool_submit` | Submit tool results or errors to resume a paused workflow |

### Human-in-the-Loop (Permission System)
| Tool | Purpose |
|:---|:---|
| `tzro_hook_list` | List human-in-the-loop workflow approval requests |
| `tzro_hook_approve` | Approve a paused step and resume task execution |

### Background Daemons (Observability)
| Tool | Purpose |
|:---|:---|
| `tzro_observer_events` | Retrieve recent observer verification and audit logs |
| `tzro_observer_memories` | List memories dynamically synthesized by the Observer Agent |
| `tzro_activity_report` | Report current agent activity to enable Sentinel correlation |
| `tzro_sentinel_alerts` | Retrieve proactive Sentinel Agent alerts (critical / suggestion / ambient) |
| `tzro_sentinel_wake` | Manually trigger an immediate Sentinel analysis cycle |

### Model Management
| Tool | Purpose |
|:---|:---|
| `tzro_model_list` | List available GGUF models in the catalog with download status |
| `tzro_model_set` | Change the active local LLM model (downloads and swaps sidecars) |

### Dashboard (Control Center)
| Tool | Purpose |
|:---|:---|
| `tzro_dashboard` | Retrieve the current dashboard state |
| `tzro_dashboard_regenerate` | Regenerate the dashboard from current system state |
| `tzro_dashboard_spec` | Get the dashboard specification schema |

### Resource Subscriptions

For real-time observability, the MCP server exposes two URI templates for push notifications:

- **Task Output:** `tzro://tasks/{taskId}/output{?format}` — Status, metrics, and consolidated output for a task
- **Node Output:** `tzro://tasks/{taskId}/nodes/{nodeId}/output{?format}` — Status and output of a specific node

---

## 💻 Core Go SDK & Integration Guide

### 1. Configuration & Env-Delegated Secrets

The core engine configuration handles environment-delegated credentials, keeping configs clean of hardcoded secrets and fully compatible with cloud CI/CD pipelines.

```go
import "tzro/internal/config"

// Fetch global configuration settings
cfg := config.Get()
fmt.Printf("Operational Mode: %s\n", cfg.ModelMode) // cooperative, local, cloud

// Resolve environment-delegated secrets recursively
// E.g., "$OPENAI_API_KEY" -> fetches OS environment variable values
apiKey := config.GetCloudAPIKey()

// Get the active delegation mode (conservative, balanced, aggressive)
delegationMode := config.GetDelegationMode()
```

### 2. Custom Tool Registration

Developers can extend the tactician's runtime capabilities by registering custom tools implementing the `tools.Tool` interface.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"tzro/internal/tools"
)

type FileArchiveTool struct{}

func (f *FileArchiveTool) Name() string {
	return "archive_files"
}

func (f *FileArchiveTool) GetSchema() (string, error) {
	return `{
		"type": "object",
		"properties": {
			"sourcePath": {"type": "string", "description": "Target folder to archive"},
			"compress": {"type": "boolean"}
		},
		"required": ["sourcePath"]
	}`, nil
}

func (f *FileArchiveTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["sourcePath"].(string)
	compress, _ := args["compress"].(bool)

	fmt.Printf("[Archive Tool] Processing path: %s (compress: %v)\n", path, compress)

	resp := map[string]interface{}{
		"status": "archived",
		"target": path + ".zip",
	}
	bytes, _ := json.Marshal(resp)
	return string(bytes), nil
}

func init() {
	// Register the custom tool globally
	tools.Register(&FileArchiveTool{})
}
```

### 3. Topological Compilation & Execution

Initialize the database, compile the abstract dependency edges utilizing Kahn's sorting algorithm, and execute the graph:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Initialize SQLite storage layer
	memory.DB.SetDBPathForTesting("tzro_runtime.db")
	if err := memory.DB.Init(); err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer memory.DB.Close()

	// 2. Define Abstract Graph (DAG)
	graph := &compiler.ExecutionGraph{
		TaskID:    "t_quickstart_demo",
		CreatedAt: time.Now().Unix(),
		Nodes: []compiler.GraphNode{
			{
				ID:           "node_01",
				Type:         "action",
				Action:       "archive_files",
				Instructions: "Archive files in folder '/Users/jp/reports'.",
				AllowedTools: []string{"archive_files"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{},
	}

	// 3. Compile and topologically sort levels via Kahn Sorter
	levels, err := compiler.CompileAndSort(graph)
	if err != nil {
		log.Fatalf("Compilation failed: %v", err)
	}

	fmt.Printf("Kahn levels to execute in sequence: %v\n", levels)

	// 4. Execute the compiled graph programmatically
	err = executor.GlobalEngine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		log.Fatalf("Execution failed: %v", err)
	}
}
```

### 4. Telemetry Event Stream Subscription

Subscribe to real-time execution updates and live streaming token deltas published across the global StreamBus:

```go
import "tzro/internal/stream"

// Subscribe to global telemetry events matching our TaskID
sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
	return chunk.TaskID == "t_quickstart_demo"
})
defer sub.Unsubscribe()

// Consume streamed chunks asynchronously
go func() {
	for chunk := range sub.Ch {
		fmt.Printf("[Telemetry] Source: %s | Node: %s | Type: %s | Payload: %s\n",
			chunk.Source, chunk.NodeID, chunk.Type, chunk.Content)
	}
}()
```

### 5. Synchronous DAG Execution Hooks

While asynchronous telemetry updates over the `StreamBus` are suitable for non-blocking UI updates and monitoring, certain operations require a blocking, synchronous middleware layer. `tzro` provides **Synchronous DAG Execution Hooks** to allow developers to intercept, validate, mutate, or pause execution of Kahn-sorted Directed Acyclic Graph (DAG) tasks in-flight.

#### Key Use Cases

- **Data Sanitization & PII Scrubbing:** Redact or sanitize sensitive raw tool outputs (e.g., credentials, database keys, or personally identifiable information) in-memory before the payload is persisted in the SQLite checkpoint database or passed to downstream steps.
- **Dynamic Credential Injection:** Inject runtime environment variables and authentication tokens immediately before a node executes, avoiding the need to persist secrets in static graph blueprints.
- **Synchronous Guardrails:** Prevent unsafe operations by validating compiled node instructions against runtime safety policies (e.g., aborting or skipping destructive database commands).
- **Human-in-the-Loop (HITL) Gateways:** Pause execution between specific levels or node transitions to await manual user approval or supervisory checks.

---

#### Technical Architecture & Hook Interface

The hooks system is defined in `internal/executor/executor.go` and executes synchronously inside the Kahn execution engine lifecycle loop.

```go
type HookAction string

const (
	ActionContinue HookAction = "continue" // Proceed with normal execution
	ActionSkip     HookAction = "skip"     // Bypass the current node/level and propagate skip downstream
	ActionPause    HookAction = "pause"    // Pause task execution and yield ErrTaskPaused
	ActionAbort    HookAction = "abort"    // Interrupt execution and fail the task immediately
)

// ExecutionHook defines synchronous lifecycle hooks that developers can register
// on the ExecutionEngine to intercept and mutate DAG level/node executions.
type ExecutionHook interface {
	// BeforeLevel intercepts level execution before concurrent steps are launched.
	BeforeLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)

	// AfterLevel executes after all concurrent steps in a level finish processing.
	AfterLevel(ctx context.Context, taskID string, levelNodes []*compiler.GraphNode) (HookAction, error)

	// BeforeNode runs immediately before a single node begins execution.
	BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (HookAction, error)

	// AfterNode runs immediately after a tool call completes, enabling output mutation.
	AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (HookAction, error)
}
```

```
[ExecuteGraph]
      │
      ▼
[Fetch activeHooks via getHooksUnlocked()]
      │
      ▼
┌────────────────────────────────────────────────────────┐
│ Loop through Kahn Topological Levels                   │
│ ────────────────────────────────────────────────────── │
│ 1. Trigger BeforeLevel hooks on levelNodes             │
│    - Handles Pause, Skip (marks nodes & propagates),   │
│      or Abort actions.                                 │
│ 2. Launch concurrent level step goroutines:            │
│    ┌─────────────────────────────────────────────────┐ │
│    │ Goroutine for single nodeID                     │ │
│    │ ─────────────────────────────────────────────── │ │
│    │ a. Trigger BeforeNode hooks                     │ │
│    │ b. Run tool call / Local Model extraction       │ │
│    │ c. Trigger AfterNode hooks (allows mutating     │ │
│    │    rawOutput inline via string pointer)         │ │
│    │ d. Handle Compaction, Caching, & Checkpoints    │ │
│    └─────────────────────────────────────────────────┘ │
│ 3. Wait for goroutine pool completion                  │
│ 4. Handle ErrTaskPaused or Level Errors                │
│ 5. Trigger AfterLevel hooks on levelNodes              │
└────────────────────────────────────────────────────────┘
```

---

#### Lifecycle Execution Mechanics

##### 1. Durable Resumption (`ActionPause`)

If any registered hook returns `ActionPause` (such as a pending Human-in-the-Loop gateway):

- The executor immediately suspends execution of the active topological level.
- Completed steps are saved and checkpointed in SQLite under their `completed` status.
- The executor yields a concrete `ErrTaskPaused` sentinel error back to the caller.
- The background task daemon de-allocates context and local memory slots.
- Upon resumption, the Kahn compiler skips previously completed steps and resumes execution from the first incomplete level, preserving state and preventing duplicate tool executions.

##### 2. Dynamic Output Mutation (`AfterNode`)

The `AfterNode` hook receives a string pointer (`*string`) to the tool's raw response. Modifying this pointer:

- Dynamically overwrites the raw tool response in memory before it undergoes context compaction or serialization.
- Automatically feeds the updated payload into the subsequent 5-layer context compaction (`cache.Process`).
- Persists the sanitized payload cleanly to SQLite checkpoints.
- Ensures secure variable interpolation for downstream nodes that reference `{{nodes.node_id.output}}`.

##### 3. Concurrency & Thread-Safety

Kahn topological level execution processes steps concurrently inside separate goroutines. To avoid mutex lock contention and race conditions under parallel executions:

- The engine retrieves a copied snapshot of the registered hooks slice exactly once at the beginning of `ExecuteGraph` (`e.getHooksUnlocked()`).
- The engine passes the immutable hooks slice down to the goroutines and `executeSingleNode` calls.
- This copied snapshot technique ensures thread-safe, deadlock-proof execution under heavy concurrent workloads.

##### 4. Skip Propagation (`ActionSkip`)

Returning `ActionSkip` from a `BeforeLevel` or `BeforeNode` hook marks the associated node(s) as `skipped` in SQLite, publishes a telemetry event, and automatically propagates the skip downstream to all children and dependent nodes. No tool execution or local inference is performed.

---

#### Core SDK Integration Example

Below is a complete implementation demonstrating registration of a custom safety hook with PII sanitization and safety guardrails:

```go
package main

import (
	"context"
	"fmt"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/executor"
)

// CustomSafetyHook implements executor.ExecutionHook
type CustomSafetyHook struct{}

// BeforeLevel intercepts level execution before concurrent steps launch
func (c *CustomSafetyHook) BeforeLevel(ctx context.Context, taskID string, nodes []*compiler.GraphNode) (executor.HookAction, error) {
	fmt.Printf("[Hook] Preparing to execute level with %d nodes\n", len(nodes))
	return executor.ActionContinue, nil
}

// AfterLevel executes after all concurrent steps in a level complete
func (c *CustomSafetyHook) AfterLevel(ctx context.Context, taskID string, nodes []*compiler.GraphNode) (executor.HookAction, error) {
	fmt.Printf("[Hook] Finished level execution for %d nodes\n", len(nodes))
	return executor.ActionContinue, nil
}

// BeforeNode runs immediately before a single node begins execution
func (c *CustomSafetyHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
	// Custom Safety Guardrail: Prevent execution of destructive operations
	if node.Action == "delete_all_records" {
		fmt.Printf("[Hook] Safety Gate: skipping dangerous action node %s\n", node.ID)
		return executor.ActionSkip, nil
	}
	return executor.ActionContinue, nil
}

// AfterNode runs immediately after a tool call completes, enabling output mutation
func (c *CustomSafetyHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (executor.HookAction, error) {
	// Sanitize output dynamically before context compaction and persistence
	if node.Action == "fetch_user_details" && rawOutput != nil {
		*rawOutput = sanitizePII(*rawOutput)
	}
	return executor.ActionContinue, nil
}

func sanitizePII(input string) string {
	// Simple replacement logic for demo purposes
	return strings.ReplaceAll(input, "SSN_SECRET_VALUE", "[REDACTED]")
}

func main() {
	// Register the hook on the global execution engine
	executor.GlobalEngine.RegisterHook(&CustomSafetyHook{})
}
```

#### Verification & Testing

The hook framework is verified by unit tests located in [executor_hooks_test.go](internal/executor/executor_hooks_test.go), covering hook ordering sequences, skip propagation, task abortion, durable pausing/resuming, inline output mutation, and concurrent high-goroutine lock safety.
To run the hook test suite:

```bash
go test -v ./internal/executor -run TestHooks
```

---

## 📋 JSON Schema Specifications

### 1. Abstract Execution Graph (DAG) Schema

The Cloud Planner plans workflows conforming to the following structure:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ExecutionGraph",
  "type": "object",
  "properties": {
    "taskId": {
      "type": "string",
      "description": "Unique identifier for this task execution instance"
    },
    "maxCycles": {
      "type": "integer",
      "default": 5,
      "description": "Maximum cyclic iterations allowed"
    },
    "mutationBudget": {
      "type": "object",
      "description": "Per-task cap on dynamic node spawning (ADR-0024)",
      "properties": {
        "maxSpawns": { "type": "integer", "description": "Maximum total spawned nodes" },
        "remainingSpawns": { "type": "integer" },
        "consecutiveFailures": { "type": "integer", "description": "Failure dampening counter" }
      }
    },
    "nodes": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string",
            "description": "Unique node identifier (e.g. node_01)"
          },
          "type": {
            "type": "string",
            "enum": ["action", "conditional", "loop", "probe"]
          },
          "action": {
            "type": "string",
            "description": "Exact registered tool name to invoke"
          },
          "instructions": {
            "type": "string",
            "description": "Supports double-braced reference resolution: {{nodes.node_01.output.key}}"
          },
          "allowedTools": { "type": "array", "items": { "type": "string" } },
          "suggestedSkillIds": {
            "type": "array",
            "items": { "type": "string" }
          },
          "activationThreshold": {
            "type": "number",
            "minimum": 0.0,
            "maximum": 1.0,
            "default": 0.0,
            "description": "Sufficiency gate for Edge Thought generation. 0.0 disables. Kahn Compiler defaults: 0.0 for deterministic/synthesis, 0.7 for action nodes."
          },
          "status": {
            "type": "string",
            "enum": ["pending", "running", "completed", "failed", "skipped"]
          }
        },
        "required": ["id", "type", "action", "instructions"]
      }
    },
    "edges": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "sourceId": { "type": "string", "description": "Parent node ID" },
          "targetId": {
            "type": "string",
            "description": "Dependent child node ID"
          }
        },
        "required": ["sourceId", "targetId"]
      }
    }
  },
  "required": ["taskId", "nodes", "edges"]
}
```

### 2. StreamChunk SSE Telemetry Schema

Real-time state and token updates dispatched over Server-Sent Events (SSE) use this payload:

```json
{
  "streamId": "exec_t_quickstart_demo_node_01",
  "source": "executor",
  "taskId": "t_quickstart_demo",
  "nodeId": "node_01",
  "type": "token",
  "content": "{\"tool_arguments\": {\"sourcePath\": \"/Users/jp/reports\"}}",
  "usage": {
    "prompt_tokens": 128,
    "completion_tokens": 32
  }
}
```

---

## ⚡ Performance & Optimization Mechanics

### 1. Dual-Inject SOP Pipeline

To prevent slot thrashing inside the local model's attention window, `tzro` implements a **Dual-Inject SOP Pipeline** that separates index searches from complete markdown specifications:

- **Index Only (Cloud Planner):** The Cloud Planner only receives highly compressed procedural skill signatures (IDs and triggers) in the system prompt. This achieves an **80% system prompt reduction**, saving expensive cloud token costs.
- **Full-Text Injection (Go Executor):** When a topological step executes, the Go Executor fetches the full Markdown SOP text from SQLite and injects it directly into the local Tactician's system message for that specific step only.

### 2. 5-Layer Context Compaction Pipeline

Before feeding tool outputs into the local model, payloads are passed through a series of compression layers to strip syntax overhead:

- **Layer 0 (Binary Pruning):** Replaces base64 data headers with a compact structural reference `[binary:image/png, Size: 1.2MB]`.
- **Layer 1 (HTML Converter):** Translates raw HTML logs or scraper outputs into standard, compressed Markdown.
- **Layer 2 (Tabular Hoisting - JSON to TSV):** Translates homogeneous JSON arrays into single-row tab-separated values (TSV). By removing repeating keys (`"id"`, `"Name"`, `"attributes"`), this achieves a mathematical reduction ratio:
  $$R = 1 - \frac{\text{Length of TSV String}}{\text{Length of Raw JSON String}}$$
  In production CRM payloads, $0.60 \le R \le 0.85$, saving up to **85% of KV token space**.
- **Layer 3 (Single Object KV Compactor):** Reformats flat maps into line-delimited key-value sets (`"key: value\n"`), stripping brackets and quotes.
- **Layer 4 (Dot-Notation Tree Flattening):** Flattens nested structures up to depth 3 (`{"a":{"b":1}}` becomes `a.b: 1`) and discards internal system metadata.

### 3. SQLite Disk-Backed JQ Cache

If a compacted output payload exceeds a critical **12KB** threshold, the Go Executor intercepts the data:

1. Writes the complete, raw JSON to a local SQLite cache table (`disk_backed_jq_cache`).
2. Returns a lightweight **Cache Envelope** JSON struct to the LLM containing the cache ID, records count, and a sample record.
3. Appends a **Cache Exploration Guide** to the step's system instructions containing JQ exploration utilities (`jq_cached_data`, `read_cached_data`, `introspect_cache`).
4. The local model can then write precise `jq` query strings which are executed natively on-disk, feeding only the highly specific filtered result back into the model's active context window.

### 4. Priority KV Cache Preemption

Executing long-running background tasks on consumer hardware must not block the user's interactive, concurrent chat. The `PreemptionManager` enables multi-tenant local model execution over a single process:

```
[Background Task Executing Node Step]
             │
             ▼
[User Sends Interactive Chat Message]
             │
             ▼ (Preemption Triggered)
┌────────────────────────────────────────────────────────┐
│               Go PreemptionManager                     │
│ ────────────────────────────────────────────────────── │
│ 1. POST /slots/0/save to export KV context state to    │
│    ~/.tzro/models/kv-cache/slot_0.bin                  │
│ 2. POST /slots/0?action=erase to wipe active cache     │
│ 3. Execute User Chat completions on Slot 0             │◄── Sub-second Chat TTFT (~450ms)
│    (Generates immediate response)                      │
│ 4. POST /slots/0/restore to reload slot_0.bin          │
│ 5. Delete slot_0.bin from disk                         │
└────────────────────────────────────────────────────────┘
                             │
                             ▼
[Background Task Resumes Execution Safely]
```

This guarantees sub-450ms Time-To-First-Token (TTFT) for user-facing chat sessions while preventing data loss or token re-evaluation cycles for background execution tasks.

### 5. Surgical Cloud Fallback Escalation

To protect against local execution throttling (thermal slowdowns or memory depletion):

- The engine monitors the active generation speed ($T_{\text{speed}} = \text{tokens} / \text{seconds}$).
- If speed drops below **5 tokens/second** for **3 consecutive steps**, the engine sets `ForceCloudFallback = true` for the session.
- The Eino dynamic schema adapter automatically translates GBNF grammars into strict system-prompt JSON instructions, routing **only** that specific throttled step to Eino's cloud interface.

### 6. Neural Edge Traversal & Activation Thresholds

When a node has a non-zero **Activation Threshold** (0.0–1.0), the system generates an **Edge Thought** on each incoming edge after the source node completes. The Edge Thought is a compact reasoning state produced by the Local Model, containing:

- **Goal Confidence** (0.0–1.0): How sufficient the accumulated context is for the target node
- **Goal Achieved** (bool): Halt flag — the task's objective has already been met

The **sufficiency gate** then evaluates:

```
A completes → Edge A→B traversed → Edge Thought generated
  ├─ confidence ≥ threshold → B executes normally
  ├─ confidence < threshold → spawn new node between A and B
  └─ goalAchieved = true   → skip B and all downstream
```

Every spawned node is a **real, checkpointed DAG node** persisted to SQLite — not a hidden internal step. This creates a quasi-neural network where the DAG responds dynamically to runtime discoveries.

#### Safety Model
- **Mutation Budget**: Per-task cap on total spawns (prevents runaway expansion)
- **Failure Dampening**: 3 consecutive spawned-node failures suppress further spawning
- **Incremental Kahn Sort**: Only pending/new nodes are re-sorted after mutations; completed nodes are frozen
- **Zero overhead**: Nodes with `activationThreshold: 0.0` skip Edge Thought generation entirely

---

## 🤖 Guidelines for AI Coding Agents

If you are an AI coding assistant, software agent, or automated bot executing tasks in this repository, you **must** adhere to the following developer guidelines:

### 1. Respect the Local Wiki Architecture

- All system concept pages, historical bug diagnostic post-mortems, and workflow diagrams are persisted under `docs/wiki/`.
- Whenever you complete a feature refactor, diagnose a benchmark failure, or implement a design shift, you must:
  1. Append a summary of your actions to the chronological log at `docs/wiki/log.md`.
  2. Create or update relevant feature files under `docs/wiki/features/` or bugs under `docs/wiki/bugs/`.
  3. Ensure `docs/wiki/index.md` remains clean, sorted, and accurately linked.

### 2. Adhere to Domain Nomenclature

Always use standard terminology in code comments, schemas, and UI components. Never use deprecated v1 names:

- **Intent:** Natural language classification type (not "prompt type").
- **Task:** Compiled sequence of steps (not "process" or "swarm").
- **Workflow:** Persistent multi-task orchestrator (not "pipeline" or "mission").
- **Complexity Tier:** Execution routing rating T0/T1/T2 (not "performance score").
- **Kahn Compiler:** DAG compiler engine (not "graph builder").
- **Semantic Validator:** Deterministic XML-to-JSON coercion seam (not "output parser").
- **Response Resolver:** Tool output normalization for downstream bindings (not "output schema registry").
- **Edge Thought:** Compact reasoning state on DAG edges (not "short-term memory").
- **Activation Threshold:** Per-node sufficiency gate (not "firing threshold").
- **Agent App:** Installable capability extension (not "plugin" or "add-on").
- Refer to `CONTEXT.md` at the repository root for the full glossary list before writing variables or documentation.

### 3. Verification & Testing Posture

- All newly exported Go functions, compilation passes, or tool registrations **require** corresponding tests in their respective packages.
- Run tests first to establish a baseline before refactoring:
  ```bash
  go test ./...
  ```
- Ensure no formatting or linting errors exist by formatting the code prior to staging:
  ```bash
  go fmt ./...
  ```

### 4. Durable Execution Delegation (Offload Policy)

When executing any task that does not explicitly require frontier model reasoning (such as code generation, complex architectural design judgment, or interactive human dialogue), coding agents **must** delegate execution to the local `tzro` engine via `tzro_run` or `tzro` CLI.

- **The Offload Decision Rule:** Ask: *"Do I need frontier-model-exclusive reasoning about intermediate outputs to decide the next step?"*
  - **Yes** → Keep execution in-context (run cloud tool calls).
  - **No** → Offload to `tzro_run` as a background task.
- **Mandatory Delegation Patterns:**
  - Codebase exploration and directory analysis — delegate with a goal and let the system dynamically spawn exploration nodes via **Activation Thresholds** (e.g., `activationThreshold: 0.8, mutationBudget: 15`).
  - Multi-source web research and memory ingestion pipelines.
- **Trigger:** Evaluate DAG offloading if you are about to make **3 or more sequential external tool calls**, or have made **5 or more in-context calls**.
- **Wait Protocol:** Stop calling other tools, schedule a one-shot wakeup timer via the client, check task status via `tzro_status` until `completed`, and consume **only** the compiled `terminal_synthesis` outcome.

---

## ⚖️ Development & Testing

Keep the repository formatted, clean, and tested using the standard build environment scripts:

### Go Subsystems

```bash
# Run all Go unit and integration tests
go test -v ./...

# Format Go source files
go fmt ./...
```

### Vite React Dashboard

```bash
# Run ESLint validation
npm --prefix web run lint

# Build production bundle cleanly
npm --prefix web run build
```

---

## 📄 License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
