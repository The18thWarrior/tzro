# Turn Reduction Hypotheses Catalog

> **Core Value Proposition**: *Reduce the number of turns your agentic workflows need to take to achieve their goals.*

---

## 1. Executive Summary & Problem Context

In autonomous AI agentic workflows, **turn count is the ultimate multiplier of latency, cost, and failure probability**:
- **Quadratic Context Cost ($O(N^2)$)**: Because frontier chat models ingest the cumulative conversation history on every turn, a 15-turn workflow re-reads prior context 15 times. Reducing turns from 14 to 3 eliminates up to 75% of cumulative prompt tokens.
- **Latency Multiplication**: Each cloud inference turn incurs network transit, pre-fill queuing, and generative latency (typically 5–20 seconds per turn). A 15-turn task takes 3–5 minutes; a 3-turn task finishes in 20 seconds.
- **Drift & Compounding Hallucinations**: Each turn boundary introduces an opportunity for the model to lose context, pursue irrelevant tangents, misunderstand prior tool outputs, or terminate prematurely.

### 1.1 Observed Empirical Turn-Inflation Drivers (from `validation-v1` to `validation-v5`)

1. **Serial Discovery Chains (The 5-Turn Cold Start)**:
   Models spend Turns 1–5 performing serial directory and file inspection (`ls` $\to$ `find` $\to$ `grep` $\to$ `read_file` $\to$ `read_callee`) before writing a single line of code.
2. **Micro-Action Ping-Pong**:
   Models execute isolated micro-steps: Turn 5 (read file), Turn 6 (edit file), Turn 7 (run test), Turn 8 (view failure), Turn 9 (edit file again), Turn 10 (re-run test).
3. **Tool Parameter Errors & Apology Spirals**:
   In `validation-v3` and `validation-v5`, models frequently made trivial syntax errors (passing `#hash` with `#`, positional args to `--budget`, wrong positional args to `ingest`), triggering 2–3 wasted apology and retry turns.
4. **Data Exploration Marathons**:
   On tabular datasets, models often took 8–14 turns importing files, inspecting schemas, running isolated SQL queries, and debugging ad-hoc Python/awk scripts.
5. **Decomposition Spirals (Excessive Granularity)**:
   When skeletons elided function bodies too aggressively without return types, models issued up to 17 sequential `tzro expand` calls.
6. **Chatty Acknowledgment Turns (Zero-Progress Turns)**:
   Models frequently generate conversational turns without tools ("I see the file. Now I will inspect its contents"), consuming a turn without altering workspace state.

---

## 2. Exhaustive Hypothesis Catalog (48 Entries)

---

### Pillar 1: Preflight & Zero-Turn Context Seeding

#### T01: Task-Mention Preflight Scanner & Turn-1 Evidence Injection
* **Type**: Radical
* **Target Bottleneck**: Turns 1–4 spent finding files and reading signatures.
* **Mechanism**: When a task prompt arrives, tzro runs an offline lexical and AST scan *before dispatching the first request to the cloud LLM*. It identifies mentioned files, function names, and error strings, extracts their declaration spans and enclosing implementations, and injects them directly into the Turn 1 prompt as pre-loaded evidence.
* **Turn & Quality Impact**: Slashes turn count from 10–14 turns down to 2–3 turns. The model can formulate and apply the fix on Turn 1.
* **Implementation Surface**: `pkg/benchmark/workflow/execute.go`, Preflight hook.

#### T02: Automatic Failing-Test Triangulation on Turn 1
* **Type**: Visionary
* **Target Bottleneck**: Turns spent running tests to reproduce failures, then locating the test file.
* **Mechanism**: If the prompt describes a bug or mentions tests, the preflight hook executes the test suite locally before Turn 1. It extracts the failing assertion line, panic stack trace, and git diff, and injects them as a structured diagnostic into Turn 1.
* **Turn & Quality Impact**: Eliminates 2–3 exploratory turns; the agent starts with exact root-cause failure coordinates.
* **Implementation Surface**: `pkg/compactor/compactor.go`, Preflight engine.

