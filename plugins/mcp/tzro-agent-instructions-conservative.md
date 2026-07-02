# tzro — Durable Local-First Execution Engine (Conservative Integration)

`tzro` compiles natural language prompts into topologically-sorted DAG workflows, executes them durably with checkpointing, and provides relational memory and knowledge graph capabilities. This file contains delegation guidelines for AI coding agents interacting with `tzro` via MCP or CLI.

---

## ⚖️ Durable Execution Delegation (Cost Optimization)

When executing tasks that involve **significant repetitive tool use** (8+ sequential calls for bulk operations), you **may** delegate to the local `tzro` engine to reduce cloud API costs and gain checkpointing benefits.

### The Offload Decision Rule

Ask one question:
> **"Is this task purely mechanical, involving 8 or more sequential tool calls with no frontier reasoning needed between steps?"**

*   **Yes** → Consider offloading to `tzro_run` as a background task.
*   **No** → Keep execution **in-context**.

### Trigger Policies
*   **Evaluation Trigger**: If you are about to make **8 or more sequential external tool calls**, pause and evaluate whether they should be offloaded as a DAG.
*   **Reassessment Trigger**: If you have made **12 or more in-context tool calls**, reassess whether the remaining work should be batched into a `tzro_run` task.

### Delegation Guidance

#### Recommended for Delegation
The following task patterns benefit from delegation to `tzro_run`:
*   **Memory ingestion pipelines** — bulk ingest operations across multiple sources.
*   **Web research and multi-source information gathering** — large-scale research with many queries.
*   **Codebase exploration** — navigating large unknown codebases with many directories.
*   **Bulk code generation** — creating multiple boilerplate files or applying surgical edits across many files via `tzro_code`.

#### Do NOT Delegate
*   **Targeted local file operations** (reading a single known file, viewing a specific function) — these feed frontier reasoning directly.
*   **Simple one-shot tool calls** or conversational Q&A.
*   **Management-plane MCP calls** (`tzro_list_tasks`, `tzro_status`, `tzro_model_list`, `tzro_skills_list`) — call these directly.
*   **Any task requiring intermediate reasoning** — keep in-context if you need to make decisions based on intermediate outputs.

---

### Wait Protocol
After calling `tzro_run`, you **MUST**:
1.  **Stop calling other tools.** Do not continue exploring or working in parallel.
2.  **Schedule a wakeup timer** using the `schedule` tool (e.g., set a one-shot reminder for 30-60 seconds).
3.  When notified, check task status via `tzro_status`.
4.  Resume only when the task status returns `completed` (or `failed`).
5.  Consume **only** the `terminal_synthesis` output.

---

## 📝 DAG Prompt Design Rules

When composing prompts for `tzro_run`, describe the *goal*, not the sequence of operations.
*   ❌ **BAD**: *"First call web_search, then parse the results, then save to memory"*
*   ✅ **GOOD**: *"Research the latest AI orchestration trends and save a structured summary to memory"*

### Delegation Templates

**Research & Ingestion:**
```
"Use web_search to find the latest changes and trends in [TOPIC], compile the findings, and save the final structured summary to memory using the save_memory tool."
```

**Codebase Exploration (Probe Node):**
```
"Explore the codebase at [PATH] using a Probe Node. Walk the directory structure to identify key entrypoints, read main modules, and output a high-level architectural overview. Use read_file, list_dir, and search_files."
```

**Local Code Generation:**
```
Use tzro_code to offload code generation.
- mode: "full" for new files (limit 500 lines).
- mode: "diff" for surgical updates to existing files.
```

---

## 📖 Domain Language Glossary

*   **Task**: A compiled sequence of execution steps (do not use "job" or "process").
*   **Kahn Compiler**: The compiler that topologically sorts abstract graphs into parallel execution layers.
*   **Local Model**: The local inference worker (GGUF, Llama sidecar) used for cost arbitrage.
*   **MCP Host**: The integration layer connecting external tools.
*   **Procedural Micro-Skill**: Standard Operating Procedures (SOPs) extracted from successful runs.
