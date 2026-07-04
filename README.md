# tzro — The Agentic OS in a Thumbdrive

![tzro App Icon](static/icon.png)

> **tzro** is a portable, local-first agentic operating system that carries everything an AI agent needs to be productive: a durable scheduler, persistent memory, a knowledge graph, a tool registry, a local model, a skill library, a package manager, background daemons, and a permission system — all in a single `tzro.db` you can carry between machines. Plug it into any agent host over MCP, or embed it directly into Go applications.

---

## 🧠 Why an Operating System, Not a Framework

The market is saturated with agent frameworks. But tzro is not a library you import — it's a **runtime you plug your agent into**.

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
| **Syscall Interface** | 40 MCP tools exposing every OS capability over stdio |

The "Thumbdrive" metaphor captures three properties simultaneously: **instant activation** (one MCP config block), **completeness** (full OS inside), and **portability** (carry your `tzro.db` between machines).

---

## 📐 High-Level Architecture Overview

`tzro` separates cognitive scheduling and execution tasks into a clean **Strategy vs Tactics** split:
- **The Strategist (Cloud Planner):** Invoked exactly **once** at task startup using a remote **Cloud Model** to classify intent, complexity, retrieve micro-skills, compile the topological graph, and declare bindings/thresholds.
- **The Tactician (Local Step Executor):** Backed by a pluggable **Inference Backend** (such as the embedded local `llama-server` sidecar running a 4B model), executing individual steps inside isolation sandboxes.

For a comprehensive guide on the internal subsystems, compilation flow, neural edge traversal, context compaction pipelines, and the Go SDK hooks, please refer to the **[Architecture Guide](docs/ARCHITECTURE.md)**.

---

## 🚀 Zero-Setup Quickstart

### 1. Provision the Engine

Execute the bootstrapper script to provision directories, compile or download the engine binaries, link the llama-server sidecar, download the default GGUF model, and initialize SQLite schemas:

```bash
./install.sh
```

The installer detects your platform, builds from source if Go is available, or fetches pre-compiled release binaries. It also provisions MCP configurations for supported AI editors (Claude Desktop, Cursor, Gemini CLI).

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

---

## 🎛️ Model Context Protocol (MCP) Server Integration

To run `tzro` in server mode and integrate it as an MCP server with Claude Desktop, Cursor, Gemini CLI, or Google Antigravity, follow these steps:

### 1. Build the Binary
```bash
go build -o bin/tzro-mcp ./cmd/tzro-mcp
```

### 2. Register in Client Configuration
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

### 3. Verify via Handshake Test
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

### 4. Safeguard the Stdio Pipe
Standard input and output are strictly reserved for JSON-RPC message framing. All debug logging and runtime warnings in `tzro` are redirected to `stderr`. Never print to `stdout` inside custom tools, middleware, or extensions.

For more details, refer to the full **[MCP Setup Guide](docs/mcp-setup-guide.md)**.

---

## 📡 MCP Syscall Interface — 40 Tools

tzro exposes its full OS capabilities as MCP tools over stdio:

### Core Execution (Kernel)
| Tool | Purpose |
|:---|:---|
| `tzro_run` | Plan, compile, and execute a durable DAG from a natural language prompt |
| `tzro_code` | Generate or modify source files using the local model with automatic compilation validation |
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
| `tzro_sentinel_wake` | Trigger an immediate Sentinel analysis cycle |

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

## 🆕 v0.8.0 Highlights

- **Local Code Generation** — `tzro_code` generates or modifies source files entirely on the local model. Complexity-based routing picks single-pass or two-phase draft generation, module context is extracted from neighboring files, and a compilation quality gate auto-repairs failures via edge thought-driven DAG mutation.
- **High-Reliability Prompting** — Hardened prompt patterns for 4B local models achieve 4.0+ quality scores on Tier 4–5 codegen tasks using explicit technical anchors, pattern locking, and deterministic exit signals.
- **Docgen Category Routing** — The planner now mandates single-probe-node plans for documentation, function indexing, and architecture analysis — preventing misrouting through rigid action pipelines.
- **Regex Pattern Search** — `search_files` upgraded from substring matching to full regex support for more precise codebase exploration.
- **Expanded Tool Limits** — `read_file` cap raised from 100→500 lines, `list_dir` cap raised from 20→100 entries.
- **SQLite WAL Mode** — Memory subsystem enables Write-Ahead Logging for concurrent read/write performance.
- **Automated Benchmark Runner** — Multi-iteration evaluation loop with retry, averaging, and CSV output.

---

## 💻 Guidelines for AI Coding Agents

If you are an AI coding assistant, software agent, or automated bot executing tasks in this repository, you **must** adhere to the following developer guidelines:

### 1. Respect the Local Wiki Architecture
All system concept pages, historical bug diagnostic post-mortems, and workflow diagrams are persisted under `docs/wiki/`. Whenever you complete a feature refactor, diagnose a benchmark failure, or implement a design shift, you must log it chronology-wise in `docs/wiki/log.md`, create/update the relevant files in `docs/wiki/features/` or `docs/wiki/bugs/`, and update `docs/wiki/index.md`.

### 2. Adhere to Domain Nomenclature
Always use standard terminology in code comments, schemas, and UI components. Never use deprecated v1 names. Refer to `CONTEXT.md` at the repository root for the full glossary list before writing variables or documentation.

### 3. Verification & Testing Posture
All newly exported Go functions, compilation passes, or tool registrations **require** corresponding tests in their respective packages. Run tests (`go test ./...`) and code formatting (`go fmt ./...`) before proposing changes.

### 4. Durable Execution Delegation (Offload Policy)
When executing any task that does not explicitly require frontier model reasoning (such as code generation, complex architectural design judgment, or interactive human dialogue), coding agents **must** delegate execution to the local `tzro` engine via `tzro_run` or `tzro` CLI. Refer to `AGENTS.md` and the offload policies in `docs/ARCHITECTURE.md` for specific triggers and wait protocols.

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