#### T03: Repository Topology Pre-Mapping
* **Type**: Pragmatic
* **Target Bottleneck**: Initial blind `ls`, `find`, and directory traversal turns.
* **Mechanism**: Inject a compact 20-line structural topology (key packages, exported interfaces, entrypoint files) into the initial system context.
* **Turn & Quality Impact**: Completely eliminates Turns 1–2 of directory reconnaissance.
* **Implementation Surface**: `pkg/hooks/instructions.go`.

#### T04: Automatic Import & Call-Closure Pre-Resolution
* **Type**: Architectural
* **Target Bottleneck**: Turns spent jumping from caller to callee across separate files.
* **Mechanism**: When a target symbol is identified in preflight, parse its Concrete Syntax Tree to resolve its immediate callers, callees, and imported type definitions, bundling them into a single interconnected context graph on Turn 1.
* **Turn & Quality Impact**: Saves 3–4 exploratory file-reading turns.
* **Implementation Surface**: `pkg/ast/spans.go`, `pkg/context/`.

#### T05: Pre-Baked Schema & Head/Tail Injection for Tabular Tasks
* **Type**: Pragmatic
* **Target Bottleneck**: Turns 1–4 of tabular tasks spent doing `head`, `wc -l`, and schema inspection.
* **Mechanism**: Detect `.csv`, `.tsv`, or `.parquet` files mentioned in the prompt. Automatically inject: row count, column data types, and first 5 / last 5 rows directly into Turn 1.
* **Turn & Quality Impact**: Drops 3–4 exploratory data discovery turns immediately.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 2: High-Order Composite Macro-Tools

#### T06: `locate_and_read` (One-Shot Symbol Explorer)
* **Type**: Pragmatic
* **Target Bottleneck**: The ubiquitous 2-turn sequence: `probe <symbol>` at Turn $N$, followed by `view_file <path>` at Turn $N+1$.
* **Mechanism**: A unified tool that takes a symbol or pattern, locates the file via Tree-sitter AST, extracts its exact declaration span, docstrings, and full implementation, returning the complete code block in one turn.
* **Turn & Quality Impact**: Reduces symbol exploration from 2 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T07: `edit_and_test` (Atomic Modification & Verification)
* **Type**: Pragmatic / High-Impact
* **Target Bottleneck**: 3-turn ping-pong: Turn 1 (edit file), Turn 2 (run test), Turn 3 (inspect test result).
* **Mechanism**: Agent supplies the target file edit and the verification command. The tool applies the patch and immediately executes the test command locally, returning the edit status and test outcome in a single response.
* **Turn & Quality Impact**: Compresses a 3-turn cycle into 1 turn. Multiplied across 2–3 edit attempts, saves 4–6 turns per task.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T08: `diagnose_incident` (Automated Stack-to-Source Traceback)
* **Type**: Visionary
* **Target Bottleneck**: Incident diagnosis requiring 6–8 turns of log inspection, grep, stack tracing, and file reads.
* **Mechanism**: Ingests raw panic/error logs, parses stack frames, automatically fetches source lines for every application frame on disk, and returns the annotated call chain in a single turn.
* **Turn & Quality Impact**: Resolves incident diagnosis tasks in 1 turn instead of 6–8 turns.
* **Implementation Surface**: `pkg/compactor/`, `pkg/context/`.

#### T09: `data_pipeline` (Unified Tabular Ingest + Query + Compute)
* **Type**: Pragmatic
* **Target Bottleneck**: Ingesting a CSV, inspecting schema, and running multiple SQL queries across 7–14 turns.
* **Mechanism**: A single primitive that takes a file path and a SQL query (or aggregation goal), auto-imports the table into SQLite, runs the query, and returns formatted results or writes them to the target file.
* **Turn & Quality Impact**: Slashes tabular tasks from 12+ turns down to 1–2 turns.
* **Implementation Surface**: `pkg/compactor/tabular.go`, `cmd/tzro/mcp.go`.

#### T10: `refactor_symbol` (Global Multi-File Rename/Update)
* **Type**: Architectural
* **Target Bottleneck**: Iteratively editing 4–5 files one-by-one to rename or update a signature across turns.
* **Mechanism**: Accepts an old symbol signature and replacement, uses AST cross-references to apply the refactoring across definition, call-sites, and imports in one atomic workspace operation.
* **Turn & Quality Impact**: Turns a 5-turn sequential editing chore into 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/executor/tools.go`.

---

### Pillar 3: Local Speculative Execution & Self-Healing Loops

#### T11: Speculative Patching with Local Test Gating
* **Type**: Visionary
* **Target Bottleneck**: Cloud LLM trying minor syntax/formatting tweaks across multiple turns after failed test runs.
* **Mechanism**: Agent supplies a candidate fix. The local engine applies it and runs tests. If tests fail due to a simple compile/syntax error or minor typo, the engine attempts deterministic AST fixes (e.g. adding missing imports or formatting) before yielding.
* **Turn & Quality Impact**: Eliminates 1–3 compiler-debugging turns.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T12: Multi-Candidate Patch Evaluator
* **Type**: Visionary
* **Target Bottleneck**: Model unsure between two edge-case fixes, trying one, failing tests, then trying the second.
* **Mechanism**: Agent submits up to 3 candidate patch variants in a single tool call (`patches: [patchA, patchB, patchC]`). The local engine tests them in sequence and commits the first one that passes tests.
* **Turn & Quality Impact**: Reduces trial-and-error hypothesis testing from 3–6 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T13: Automatic Post-Edit Linter & Formatter Hook
* **Type**: Pragmatic
* **Target Bottleneck**: Wasted turns running `gofmt`, `eslint`, or fixing whitespace errors flagged by CI/tests.
* **Mechanism**: On every file modification, the tool hook automatically runs the project's native formatter (`gofmt`, `ruff`, `prettier`) before executing tests or returning to the model.
* **Turn & Quality Impact**: Completely eliminates turns spent resolving code formatting or lint failures.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T14: Local Deadlock & Concurrency Trace Harvester
* **Type**: Pragmatic
* **Target Bottleneck**: Integration tests deadlocking and hitting the full 30-second timeout, forcing the agent to retry or guess.
* **Mechanism**: Run tests with a tight timeout and Goroutine stack dump monitor. If deadlock occurs, capture Goroutine states, pinpoint the exact mutex and line numbers holding locks, and return the deadlock diagnostic immediately.
* **Turn & Quality Impact**: Eliminates 2–3 turns of blind concurrency debugging and timeout retries.
* **Implementation Surface**: `pkg/executor/shell_unix.go`, `pkg/compactor/`.

#### T15: Speculative Search-and-Expand
* **Type**: Architectural
* **Target Bottleneck**: Receiving a skeleton, realizing a function body is needed, and calling `expand` on the next turn.
* **Mechanism**: When an agent requests a skeleton and its prompt mentions a specific function, automatically expand that specific function in the initial skeleton response.
* **Turn & Quality Impact**: Saves 1 turn per file read by avoiding the immediate follow-up expansion call.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

---

### Pillar 4: Directed Graph & Multi-Step Pipeline Offload

#### T16: Shell-Pipe Execution DSL (`tzro pipe`)
* **Type**: Visionary
* **Target Bottleneck**: Models refusing to construct complex JSON DAGs, falling back to 6 sequential shell turns.
* **Mechanism**: Allow models to submit an intuitive Unix-style pipeline: `tzro pipe "probe VerifyToken | skeleton | grep crypto > out.txt"`. The engine compiles it into an execution graph and runs it on-device in 1 turn.
* **Turn & Quality Impact**: Replaces 4–6 interactive cloud turns with a single local execution.
* **Implementation Surface**: `pkg/executor/engine.go`, `cmd/tzro/execute.go`.

#### T17: Autonomous Local Exploration Sub-DAG
* **Type**: Visionary
* **Target Bottleneck**: Broad code investigation spanning 8–10 turns of grep, find, and file reads.
* **Mechanism**: Agent dispatches an exploration directive to Tzro's local engine: "Trace all callers of `GenerateInvoice` and return their error handling blocks". Tzro executes the traversal locally and returns only the final structured findings.
* **Turn & Quality Impact**: Collapses an 8-turn investigation into 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/executor/engine.go`.

#### T18: Conditional On-Device Branching Workflows
* **Type**: Architectural
* **Target Bottleneck**: Cloud model waiting for test output just to decide whether to commit or retry.
* **Mechanism**: Local execution graph with conditional branching: `Node A (run test) -> on_pass: Node B (write summary) -> on_fail: Node C (extract diff and stack trace)`. The entire decision tree runs locally.
* **Turn & Quality Impact**: Cuts 2–3 turns of mechanical branch evaluation.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T19: Multi-File Atomic Batch Patching in Single Graph
* **Type**: Pragmatic
* **Target Bottleneck**: Applying changes across 4 files sequentially over 4 separate turns.
* **Mechanism**: Submit an array of file patches in a single graph invocation. The engine applies them transactionally, verifies tests, and rolls back if tests fail.
* **Turn & Quality Impact**: Cuts multi-file editing from $N$ turns down to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T20: Deterministic Tabular Analysis Pipeline
* **Type**: Pragmatic
* **Target Bottleneck**: Ingest $\to$ Schema Check $\to$ Query 1 $\to$ Query 2 $\to$ Write Result taking 5–7 turns.
* **Mechanism**: Standardized local pipeline: Ingest CSV $\to$ Run SQL aggregations $\to$ Format JSON output $\to$ Write to disk in 25ms locally.
* **Turn & Quality Impact**: Reduces data tasks from 7–14 turns down to exactly 1 turn.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 5: Eliminating Ergonomic Errors & Recovery Detours

#### T21: Permissive Flag & Argument Normalization
* **Type**: Pragmatic
* **Target Bottleneck**: Real errors seen in validation: `#hash` rejecting `#`, positional args to `--budget`, positional table names to `ingest`.
* **Mechanism**: Make CLI parsers maximally tolerant: strip leading `#` from hashes automatically; detect numeric arguments to `context` as budgets; infer positional filenames vs table names.
* **Turn & Quality Impact**: Directly eliminates the 2–3 turn error recovery penalties observed in benchmark runs.
* **Implementation Surface**: `cmd/tzro/`, `pkg/context/`.

#### T22: Auto-Correcting Fuzzy Path Resolver
* **Type**: Pragmatic
* **Target Bottleneck**: Models hallucinating slightly incorrect paths (e.g. `/workspace/pkg/auth.go` instead of `pkg/auth/authenticator.go`), causing ENOENT errors and retry turns.
* **Mechanism**: When a tool receives a non-existent path, run an instant fuzzy match across workspace paths. If a high-confidence match exists, execute against the real path with an inline notice: `[Resolved to 'pkg/auth/authenticator.go']`.
* **Turn & Quality Impact**: Converts an immediate failure turn into an instant success turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T23: Missing Host Utility Shimming
* **Type**: Pragmatic
* **Target Bottleneck**: Model attempting to run `jq`, `sqlite3`, or `column` in isolated environments where they are missing, triggering failure and replanning turns.
* **Mechanism**: Provide built-in Go shims for common CLI data utilities (`jq`, `sqlite3`, `curl`, `tree`) within the tzro environment.
* **Turn & Quality Impact**: Prevents 1–2 turns of tool-discovery detours when standard host binaries are missing.
* **Implementation Surface**: `pkg/executor/shell_unix.go`.

#### T24: Pre-Execution AST Syntax Validation on Edits
* **Type**: Pragmatic
* **Target Bottleneck**: Model submitting code with unclosed braces or invalid syntax, waiting a full turn for a compiler failure, and spending another turn fixing it.
* **Mechanism**: Parse proposed edits with Tree-sitter before writing to disk. If syntax is malformed, reject immediately with exact line syntax diagnostics, prompting immediate in-turn correction.
* **Turn & Quality Impact**: Prevents wasted test execution turns on broken syntax.
* **Implementation Surface**: `pkg/ast/`, `pkg/executor/tools.go`.

#### T25: Suppressing Empty Apology & Conversational Filler Turns
* **Type**: Pragmatic
* **Target Bottleneck**: Chatty turns where the model says "I see the problem. Let me now edit service.go" without calling any tool.
* **Mechanism**: In the agent harness or proxy, detect assistant messages that contain zero tool calls and zero final answers. Prompt the model inline to emit the tool call immediately before returning the turn to the user/runner.
* **Turn & Quality Impact**: Eliminates 1–3 zero-progress conversational filler turns per task.
* **Implementation Surface**: `pkg/hooks/picoder.go`, Pi Extension.

---

### Pillar 6: Parallel Action Batching & Multi-Call Scheduling

#### T26: System-Level Multi-Tool Batching Enforcement
* **Type**: Pragmatic
* **Target Bottleneck**: Models issuing tool calls strictly one-by-one when operations are completely independent.
* **Mechanism**: Instruct the agent in system guidelines to emit parallel tool calls for independent reads, searches, and checks in a single turn.
* **Turn & Quality Impact**: Collapses 3 sequential reads into 1 parallel turn.
* **Implementation Surface**: `pkg/hooks/instructions.go`.

#### T27: Multi-File Slicing Primitive (`read_files_batch`)
* **Type**: Pragmatic
* **Target Bottleneck**: Reading 3 related interface files across 3 separate conversational turns.
* **Mechanism**: Expose a tool that accepts an array of file paths or symbol names, returning all declaration spans and implementations in a single consolidated response.
* **Turn & Quality Impact**: Cuts 3 file-reading turns down to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T28: Concurrent Probe & Grep Fan-Out
* **Type**: Pragmatic
* **Target Bottleneck**: Searching for symbol A, then searching for symbol B on the next turn.
* **Mechanism**: Allow `tzro probe` to accept multiple query terms simultaneously, returning combined ranked symbol matches in 1 turn.
* **Turn & Quality Impact**: Halves discovery turn count during multi-component investigations.
* **Implementation Surface**: `cmd/tzro/probe.go`, `pkg/search/`.

#### T29: Bundled Post-Edit Verification Packet
* **Type**: Pragmatic
* **Target Bottleneck**: Turn 10 (run test) $\to$ Turn 11 (`git diff` to verify) $\to$ Turn 12 (`git status`).
* **Mechanism**: Provide a verification tool that executes unit tests, computes git diff of modified files, and checks git status concurrently, returning the complete verification state in 1 turn.
* **Turn & Quality Impact**: Reduces post-edit verification from 3 turns to 1 turn.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T30: Batched Tabular Ingest & Multi-Query Array
* **Type**: Pragmatic
* **Target Bottleneck**: Calling `tzro query` 5 separate times across 5 turns to calculate 5 numbers.
* **Mechanism**: Allow `tzro query` to accept an array of SQL queries in a single call, executing them in a single SQLite transaction and returning all results at once.
* **Turn & Quality Impact**: Directly eliminates 4 cloud turns on analytical data tasks.
* **Implementation Surface**: `cmd/tzro/query.go`, `pkg/compactor/tabular.go`.

---

### Pillar 7: Rich Semantic Skeletons & Proactive Dependency Inlining

#### T31: Self-Contained Skeletons with Inlined Types & Return Shapes
* **Type**: Architectural
* **Target Bottleneck**: Skeletons showing function names but eliding return types or struct definitions, forcing follow-up read turns.
* **Mechanism**: Ensure AST skeletons retain complete type signatures, return structs, interface definitions, and field tags.
* **Turn & Quality Impact**: Eliminates the primary reason models call `expand` or read surrounding files, cutting follow-up turns by 80%.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

#### T32: Call-Site Preview Inlining
* **Type**: Architectural
* **Target Bottleneck**: Agent opening 2 other files just to see how a target function is invoked.
* **Mechanism**: When returning declaration spans, extract the top 2 call-sites across the codebase and inline them as 3-line usage snippets directly below the signature.
* **Turn & Quality Impact**: Satisfies the model's need for usage examples in 1 turn, avoiding 2 caller-reading turns.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/ast/spans.go`.

#### T33: Auto-Inlining of Small Functions (<6 Lines)
* **Type**: Pragmatic
* **Target Bottleneck**: Models requesting expansion of trivial 2-line helpers, getters, or error wrappers across turns.
* **Mechanism**: In AST skeletons, never elide function bodies that are 5 lines or fewer. Inline them directly.
* **Turn & Quality Impact**: Eliminates 1–3 expansion turns per file read.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

#### T34: Semantic Breadcrumb Inlining
* **Type**: Architectural
* **Target Bottleneck**: Viewing a method, then having to read the top of the file to understand receiver struct fields and imports.
* **Mechanism**: When slicing or skeletonizing an individual method, always include the enclosing struct definition, receiver fields, and relevant imports at the top of the excerpt.
* **Turn & Quality Impact**: Saves 1 turn of scrolling/reading file headers.
* **Implementation Surface**: `pkg/ast/spans.go`.

#### T35: Automatic Error Type & Constant Inlining
* **Type**: Pragmatic
* **Target Bottleneck**: Model reading `service.go`, seeing `return ErrInvalidTier`, then spending a turn searching for `ErrInvalidTier` definition.
* **Mechanism**: When returning a function's code, detect referenced package-level constants and errors, inlining their definitions in a comment block.
* **Turn & Quality Impact**: Eliminates 1 follow-up search turn per bug fix.
* **Implementation Surface**: `pkg/ast/spans.go`.

---

### Pillar 8: On-Device System 1 / Deterministic Decision Delegation

#### T36: Local Fast-Path Routing (JEV / ONNX Rule Router)
* **Type**: Visionary
* **Target Bottleneck**: Calling cloud LLM on Turn 1 just to ask "Which file should I inspect?".
* **Mechanism**: Use the local lightweight decision worker (JEV / GLiNER / rule classifier) to parse the task prompt and automatically trigger the initial file read or test run locally before the cloud LLM is invoked.
* **Turn & Quality Impact**: Skips Turn 1 cloud inference entirely; cloud LLM receives initial code on its first invocation.
* **Implementation Surface**: `pkg/decision/`, `pkg/extractor/`.

#### T37: Local Output Artifact Generator
* **Type**: Pragmatic
* **Target Bottleneck**: Agent spending 2 turns formatting and writing a required `summary.json` or benchmark result artifact.
* **Mechanism**: Provide a dedicated tool `write_benchmark_result(data: object)` that validates schema compliance and writes the required artifact in 1 turn.
* **Turn & Quality Impact**: Eliminates 1–2 formatting and retry turns at the conclusion of tasks.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### T38: Local Heuristic Bug Classifier & Fix Suggester
* **Type**: Visionary
* **Target Bottleneck**: Model spending 3 turns analyzing an obscure stack trace or nil pointer dereference.
* **Mechanism**: Parse panic stack traces with deterministic heuristics (e.g. nil pointer in mutex lock, missing map initialization, out of bounds slice), injecting the exact root cause and suggested fix pattern.
* **Turn & Quality Impact**: Cuts diagnostic analysis turns from 3 turns to 1 turn.
* **Implementation Surface**: `pkg/compactor/compactor.go`.

#### T39: Impact-Driven Test Selector (Fast Local Feedback)
* **Type**: Pragmatic
* **Target Bottleneck**: Running the entire test suite (taking 45 seconds or hitting timeouts) on every turn.
* **Mechanism**: Use `tzro impact` to automatically select and execute *only* the specific test that covers the modified function, running in <500ms.
* **Turn & Quality Impact**: Eliminates timeout failures and lets agents verify edits instantly in 1 turn.
* **Implementation Surface**: `pkg/context/impact.go`.

#### T40: Deterministic NL-to-SQL Template Engine
* **Type**: Pragmatic
* **Target Bottleneck**: Model taking 3 turns to craft and debug a SQL query for a simple aggregation.
* **Mechanism**: In tabular workflows, provide pre-compiled SQL templates for common analytical questions (sum by category, top N by revenue, filter by date), auto-executing on ingest.
* **Turn & Quality Impact**: Turns analytical query formulation into a 1-turn automated answer.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

---

### Pillar 9: Intent-Driven Declarative Primitives (Goal-Oriented Action)

#### T41: `solve_failing_test` Primitive
* **Type**: Radical
* **Target Bottleneck**: The entire 8-turn cycle of reproducing, finding, editing, and verifying a test failure.
* **Mechanism**: A single high-level tool that takes a failing test identifier. Tzro runs the test, locates the failing function, extracts its AST span, applies the agent's proposed logic change, and runs the test to confirm.
* **Turn & Quality Impact**: Solves targeted bug fix tasks in 1–2 turns total.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### T42: `implement_interface_method` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: 3 turns spent reading interface, finding target struct, and generating boilerplate method signature.
* **Mechanism**: Accepts interface name and struct name; auto-scaffolds the exact method signature and docstring in the target file, leaving only the body for the agent to complete.
* **Turn & Quality Impact**: Cuts 2 boilerplate turns from interface implementation tasks.
* **Implementation Surface**: `pkg/ast/`.

#### T43: `extract_and_summarize_dataset` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: 8 turns of exploratory data wrangling.
* **Mechanism**: High-level declarative tool: agent specifies the dataset file and natural language aggregation question. Tzro runs ingest, executes query, formats output, and writes `summary.json` in one atomic step.
* **Turn & Quality Impact**: Collapses the entire data analysis benchmark task into exactly 1 turn.
* **Implementation Surface**: `pkg/compactor/tabular.go`.

#### T44: `sync_documentation_and_types` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: Updating docs, types, and comments across 3 turns after a code change.
* **Mechanism**: Analyzes git diff of code changes, updates related docstrings and markdown specs across the repo in 1 turn.
* **Turn & Quality Impact**: Eliminates 2 post-implementation documentation turns.
* **Implementation Surface**: `pkg/context/impact.go`.

---

### Pillar 10: Radical & Assumption-Breaking Turn Reducers

#### T45: One-Turn End-to-End Task Solver (Zero-Turn Synthesis)
* **Type**: Radical
* **Target Bottleneck**: Multi-turn agentic workflows when a single high-context prompt could solve the task in Turn 1.
* **Mechanism**: When a task prompt arrives, tzro's offline compiler gathers all relevant source files, types, and test specs into a self-contained "mega-prompt". The frontier LLM emits the complete solution in Turn 1. A local runner verifies and commits.
* **Turn & Quality Impact**: Reduces turn count from 10–15 turns to **exactly 1 turn** ($>85\%$ turn reduction).
* **Implementation Surface**: `pkg/benchmark/workflow/execute.go`.

#### T46: Reverse Orchestration: Local Agent with Cloud Oracle Calls
* **Type**: Radical
* **Target Bottleneck**: Cloud LLM acting as the primary orchestrator, making slow cloud roundtrips for every trivial shell command.
* **Mechanism**: Invert the control loop: a local, deterministic state-machine agent runs locally on device, managing file reads, test runs, and git status. It calls the cloud LLM *only* as an oracle for code synthesis when a specific block needs implementation.
* **Turn & Quality Impact**: Slashes conversational turns from 15 down to 1–2 cloud calls.
* **Implementation Surface**: `pkg/executor/engine.go`, `cmd/tzro/`.

#### T47: Continuous Patch Streaming & Auto-Commit Loop
* **Type**: Visionary
* **Target Bottleneck**: Turn-based conversational handoffs between code generation and execution.
* **Mechanism**: Model streams a patch. The local engine applies hunks on the fly as they stream in, runs tests in parallel, and yields execution feedback immediately without waiting for full turn completion.
* **Turn & Quality Impact**: Converts discrete multi-turn cycles into a fluid single-stream execution.
* **Implementation Surface**: `pkg/proxy/`, `pkg/executor/`.

#### T48: Virtual Entity Mutation (Direct State Manipulation)
* **Type**: Radical
* **Target Bottleneck**: Navigating file paths, line numbers, and disk I/O across multiple turns.
* **Mechanism**: Represent codebase components as virtual entities in memory (e.g. `service.AccountDiscount[Enterprise] = 0.20`). The model mutates the entity state declaratively; the local engine compiles the change into AST source edits and commits.
* **Turn & Quality Impact**: Replaces 6 turns of file search, read, line identification, and editing with 1 declarative state mutation turn.
* **Implementation Surface**: `pkg/ast/`, `pkg/executor/`.

---

## 3. Turn Reduction Prioritization Matrix

| Tier | Focus Areas | Key Hypotheses | Expected Turn Reduction | Implementation Complexity |
| :--- | :--- | :--- | :--- | :--- |
| **Tier 1: Immediate Wins (Pragmatic)** | Composite Tools & Ergonomic Auto-Fixes | **T07** (`edit_and_test`), **T06** (`locate_and_read`), **T21** (Param Normalization), **T09** (`data_pipeline`), **T25** (Suppress Empty Turns) | **30% – 50% fewer turns** | Low (< 1-2 days each) |
| **Tier 2: Discovery Collapsers (Architectural)** | Preflight Seeding & Batching | **T01** (Preflight Scanner), **T02** (Failing-Test Triangulation), **T26** (Parallel Batching), **T31** (Rich Skeletons), **T39** (Impact Test Selection) | **50% – 65% fewer turns** | Medium (3-5 days) |
| **Tier 3: Autonomous Sub-Loops (Visionary)** | Pipe DSL, Multi-Candidate & Incident Packets | **T16** (`tzro pipe`), **T08** (`diagnose_incident`), **T12** (Multi-Candidate Patch), **T11** (Speculative Patching) | **60% – 75% fewer turns** | Medium-High |
| **Tier 4: Radical Paradigms (Assumption-Breaking)** | Control Inversion & Zero-Turn Synthesis | **T45** (One-Turn Solver), **T46** (Reverse Orchestration), **T41** (`solve_failing_test`), **T48** (Virtual Entity Mutation) | **75% – 90% fewer turns** (Down to 1-2 turns) | High (Architectural shift) |

---

## 4. Synergies Between Turn Reduction and Token Reduction

Reducing turns is the most powerful lever for reducing total tokens:
1. **Compounding Savings**: Each eliminated turn removes one entire round of prompt overhead ($2{,}000–$10{,}000 tokens per turn in mid-to-late conversation).
2. **Preventing Error Drift**: A 2-turn workflow cannot get lost in the weeds; a 15-turn workflow has a high cumulative probability of pursuing false leads.
3. **KV-Cache Alignment**: Fewer turns mean fewer prefix invalidations, maximizing the prompt cache hit rate across the entire task.
