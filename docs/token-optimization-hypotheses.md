# Token Optimization Hypotheses Catalog

> **Core Value Proposition**: *Reduce how many tokens your agentic workflows consume with no loss in quality.*

---

## 1. Executive Summary & Problem Context

This document codifies an exhaustive catalog of **48 hypotheses** designed to achieve Tzro's core value proposition: **slashing agentic workflow token consumption while preserving or improving task completion quality**.

### 1.1 Empirical Baseline & Observed Bottlenecks
Through rigorous paid and offline benchmark runs (`validation-v1` through `validation-v5` across representative real-world coding, refactoring, and data analysis tasks), we have isolated the primary drivers of token bloat and quality regressions in autonomous agents:

1. **Initial Prompt Tax (Fixed Schema Cost)**: 
   Standard tool definitions and skill instructions currently add ~1,167 fixed tokens to Turn 1 (2,863 prompt tokens for Standard vs 1,696 for Baseline). Because this schema is repeated or cached on every turn, small tasks (e.g. cache-control or quick edits) suffer a net token penalty before any work begins.
2. **Quadratic Context Accumulation ($O(N^2)$ Inflation)**:
   In multi-turn tasks (8–20 turns), historic tool results (large `read_file` outputs, verbose compiler logs, directory listings) linger in the prompt history for all subsequent turns. A 5,000-token file read at Turn 2 costs 50,000 cumulative tokens if the conversation runs 12 turns.
3. **Tool Adoption & Ergonomic Friction**:
   Frontier models default to standard shell reflexes (`cat`, `head`, `grep`, `python`). When custom tools (`tzro probe`, `tzro skeleton`, `tzro ingest`) have rigid flag formats (e.g. rejecting positional arguments, or tripping on `#hash` strings), models incur 2–3 expensive recovery turns that erase any compression savings.
4. **Decomposition Spirals (The "17 Expand" Failure Mode)**:
   When structural skeletons elide function bodies too aggressively without inlining critical return types or signatures, models enter desperate exploration spirals—issuing dozens of sequential `expand` requests rather than a single targeted read.
5. **Multi-Turn Cloud Roundtrips for Deterministic Work**:
   Ingesting a dataset, inspecting schema, and running multiple verification queries frequently consumes 7–14 separate cloud LLM hops when the entire pipeline could execute in milliseconds locally in a single sub-graph.
6. **The 12.5× KV-Cache Economic Multiplier**:
   When prompt prefixes fluctuate across turns (due to non-deterministic tool sorting, volatile timestamps, or dynamic instructions), cloud providers charge the full cache-miss price ($1.25 \times P_{\text{base}}$) instead of the cached rate ($0.10 \times P_{\text{base}}$), turning a 5% context increase into a massive billing spike.

---

## 2. Exhaustive Hypothesis Catalog (48 Entries)

---

### Pillar 1: Initial Prompt Tax & Tool Schema Compression

#### H01: Progressive Tool Schema Disclosure (Lazy Stubs)
* **Type**: Pragmatic
* **Target Bottleneck**: Turn 1 prompt tax (+1,167 tokens).
* **Mechanism**: Register only lightweight tool stubs (~80 tokens total) on Turn 1 with minimal descriptions. The full schema for specialized tools (e.g., `tzro_execute_graph`, `tzro ingest`, `tzro impact`) is disclosed dynamically only when the agent expresses intent or reaches a relevant stage.
* **Token & Quality Impact**: Slashes Turn 1 overhead by 85% across all 7 benchmark tasks. Eliminates prompt bloat on simple tasks that never use complex tools.
* **Implementation Surface**: `pkg/hooks/picoder.go`, Pi Extension.

#### H02: Compact TypeScript-Style Interface Signatures for Tool Definitions
* **Type**: Pragmatic
* **Target Bottleneck**: Verbose JSON Schema formatting (`properties`, `type`, `description` trees).
* **Mechanism**: Replace standard JSON Schema object definitions with ultra-compact TypeScript/Go type signatures in tool definitions (e.g. `execute_graph(graph: {nodes: Node[], entrypoints?: string[]}): Result`). Frontier LLMs parse TS definitions with equal or higher parameter accuracy.
* **Token & Quality Impact**: Reduces tool definition tokens by 65–75% per turn with zero loss in parameter compliance.
* **Implementation Surface**: `pkg/executor/tool_schema.json`, `pkg/hooks/setup_clients.go`.

#### H03: Repository-Aware Dynamic Skill Stripping
* **Type**: Pragmatic
* **Target Bottleneck**: Irrelevant multi-language documentation loaded into context.
* **Mechanism**: During preflight/startup, detect repository characteristics (e.g., Go workspace with no Python or CSV files). Dynamically strip out instructions, examples, and rules for irrelevant languages and tools before injecting `SKILL.md`.
* **Token & Quality Impact**: Drops 400–800 static tokens from the system prompt on every single turn.
* **Implementation Surface**: `pkg/hooks/instructions.go`, `cmd/tzro/mcp.go`.

#### H04: Single Unified Poly-Tool Dispatcher
* **Type**: Architectural
* **Target Bottleneck**: Multiple discrete tool schemas multiplying prompt token size.
* **Mechanism**: Collapse `tzro probe`, `tzro context`, `tzro skeleton`, `tzro expand`, `tzro ingest`, and `tzro query` into a single tool definition: `tzro(action: string, args: string[])`.
* **Token & Quality Impact**: Cuts 6 separate tool definitions down to 1 schema (~150 tokens total), saving ~1,000 prompt tokens per turn.
* **Implementation Surface**: `pkg/hooks/pi_extension.ts`, `pkg/executor/tools.go`.

#### H05: Zero-Schema Shell Interception (Tool-Free Delivery)
* **Type**: Radical
* **Target Bottleneck**: Exposing any custom LLM tool definitions at all.
* **Mechanism**: Register *zero* custom tools in the agent's schema. Let the agent use its native `run_command` / `bash` tool exclusively. Transparently alias or intercept standard commands (`cat`, `grep`, `head`) via a pseudo-terminal or PATH shim to execute tzro compaction automatically.
* **Token & Quality Impact**: Exactly 0 additional tool schema tokens added to baseline prompt; achieves 100% adoption because the model uses its standard shell reflex.
* **Implementation Surface**: `pkg/executor/shell_unix.go`, Isolated PATH environment.

---

### Pillar 2: Quadratic Context Decay & Conversational Garbage Collection

#### H06: Sliding-Window Tool Result Tombstoning (Eager Output GC)
* **Type**: Visionary
* **Target Bottleneck**: Stale tool outputs from Turns 1–5 lingering through Turns 10–20 ($O(N^2)$ context inflation).
* **Mechanism**: When a tool result is older than $K$ turns ($K \approx 2$) and subsequent assistant turns have already processed it (e.g., produced code or moved to a new file), replace the raw tool output in the context array with a 1-line cryptographic tombstone: `[Output of 'read_file service.go' (850 lines) compacted at Turn 4: hash #a8f19c]`.
* **Token & Quality Impact**: Flattens token trajectory from quadratic to linear ($O(N)$). Eliminates up to 60% of total tokens on tasks taking $>8$ turns.
* **Implementation Surface**: `pkg/proxy/`, Message normalizer / KV-lock loopback proxy.

#### H07: Dead-Read Tombstoning on File Modification
* **Type**: Pragmatic
* **Target Bottleneck**: Retaining old file reads after the file has been edited.
* **Mechanism**: If an agent reads `pkg/service.go` at Turn 2, and then calls `write_to_file` or `edit` on `pkg/service.go` at Turn 5, immediately tombstone the Turn 2 read output. The model already has the latest state in the edit call.
* **Token & Quality Impact**: Prevents duplicate representations of the same file from co-existing in context. Saves 1,000–5,000 tokens per modified file.
* **Implementation Surface**: `pkg/proxy/`, `pkg/hooks/output.go`.

#### H08: Failed-Turn Splicing (Pruning Error Detours)
* **Type**: Pragmatic
* **Target Bottleneck**: Retaining syntax errors, bad flags, or failed test runs across the rest of the conversation.
* **Mechanism**: When a command fails (e.g., `exit 1` or tool parameter validation error) and the agent immediately issues a corrected command that succeeds on the next turn, splice out the failed turn's verbose stderr/trace from future prompt history, replacing it with a minimal marker: `[Command retried successfully]`.
* **Token & Quality Impact**: Prevents error spirals where models obsess over previous failure traces; cuts 300–1,200 tokens per retried error.
* **Implementation Surface**: `pkg/proxy/`, `pkg/hooks/picoder.go`.

#### H09: Ephemeral Deliberation / Scratchpad Pruning
* **Type**: Radical
* **Target Bottleneck**: Model reasoning chains (`<thought>` or verbose planning text) converting into permanent prompt tokens on every future turn.
* **Mechanism**: Strip or summarize the model's generated natural language thoughts/deliberations from turns $>1$ turn in the past, preserving only the tool call and the tool response.
* **Token & Quality Impact**: Saves 300–1,000 input tokens *per turn* for every turn in the history, keeping the conversation purely operational.
* **Implementation Surface**: `pkg/proxy/` request pipeline.

#### H10: Sliding Search/Discovery Deduping
* **Type**: Pragmatic
* **Target Bottleneck**: Repeated `ls`, `git status`, or repetitive `grep` outputs filling conversation history.
* **Mechanism**: When an agent runs discovery commands that return identical or subset results compared to an earlier turn, replace the payload with `[Identical to Turn N result]`.
* **Token & Quality Impact**: Eliminates redundant environment noise, saving 200–800 tokens per repeated command.
* **Implementation Surface**: `pkg/compactor/`, `pkg/hooks/output.go`.

---

### Pillar 3: Zero-Adoption Transparent Hooks (Invisible Compaction)

#### H11: Transparent AST Skeletonization on Native `read_file`
* **Type**: Pragmatic
* **Target Bottleneck**: Baseline agents reading 800-line source files in full because they don't know to call `tzro skeleton`.
* **Mechanism**: Hook into the agent's default `read_file` tool. If the file is $>80$ lines and matches a supported AST language, automatically return the structural skeleton with line numbers and elided bodies, appending a 1-line hint on how to slice or expand exact lines.
* **Token & Quality Impact**: 70–85% token reduction on every whole-file read without requiring the model to change its tool habits.
* **Implementation Surface**: `pkg/hooks/output.go`, `pkg/ast/`.

#### H12: Transparent Head/Tail + Schema Interception on Tabular Reads
* **Type**: Pragmatic
* **Target Bottleneck**: Massive variance on CSV/data tasks (Baseline reading 1,000 rows / 45,521 bytes vs 15 rows).
* **Mechanism**: When any read or shell command attempts to read a `.csv`, `.tsv`, or `.parquet` file, intercept the output and return: (1) schema and data types, (2) row count and summary statistics, (3) first 5 and last 5 rows in compact markdown, and (4) an auto-imported SQLite table pointer.
* **Token & Quality Impact**: Neutralizes the 300,000-token variance observed in `revenue_8192`. Enforces 95%+ data token reduction regardless of model behavior.
* **Implementation Surface**: `pkg/compactor/tabular.go`, `pkg/hooks/output.go`.

#### H13: Auto-Compaction of Go / Python / Cargo Test Runners
* **Type**: Pragmatic
* **Target Bottleneck**: Verbose test logs emitting hundreds of passing tests and standard-library stack frames.
* **Mechanism**: Automatically intercept `go test`, `pytest`, `cargo test`, `npm test` commands in the execution hook. Surface only: failure summary, exact failing assertion line, and 5 lines of contextual diff. Elide runtime/stdlib frames.
* **Token & Quality Impact**: 80–90% token reduction on test failure outputs with zero loss of root cause diagnostics.
* **Implementation Surface**: `pkg/compactor/compactor.go`.

#### H14: Virtual Workspace Spillover for Bulky Command Output
* **Type**: Pragmatic
* **Target Bottleneck**: Unexpected massive command outputs flooding the context window (e.g., `cat` on a bundle or huge build log).
* **Mechanism**: Set an automatic 2,000-character / 30-line inline cap on all shell tool results. If exceeded, persist the full output to `.tzro/spillover/out_<id>.txt`, return the top 20 lines, the bottom 10 lines, and the exact path for targeted inspection.
* **Token & Quality Impact**: Hard-caps worst-case tool output size, guaranteeing an agent never burns 20k tokens on an accidental dump.
* **Implementation Surface**: `pkg/hooks/output.go`.

#### H15: Selective AST Expansion on Write / Diff
* **Type**: Architectural
* **Target Bottleneck**: Model rewriting entire 500-line files when editing a 5-line function.
* **Mechanism**: Intercept `write_to_file`. If the target file already exists and the edit modifies $<20\%$ of the AST, calculate the AST diff locally and apply it, confirming the edit without echoing the full file content back into context.
* **Token & Quality Impact**: Eliminates massive generation tokens and echoes, saving 1,000–4,000 tokens per file modification.
* **Implementation Surface**: `pkg/executor/tools.go`, `pkg/ast/`.

---

### Pillar 4: Eliminating Ergonomic Friction & Recovery Spirals

#### H16: Permissive Parameter Normalization (Zero-Error CLI)
* **Type**: Pragmatic
* **Target Bottleneck**: Real errors seen in validation: `#hash` rejecting `#`, positional args to `--budget`, positional table names to `ingest`.
* **Mechanism**: Update CLI flag and argument parsers to accept all observed variants: strip leading `#` from hashes automatically; detect numeric arguments to `context` as budgets; infer positional filenames vs table names.
* **Token & Quality Impact**: Directly eliminates the 2–3 turn recovery penalties that flipped Standard from saving 33% to losing 20% in `validation-v3` and `validation-v5`.
* **Implementation Surface**: `cmd/tzro/`, `pkg/context/`.

#### H17: Self-Contained Skeletons with Inlined Signatures & Return Types
* **Type**: Architectural
* **Target Bottleneck**: The "17 expand calls" failure mode: models receive a skeleton, but because return types or struct fields are missing, they issue dozens of expansions.
* **Mechanism**: Enhance AST skeletons to retain complete type signatures, return types, struct field tags, and one-line docstrings. Only elide the internal imperative block of the function body.
* **Token & Quality Impact**: Provides enough semantic context that 90% of `tzro expand` calls become completely unnecessary, stopping expansion loops before they start.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

#### H18: Named Symbol Target Expansion (`expand --symbol`)
* **Type**: Pragmatic
* **Target Bottleneck**: Models struggling to copy 64-character SHA-256 hashes or getting tripped by hash formatting.
* **Mechanism**: Allow `tzro expand <filepath> --symbol <FunctionName>` or `tzro expand <FunctionName>` directly in addition to hash-based expansion.
* **Token & Quality Impact**: Replaces cryptographic hash hunting with natural language symbol names; reduces parameter errors to zero.
* **Implementation Surface**: `cmd/tzro/`, `pkg/context/`.

#### H19: Immediate In-Band Auto-Correction of Invalid Commands
* **Type**: Pragmatic
* **Target Bottleneck**: Tool errors returned to the LLM triggering an apology turn and re-planning.
* **Mechanism**: When a tool call is malformed (e.g. `tzro skeleton orders.csv` on a CSV file), the tool hook detects the file type, automatically executes the correct tool (`tzro ingest orders.csv`), and returns the intended result with an inline notice: `[Auto-corrected to 'tzro ingest orders.csv']`.
* **Token & Quality Impact**: Converts a 2-turn error-and-recovery detour into a 0-turn seamless success.
* **Implementation Surface**: `pkg/hooks/pi_extension.ts`, `pkg/executor/engine.go`.

#### H20: Proactive Context Pre-Loading in Skeletons
* **Type**: Architectural
* **Target Bottleneck**: Skeletons showing callers without callees, prompting repeated grep turns.
* **Mechanism**: When skeletonizing a file, if a function is under 5 lines (e.g., simple getters, delegators, or panic wrappers), do not elide it. Inline small bodies directly.
* **Token & Quality Impact**: Drastically cuts follow-up expansion requests for trivial functions while preserving 80% compaction on large functions.
* **Implementation Surface**: `pkg/ast/skeleton.go`.

---

### Pillar 5: High-Order Composite Primitives (Collapsing Multi-Turn Workflows)

#### H21: Atomic `find_and_view` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: The ubiquitous 2-step hop: `tzro probe <symbol>` at Turn 1, followed by `view_file <path> <lines>` at Turn 2.
* **Mechanism**: Create a single composite tool `find_and_view(query: string)` that searches for the symbol/pattern, extracts the exact declaration span and implementation, and returns the focused code in one turn.
* **Token & Quality Impact**: Collapses a 2-turn cloud roundtrip (and its duplicate context) into a single turn. Cuts exploration tokens by 50%.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### H22: Atomic `test_and_diagnose` Primitive
* **Type**: Pragmatic
* **Target Bottleneck**: Agent runs `go test`, sees failure, then runs `git diff` or reads the test file in separate turns to understand what broke.
* **Mechanism**: Execute tests, capture failure, parse the failing test's source file and assertion lines, grab current git diff of tested code, and package into a single structured diagnostic packet.
* **Token & Quality Impact**: Reduces debugging initiation from 3 turns to 1 turn.
* **Implementation Surface**: `pkg/compactor/compactor.go`, `pkg/executor/tools.go`.

#### H23: One-Shot `patch_and_verify` Primitive
* **Type**: Architectural
* **Target Bottleneck**: Sequential edit -> run test -> check output across 2–3 cloud turns.
* **Mechanism**: Agent supplies an edit and the test command to verify. The tool applies the patch in memory or workspace, runs the test, and returns the test outcome and diff status in a single response. If tests fail, it can automatically roll back.
* **Token & Quality Impact**: Replaces 2 cloud model hops with 1 local execution, slashing verification latency and tokens by ~60%.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### H24: Unified Tabular Analytics Primitive (`data_query`)
* **Type**: Pragmatic
* **Target Bottleneck**: Ingesting a CSV, inspecting schema, and running 3–5 SQL queries taking 7–14 turns.
* **Mechanism**: A single tool `data_query(file: string, query: string)` that transparently handles ingest (if not already loaded), runs the SQL query, and returns the tabular result in one step.
* **Token & Quality Impact**: Prevents the 14-request marathon seen in Standard task 7; reduces data analysis workflows to 1–2 turns.
* **Implementation Surface**: `pkg/compactor/tabular.go`, `cmd/tzro/mcp.go`.

#### H25: Automated Incident Diagnostic Packet (`incident_investigate`)
* **Type**: Visionary
* **Target Bottleneck**: Incident tasks requiring log inspection, grep, stack trace tracing, and source reads across 6–8 turns.
* **Mechanism**: Ingest error log or panic string, extract stack trace, locate application frames on disk, extract declaration spans for every frame, and return the complete call stack with code snippets in a single turn.
* **Token & Quality Impact**: Solves panic/incident diagnosis tasks in a single turn (1 request vs 7 requests).
* **Implementation Surface**: `pkg/compactor/`, `pkg/context/`.

---

### Pillar 6: Local Graph Execution & Autonomous Sub-Loops

#### H26: Pipe-Oriented Execution DSL (Unix-Style Local Graph)
* **Type**: Visionary
* **Target Bottleneck**: Models refusing to write cumbersome JSON DAGs (`{"nodes": [...], "edges": [...]}`).
* **Mechanism**: Allow models to submit a linear or branching pipe DSL string: `tzro pipe "ingest orders.csv | query 'SELECT sum(total) FROM orders' > summary.json"`. The engine compiles it into an execution graph and runs it locally.
* **Token & Quality Impact**: Unlocks adoption of local graph execution by matching the model's natural shell instincts. Eliminates cloud roundtrips for multi-step pipelines.
* **Implementation Surface**: `pkg/executor/engine.go`, `cmd/tzro/execute.go`.

#### H27: Speculative Patch & Test Loop (Local Iteration)
* **Type**: Radical
* **Target Bottleneck**: Cloud LLM trying slight variations of a syntax fix across 4 separate turns.
* **Mechanism**: Agent provides a candidate fix with optional variation hints. The local engine applies the patch, runs tests, and if failing, iterates locally using rule-based transformations or yields with test diagnostics.
* **Token & Quality Impact**: Offloads trial-and-error compile/test loops from cloud inference to local compute.
* **Implementation Surface**: `pkg/executor/engine.go`.

#### H28: Transient Local Python/Go Script Runner
* **Type**: Pragmatic
* **Target Bottleneck**: Agent making 5 model hops to calculate metrics, parse data, or verify math.
* **Mechanism**: Provide a dedicated sandbox tool `tzro_eval(script: string)` where the agent writes a quick one-off script that runs locally, performs data transformation or verification, and outputs only the final answer.
* **Token & Quality Impact**: Prevents the pattern where an agent queries SQLite 5 times to do arithmetic that a 3-line script could compute in 1 millisecond.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### H29: Task-Triggered Macro Scaffolding
* **Type**: Architectural
* **Target Bottleneck**: Cold-start discovery turns at the beginning of standard coding tasks.
* **Mechanism**: When an agent receives a bug fix prompt, tzro detects mentioned files or identifiers and runs preflight discovery before the agent's first action, attaching relevant skeletons and failed test traces to Turn 1.
* **Token & Quality Impact**: Eliminates turns 1–3 of blind file finding (`find`, `ls`, `grep`).
* **Implementation Surface**: `pkg/context/`, `pkg/hooks/instructions.go`.

---

### Pillar 7: KV-Cache Multipliers & Prefix Determinism

#### H30: Strict Byte-for-Byte Prefix Pinning
* **Type**: Architectural
* **Target Bottleneck**: 12.5× cache miss penalty when timestamps, session IDs, or reordered tools invalidate KV-cache prefixes.
* **Mechanism**: In `pkg/kvlock`, enforce strict byte-for-byte serialization of system prompt, workspace instructions, and tool definitions across every single request. Move all dynamic state (working directory, session time, step counter) into the final user turn or trailing metadata.
* **Token & Quality Impact**: Locks KV cache hit rates at 90–98%, dropping input token costs from $1.25 \times P_{\text{base}}$ to $0.10 \times P_{\text{base}}$.
* **Implementation Surface**: `pkg/kvlock/`, `pkg/proxy/`.

#### H31: Deterministic Tool Output Canonicalization
* **Type**: Architectural
* **Target Bottleneck**: Non-deterministic tool output serialization (e.g. random JSON key ordering, varying whitespace) causing downstream cache misses.
* **Mechanism**: Pass all tool outputs through a canonicalizer that sorts JSON keys, normalizes file paths, strips non-deterministic timestamps, and enforces uniform formatting before injecting into conversation history.
* **Token & Quality Impact**: Ensures that identical intermediate states produce identical cache prefixes across repeated runs and downstream steps.
* **Implementation Surface**: `pkg/compactor/`, `pkg/hooks/output.go`.

#### H32: Synthetic Cache Warming / Keep-Alive Heartbeats
* **Type**: Visionary
* **Target Bottleneck**: LLM provider caches expiring during agent thinking or tool execution pauses (typically 5–10 minute TTLs).
* **Mechanism**: When a long tool execution or human pause is detected, the proxy sends a zero-completion-token keep-alive ping with the pinned prefix to keep the KV-cache warm in the provider's GPU memory.
* **Token & Quality Impact**: Prevents cold cache miss penalties on subsequent turns in long-running tasks.
* **Implementation Surface**: `pkg/proxy/`.

#### H33: Cross-Turn Append-Only Guarantees
* **Type**: Architectural
* **Target Bottleneck**: Agent harnesses that modify previous messages (e.g. rewriting message indices or injecting per-turn metadata into prior messages), which busts the entire KV cache.
* **Mechanism**: Intercept client requests at the proxy level. If the client modified historic messages, re-align them to match the cached prefix byte-for-byte while appending genuine diffs to the latest message.
* **Token & Quality Impact**: Restores cache hits even when using imperfect client harnesses.
* **Implementation Surface**: `pkg/proxy/`.

---

### Pillar 8: Advanced AST & Semantic Compaction

#### H34: Tree-Shaken Interface Slicing
* **Type**: Architectural
* **Target Bottleneck**: Skeletons including 50 unrelated functions from a large service file.
* **Mechanism**: Given a target symbol or query, perform reachability analysis across the AST. Return *only* the target function, its direct caller/callee signatures, and referenced types. Omit all unrelated sibling methods completely.
* **Token & Quality Impact**: Replaces whole-file skeletons with targeted AST slices, delivering 92–95% token reduction compared to raw files (vs 70% for whole-file skeletons).
* **Implementation Surface**: `pkg/ast/spans.go`, `pkg/context/`.

#### H35: Global Semantic Type Header Generation
* **Type**: Architectural
* **Target Bottleneck**: Agents exploring 5 files just to understand struct definitions and interfaces.
* **Mechanism**: On startup, parse all struct/interface/type definitions in the workspace into an ultra-compact virtual header (~300–500 tokens). Inject this header once into the system context.
* **Token & Quality Impact**: Gives the model instant global type awareness, eliminating multiple discovery file reads across the entire session.
* **Implementation Surface**: `pkg/ast/`, `pkg/context/`.

#### H36: AST-Aware Comment, Docstring, and License Stripping
* **Type**: Pragmatic
* **Target Bottleneck**: Large source files containing 30–50% boilerplate comments, legal headers, and repetitive docstrings.
* **Mechanism**: Use Tree-sitter grammars to automatically strip copyright headers, author tags, and verbose internal comments during file reads and skeletons, preserving only functional code and API docstrings.
* **Token & Quality Impact**: Saves 15–30% of source code tokens immediately on every read.
* **Implementation Surface**: `pkg/ast/`.

#### H37: Declaration Spans with Collapsible Usage Snippets
* **Type**: Architectural
* **Target Bottleneck**: Models needing to see how a function is called, forcing whole-file reads.
* **Mechanism**: When returning declaration spans, extract the top 2 call-sites across the codebase and inline them as 3-line usage snippets directly below the signature.
* **Token & Quality Impact**: Satisfies the model's need for usage examples in 6 lines of context without opening caller files.
* **Implementation Surface**: `pkg/context/impact.go`, `pkg/ast/spans.go`.

---

### Pillar 9: Local On-Device Intelligence (System 1 / Micro-Workers)

#### H38: On-Device CPU Parameter Extractor (GLiNER / Regex) for Zero-Turn Execution
* **Type**: Visionary
* **Target Bottleneck**: Asking the cloud model to extract file paths or query parameters across multiple turns.
* **Mechanism**: Run a fast, lightweight local extractor (GLiNER / ONNX or native regex) on the task prompt to automatically identify mentioned files, symbols, and test names, pre-populating tool arguments offline.
* **Token & Quality Impact**: Eliminates exploratory cloud turns; directly seeds the agent with exact arguments.
* **Implementation Surface**: `pkg/extractor/`, `bin/gliner_worker.py`.

#### H39: Local Pass/Fail Outcome Classifier
* **Type**: Pragmatic
* **Target Bottleneck**: Sending 2,000 tokens of raw build/test stdout to the cloud model just for it to say "The build succeeded".
* **Mechanism**: Run a deterministic local evaluator on command output. If the command exited with 0 and output matches known success patterns, return a compact 1-line signal: `[Success: All 24 tests passed in 1.2s]`.
* **Token & Quality Impact**: Prevents passing test outputs from polluting the context; saves 500–2,000 tokens per test run.
* **Implementation Surface**: `pkg/compactor/compactor.go`.

#### H40: Local BM25 / FTS5 Code Search Reranker
* **Type**: Pragmatic
* **Target Bottleneck**: `tzro probe` returning 10 candidate symbols, forcing the model to read all 10 signatures.
* **Mechanism**: Use SQLite FTS5 with BM25 ranking and lexical proximity scoring to filter probe candidates down to the top 2 highest-probability matches before returning to context.
* **Token & Quality Impact**: Cuts discovery output tokens from ~500 to <150 tokens per probe call.
* **Implementation Surface**: `pkg/search/`, `pkg/store/fts.go`.

#### H41: Pre-Edit AST Syntax & Lint Validator
* **Type**: Pragmatic
* **Target Bottleneck**: Cloud model producing code with missing brackets or import typos, failing tests, and taking 2 extra turns to fix syntax.
* **Mechanism**: Intercept file edits in the local hook. Parse with Tree-sitter. If syntax is invalid, reject the edit locally with the exact parse error before running the full test suite or returning to cloud.
* **Token & Quality Impact**: Catches dumb syntax errors in 5ms locally; prevents failed cloud iteration turns.
* **Implementation Surface**: `pkg/ast/`, `pkg/executor/tools.go`.

---

### Pillar 10: Radical & Assumption-Breaking Strategies

#### H42: Zero-Turn Solution Context Injection (Preflight Seeding)
* **Type**: Radical
* **Target Bottleneck**: The entire exploration phase (Turns 1–4) spent finding files and reading signatures.
* **Mechanism**: When the task prompt is received, tzro analyzes the prompt offline *before sending it to the cloud model*. It runs probe and impact analysis, collects the 2 most relevant declaration spans and the failing test trace, and injects them directly into the Turn 1 prompt as prepared evidence.
* **Token & Quality Impact**: Turns 10-turn tasks into 2-turn tasks ("Here is the problem and the code; now write the fix"). Slashes total task tokens by 50–70%.
* **Implementation Surface**: `pkg/benchmark/workflow/execute.go`, Preflight hook.

#### H43: Inverted Architecture: Agent as High-Level Policy, Tzro as Local Executor
* **Type**: Radical
* **Target Bottleneck**: Cloud LLM acting as a micro-manager issuing low-level bash commands (`cat`, `sed`, `grep`, `awk`).
* **Mechanism**: The cloud LLM is never given shell or file tools. It emits a high-level declarative directive (e.g. `policy: "Fix discount logic in CalculateTax for Enterprise tier, verify against service_test.go"`). Tzro's local runtime executes the search, tests, and patch synthesis locally, only returning to cloud if policy decisions are required.
* **Token & Quality Impact**: Cuts cloud model interaction by 80–90%, reducing token consumption to a fraction of baseline.
* **Implementation Surface**: `pkg/executor/engine.go`, `pkg/decision/`.

#### H44: Strict Unified Diff Patching (Banning Full-File Dumps)
* **Type**: Pragmatic / Bold
* **Target Bottleneck**: Models generating 300 lines of output to change 2 lines, burning output tokens and prompt tokens on future turns.
* **Mechanism**: Completely disallow full-file rewrite tools. Expose only a surgical patch tool (`apply_patch` or `replace_symbol`). If a model attempts a full-file write, convert it into an AST diff locally and return only the diff confirmation.
* **Token & Quality Impact**: Slashes output token generation by 70–90% during editing turns.
* **Implementation Surface**: `pkg/executor/tools.go`.

#### H45: Virtual Symbol Filesystem (Exposing Code via Virtual Paths)
* **Type**: Radical
* **Target Bottleneck**: Models having to learn new tzro tools (`skeleton`, `expand`, `probe`) instead of using standard tools like `cat` and `view`.
* **Mechanism**: Mount a virtual filesystem or path-interception layer where symbols are accessible as virtual files: e.g. `cat pkg/service.go:CalculateTax` or `cat pkg/service.go/types`. The agent reads exact functions with standard OS utilities.
* **Token & Quality Impact**: 100% adoption with zero training or skill prompts; achieves surgical sub-file reads using standard tools.
* **Implementation Surface**: `pkg/executor/shell_unix.go`.

#### H46: Multi-Model Dynamic Tiered Dispatch
* **Type**: Visionary
* **Target Bottleneck**: Using an expensive frontier model for trivial grep, syntax checking, and file reading turns.
* **Mechanism**: In the loopback proxy, inspect each turn. If the turn is purely exploratory (listing directories, inspecting logs, running a test), route the turn to an ultra-cheap, fast model (e.g. Gemini Flash / MiniMax). Route back to the frontier model when complex code synthesis is detected.
* **Token & Quality Impact**: Cuts monetary cost by 70–85% even if token volume remains constant.
* **Implementation Surface**: `pkg/proxy/`.

#### H47: Output Token Budget Caps with Graceful Yield
* **Type**: Pragmatic
* **Target Bottleneck**: Models generating runaway verbose explanations, ASCII diagrams, and duplicated code in their responses.
* **Mechanism**: Enforce strict system instructions and proxy output token caps (e.g., max 300 output tokens unless generating new code files). Reward terse, code-only responses.
* **Token & Quality Impact**: Slashes output tokens by 50% across all turns. Output tokens are typically billed at 3–4× the rate of input tokens.
* **Implementation Surface**: `pkg/hooks/instructions.go`, `pkg/proxy/`.

#### H48: Dual-Buffer Context Architecture (Public Semantic Tape vs Ephemeral Tool Buffer)
* **Type**: Radical
* **Target Bottleneck**: Mixing raw tool execution artifacts with high-level semantic intent in the same conversation array.
* **Mechanism**: Maintain two distinct context streams:
  1. *Public Semantic Tape*: Contains user instructions, architectural decisions, and final committed code diffs (preserved across turns).
  2. *Ephemeral Tool Buffer*: Contains raw command outputs, search iterations, and compiler errors (flushed completely once a milestone is verified).
* **Token & Quality Impact**: Keeps conversation history perpetually compact (~2,000–3,000 tokens) regardless of whether a task takes 5 turns or 50 turns.
* **Implementation Surface**: `pkg/proxy/`, `pkg/hooks/`.

---

## 3. Prioritization & Tiering Matrix

| Tier | Focus Areas | Key Hypotheses | Expected Token Impact | Complexity |
| :--- | :--- | :--- | :--- | :--- |
| **Tier 1: Immediate Wins (Pragmatic)** | CLI Ergonomics & Zero-Adoption Hooks | **H11** (Auto-Skeleton), **H12** (Auto-Tabular), **H16** (Param Normalization), **H01** (Lazy Schemas), **H13** (Log Compaction) | **35% – 50% reduction** on data & coding tasks | Low (< 1-2 days each) |
| **Tier 2: Quadratic Killers (Architectural)** | Context Pruning & Composite Tools | **H06** (Tool Result Tombstoning), **H07** (Dead-Read Eviction), **H21** (find_and_view), **H24** (data_query), **H30** (Prefix Lock) | **40% – 60% reduction** on multi-turn tasks | Medium (3-5 days) |
| **Tier 3: Game Changers (Visionary)** | Pipe DSL, Multi-Step Offload & AST Slicing | **H26** (Pipe DSL), **H17** (Rich Skeletons), **H34** (Tree-Shaken Slices), **H44** (Diff-Only Edits) | **50% – 70% reduction** | Medium-High |
| **Tier 4: Radical Paradigms (Assumption-Breaking)** | Control Inversion & Zero-Turn Seeding | **H42** (Zero-Turn Preflight), **H43** (Agent as Policy), **H45** (Virtual Symbol FS), **H48** (Dual-Buffer Context) | **75% – 90% reduction** | High (Architectural shift) |

---

## 4. Next Steps for Autonomous Execution Loop

To execute against this catalog using the autonomous `/goal` execution loop:
1. **Selection & Ordering**: Pick a candidate hypothesis (starting with Tier 1 pragmatic wins).
2. **Implementation Tracer**: Implement the change in an isolated branch/checkout.
3. **Automated Verification**:
   - Run race-detector unit suites (`go test -race ./...`).
   - Run the matched benchmark matrix (`tzro bench workflows --profiles baseline,standard`).
   - Evaluate against the release criteria: $\ge 33\%$ token savings in 3 matched repetitions, lower estimated cost, and 0 quality regressions.
4. **Autonomous Git Gate**: If passing, commit and update progress ledger; if regressing, record root-cause failure trace, revert, and advance to next hypothesis.

## 5. Hypotheses derived from campaign evidence

These entries extend the catalog as experiments expose new constraints. Predictions remain unmeasured unless a linked result establishes the specific claim.

#### H49: Evidence Delivery with Explicit Reuse Guidance
* **Type**: Pragmatic
* **Target Bottleneck**: E009 injected README evidence, but the model read the same file again.
* **Mechanism**: Keep evidence selection unchanged. Add a short, trusted wrapper that identifies the retrieved source and explains when another read is necessary. Compare this wrapper with evidence-only injection. Source contents remain untrusted data.
* **Token & Quality Impact**: Predict fewer duplicate reads without losing freshness checks. Reject if duplicate reads persist, total cost rises, or required evidence is missed.
* **Implementation Surface**: `pkg/hooks/pi_extension.ts`; E009 custom-message trace and a new matched ordinary-task trial.

#### H50: Immutable Data Identities with Explicit Freshness
* **Type**: Architectural
* **Target Bottleneck**: E010 exposed table aliases across distinct file tails. E011 fixed complete-input identity, but query results still represent an imported snapshot.
* **Mechanism**: Bind imported evidence to complete source content and expose its source fingerprint and snapshot semantics. Detect changed source files before presenting results as current. Preserve intentional queries against older snapshots.
* **Token & Quality Impact**: Predict fewer incorrect or redundant reimports. Full-input identity has local correctness evidence from E011; freshness behavior and economic benefit remain unmeasured.
* **Implementation Surface**: CLI ingest/query, tabular envelopes, source metadata, and automatic evidence hooks.

#### H51: Cache-Aware Timing of Context Reduction
* **Type**: Architectural
* **Target Bottleneck**: E012 removed 29.8% of repeated tool-text bytes offline. E013 proved delivery, but neither measured the cost of invalidating cached prefixes.
* **Mechanism**: Compare immediate aging with reduction deferred to an existing context-reset boundary. Preserve exact recovery in both conditions. Measure actual cached input, uncached input, recovery calls, and billed cost.
* **Token & Quality Impact**: Predict that fewer rewritten prefixes can outperform more aggressive byte removal. Reject if billed cost or task quality worsens despite smaller context.
* **Implementation Surface**: Pi context hooks, recovery storage, and matched provider accounting.

#### H52: Schema-Guided Recovery of Equivalent Tool Arguments
* **Type**: Pragmatic
* **Target Bottleneck**: E009 graph expansion failed because the model supplied `item` instead of `id`. E015 recovered fixed aliases locally.
* **Mechanism**: Define explicit equivalent argument names beside each tool contract. Normalize only unambiguous values before dispatch, and reject conflicts. Keep tool schemas and recovery rules synchronized. Never invent missing values.
* **Token & Quality Impact**: Predict fewer error-recovery decisions while preserving tool intent. Reject on ambiguous execution, changed values, or workflow overhead exceeding the saved retries.
* **Implementation Surface**: Executor tool contracts, native tool-call hooks, and ordinary malformed-call traces.

#### H53: Tool Selection Before the Agent Context Snapshot
* **Type**: Architectural
* **Target Bottleneck**: E002 found that Pi ignores within-task tool activation after copying its tool list. Pre-agent events run before that copy.
* **Mechanism**: Select supported tool groups during `before_agent_start`, using prompt and repository signals. Preserve normal native tools and an explicit fallback. Verify the first provider schema and actual execution before testing natural adoption.
* **Token & Quality Impact**: Predict lower unused-schema overhead without the within-task activation failure. Reject if required tools are unavailable or fallback turns erase the saving.
* **Implementation Surface**: Pi pre-agent hook, tool registration, and installed-client compatibility fixtures.

#### H54: Faithful SQL Column Names in Data Envelopes
* **Type**: Pragmatic
* **Target Bottleneck**: A local diagnostic found that ingest advertises `net-total` while SQLite stores `nettotal`. Querying the advertised quoted name returned 0 instead of 30.
* **Mechanism**: Use one column-name normalization contract for storage, envelopes, and executor metadata. Expose the exact queryable identifiers and preserve source-name mappings when names change. Reject ambiguous normalized names instead of silently choosing a column.
* **Token & Quality Impact**: Predict correct queries using advertised names and fewer schema-recovery calls. Correctness and economic effects need separate validation; no savings percentage is assumed.
* **Implementation Surface**: `pkg/store`, `pkg/compactor/tabular.go`, CLI ingest, executor data tools. Evidence: `.scratch/standard-savings/experiments/column-contract-diagnostic.json`.

#### H55: Preserve Duplicate SQL Result Columns or Fail Explicitly
* **Type**: Pragmatic
* **Target Bottleneck**: A local query diagnostic returned `2, 2` for `SELECT 1 AS value, 2 AS value`. Mapping results by column name silently overwrote the first value.
* **Mechanism**: Detect duplicate result labels before converting rows into maps. Return an explicit request for unique SQL aliases, or adopt a representation that preserves column positions. Test both approaches before selecting one.
* **Token & Quality Impact**: Predict fewer silently incorrect answers. An explicit error can add a recovery turn; correctness takes priority and economic effects remain unmeasured.
* **Implementation Surface**: `pkg/store.QuerySQL`, query output renderers, executor query metadata. Evidence: `.scratch/standard-savings/experiments/query-column-diagnostic.json`.

#### H56: Progressive Schemas at the Provider Boundary
* **Type**: Architectural
* **Target Bottleneck**: E002 could not activate a newly added tool inside Pi's copied agent context. H53 works only before that snapshot.
* **Mechanism**: Keep executable tools registered in the agent snapshot throughout the task. Use `before_provider_request` to hide optional schemas only in supported wire payloads. Reveal them after a relevant tool result. Unsupported payload shapes pass through unchanged. Test graph execution after disclosure in the same task.
* **Token & Quality Impact**: Predict that unused schemas can remain absent while later disclosed tools execute without another agent task. First-request savings, cache disruption, adoption, and total cost require separate measurement.
* **Implementation Surface**: Pi `before_provider_request` and `tool_result` hooks. Derived from E002 and E017; this mechanism has not yet been tested.

#### H57: Bounded Recovery from Textual Tool Requests
* **Type**: Pragmatic
* **Target Bottleneck**: E020's parent ended with `<needs_remote><tool-use ...>` text, so no native tool ran and the requested output file was missing.
* **Mechanism**: At a completed assistant turn, detect an entire response consisting of unsupported tool-request markup. Queue one corrective follow-up that directs the model to its registered native tools. Do not execute the textual request. Limit recovery to once per user task.
* **Token & Quality Impact**: Predict fewer failed tasks at the cost of one bounded recovery decision. Reject if the client exits before recovery, valid answers are interrupted, or repeated correction loops occur.
* **Implementation Surface**: Pi `turn_end`, `before_agent_start`, and follow-up message APIs. Evidence: E020 parent incident trace.

#### H58: Clear Standard Agent Role and Tool-Only Instructions
* **Type**: Pragmatic
* **Target Bottleneck**: Standard exposes descriptions of local decision workers and yielding even when workers are disabled. E020's textual yield is consistent with role confusion, but does not establish its cause.
* **Mechanism**: In Standard descriptions and installed guidance, describe only executable model-free operations. State that the host model performs reasoning and uses native tool calls. Retain worker guidance for Full. Compare unchanged prompts/tasks and preserve CLI access.
* **Token & Quality Impact**: Predict fewer unsupported yields and less irrelevant instruction overhead. Reject if native tool use or quality worsens, or if savings disappear in confirmation.
* **Implementation Surface**: Generated Pi tool descriptions and installed skill guidance. Derived from E020; causal role-confusion explanation remains untested.

#### H59: Pre-Execution Test Compaction Through Native Tool Hooks
* **Type**: Pragmatic
* **Target Bottleneck**: Output hooks see text after execution and may lack the process exit status. E024 confirms that additional native hook events can change the workflow without model-selected Tzro calls.
* **Mechanism**: In tool_call, wrap only explicitly supported standalone test commands with tzro compact --run. Preserve the command, timeout, working directory and failure status. Leave compound shell commands unchanged. Verify that input mutation reaches the actual native executor and that diagnostics remain recoverable.
* **Token & Quality Impact**: Predict automatic evidence-contract delivery without an extra model decision. Existing post-tool compaction may make byte savings redundant; reject if failure status, diagnostics or shell semantics change. Workflow economics remain unmeasured.
* **Implementation Surface**: Pi tool_call hooks, native bash execution, and tzro compact. Derived from E024 hook compatibility and the existing post-tool output boundary.

#### H60: Bounded Pre-Agent Log Evidence
* **Type**: Pragmatic
* **Target Bottleneck**: E020 ended with an unexecuted request to inspect incident.log. E022 confirmation spent recovery calls on an unsupported flag and a mistyped log path. E025 proved that pre-agent data evidence is naturally consumed, though its cost gain did not repeat.
* **Mechanism**: For one explicitly mentioned local log file, run the existing deterministic compactor before the first model request. Inject a bounded source-labelled packet only when it is materially smaller, retains a full-output recovery pointer, and preserves diagnostics. Skip oversized, unavailable or out-of-workspace files without changing normal tools.
* **Token & Quality Impact**: Predict fewer initial log-discovery calls without relying on model-selected tools. Reject if the packet loses request identifiers or application frames, hides truncation, reads outside the workspace, or adds more work than it saves.
* **Implementation Surface**: Pi before_agent_start, existing log compaction and artifact recovery. This is a log-only packet, distinct from H25 source-frame enrichment.

#### H61: Compact Repeated Background-Stack Summaries
* **Type**: Pragmatic
* **Target Bottleneck**: E027 reduced the representative incident log from 34912 bytes to an 8129-byte packet, still above its frozen 6000-byte injection cap. Repeated background-stack markers remain after standard-library frame elision.
* **Mechanism**: Compact consecutive equivalent background-only stack summaries into a bounded count and goroutine-identity representation. Preserve application frames, request boundaries, ordering and an exact original recovery pointer. Do not merge stacks containing distinct application evidence.
* **Token & Quality Impact**: Predict smaller recoverable packets and broader eligibility for bounded log injection. Reject if application evidence disappears, distinct incidents are conflated, recovery fails, or bytes remain above the declared packet target.
* **Implementation Surface**: Stack-trace elider and compaction evidence formatter. Derived from E027 representative-opportunity.json; unimplemented.

#### H62: Reject Stacked Statements in Read-Only Queries
* **Type**: Pragmatic
* **Target Bottleneck**: E030 found that SELECT 1; DELETE FROM example passes the read-only prefix check and deletes imported rows.
* **Mechanism**: Validate that query input contains exactly one SELECT statement before execution. Recognize quoted strings/identifiers and comments so embedded semicolons remain valid. Reject appended statements before any database effect; preserve ordinary SELECT behavior and explicit terminal semicolons.
* **Token & Quality Impact**: Predict elimination of silent mutation through the read-only query interface. Correctness only; fewer retries or dollar savings remain unmeasured. Does not claim safety for arbitrary side-effecting user-defined SQLite functions.
* **Implementation Surface**: pkg/store.QuerySQL shared by CLI and graph queries. Evidence: E030 readonly-diagnostic.json.

#### H63: Honor Explicit File Scope in Impact Analysis
* **Type**: Pragmatic
* **Target Bottleneck**: During this campaign, tzro impact pkg/compactor/compactor.go returned unrelated changed symbols. The CLI accepts positional arguments but ignores them, despite documented file-scoped usage.
* **Mechanism**: Resolve explicit files to their current declarations, then use the existing impact pipeline on those anchors. Preserve caller discovery outside the selected file. Reject conflicting scopes and invalid paths; retain existing no-argument git-diff behavior.
* **Token & Quality Impact**: Predict relevant pre-edit context and less unrelated packing. Reject if unrelated changed files become anchors, real callers disappear, or explicit unchanged files cannot be analyzed. Correctness and workflow savings need separate checks.
* **Implementation Surface**: cmd/tzro impact and pkg/context ImpactAnalyzer. Evidence: current CLI ignores args and always enters AnalyzeDiffScope without --symbol.

#### H64: Exclude Function-Local Declarations from File Impact Anchors
* **Type**: Pragmatic
* **Target Bottleneck**: E037 preparation found that explicit impact for read.go treats the function-local variable n as a workspace-wide anchor, yielding 1513 candidates and filling the pack with unrelated n references.
* **Mechanism**: For explicit Go file impact, exclude variable and type declarations contained inside function or method ranges. Keep package-level declarations and callable declarations. Preserve current diff behavior while testing this file-scope refinement independently.
* **Token & Quality Impact**: Predict fewer unrelated candidates without losing external callers or global variable consumers. Reject if local variables remain anchors or package declarations and real callers disappear. Local correctness does not establish workflow savings.
* **Implementation Surface**: pkg/context AnalyzeFiles and AST declaration range metadata. Derived from the E032 binary analyzing pkg/executor/read.go during E037 preparation.

#### H65: Preserve Workspace Policy During Probe Discovery
* **Type**: Pragmatic
* **Target Bottleneck**: Capability review for find-and-view found that pkg/probe reads workspace paths and indexes source without evaluating the workspace privacy policy. Both CLI and graph dispatch call this path directly.
* **Mechanism**: Apply logical and resolved-path policy before reading or indexing discovery candidates. Honor content policy before returning snippets or recovery handles, and fail explicitly for malformed policy. Keep allowed-source discovery useful.
* **Token & Quality Impact**: Predict policy-consistent discovery and recovery without introducing remote inference. Reject if blocked content reaches snippets or new recoverable blobs, or ordinary permitted matches disappear. This is a correctness hypothesis, not a savings claim.
* **Implementation Surface**: pkg/probe.Probe, shared CLI and graph callers. Static evidence in pkg/probe/probe.go, cmd/tzro/main.go and pkg/executor/tools.go; requires synthetic reproduction.

#### H66: Bind Probe Recovery Handles to the Matched Span
* **Type**: Pragmatic
* **Target Bottleneck**: Probe initially assigns the first skeleton hash in a file to every textual match. A symbol-index lookup can override it, but body-only text matches can retain an unrelated first-function handle.
* **Mechanism**: Resolve recovery handles from the declaration containing the matched source line. Return no body handle for unmatched top-level text rather than assigning an unrelated body. Keep source anchors and recovery content consistent.
* **Token & Quality Impact**: Predict fewer misleading expansion calls and correct evidence attribution. Reject if a match inside a later function expands the first function, or top-level text advertises an unrelated body. Savings and natural follow-up behavior remain unmeasured.
* **Implementation Surface**: pkg/probe.Probe and AST declaration-span extraction. Static evidence: skel.Hashes[0] assignment followed by an optional symbol-name search.

#### H67: Prefer Workspace-Relative Tool Paths
* **Type**: Pragmatic
* **Target Bottleneck**: E039 hook-only authentication trace duplicated part of a long absolute temporary-workspace prefix and received ENOENT. The target file existed under the current workspace, where a short relative path would have worked.
* **Mechanism**: State the current-workspace relative-path convention in ordinary tool guidance and present relative paths in discovery examples. Keep absolute paths supported and preserve workspace/path policy. Measure whether natural calls adopt the convention instead of copying long prefixes.
* **Token & Quality Impact**: Predict fewer path-copy errors and shorter tool arguments without fuzzy redirection. Reject if ordinary tasks do not adopt relative paths, error or recovery rates grow, or guidance overhead exceeds whole-task savings.
* **Implementation Surface**: Pi native tool guidance and ordinary CLI examples. Evidence: E039/B-tool-errors.json. Distinct from T22, which repairs a failed path after the call.

#### H68: Recoverable Log Fallback for Source-Skeleton Requests
* **Type**: Pragmatic
* **Target Bottleneck**: E039 control called tzro skeleton incident.log and received 34913 bytes. Explicit toolkit responses bypass the native post-tool compaction hook, so this unsupported-source fallback delivered the full log.
* **Mechanism**: For a log supplied to the skeleton interface, use the existing evidence compactor as a clearly labelled fallback with exact original recovery, or return a concise route to the correct compact-file operation. Keep supported source skeleton behavior unchanged and measure actual route adoption.
* **Token & Quality Impact**: Predict that choosing a source-oriented tool for a log no longer causes an oversized raw response. Reject if request IDs, cause or application frames disappear, exact recovery fails, supported source changes, or the extra routing step costs more than it saves.
* **Implementation Surface**: CLI and graph skeleton dispatch, compaction evidence contract, and tool descriptions. Evidence: E039/incident-adoption-partial.json; fallback semantics and native adoption need separate tests.

#### H69: Respect Lexical Scopes Across File-Impact Languages
* **Type**: Pragmatic
* **Target Bottleneck**: The user questioned E038's Go-only scope. Inspection shows Python, JavaScript/TypeScript/TSX and Rust still collect nested functions or types as workspace-wide anchors. The exact local-variable failure was Go-specific; analogous failures need reproduction.
* **Mechanism**: Apply callable-body boundaries to explicit-file declaration traversal for every currently supported impact language. Retain module/package declarations and class/impl methods. Keep diff extraction unchanged. Use language-specific AST node kinds under a shared scope rule.
* **Token & Quality Impact**: Predict fewer unrelated same-name references without losing true external callers or valid member anchors. Reject if any supported language retains function-local declarations, valid members disappear, or diff behavior changes. This confirms scope correctness, not workflow savings.
* **Implementation Surface**: pkg/context.extractFileDeclarationsFromAST and AnalyzeFiles. Provenance: E038 and user cross-language scope review; independent of its frozen Go contract. Does not add declaration support for new languages.

#### H70: Route TSX Declaration Anchors to the TypeScript Adapter
* **Type**: Pragmatic
* **Target Bottleneck**: E042 finds correct TSX declaration anchors but no true caller in either arm. The grammar emits language tsx while the adapter registry only registers typescript and javascript.
* **Mechanism**: Register the existing TypeScript reference adapter for the TSX grammar identifier. Preserve AST language provenance and reuse the same path, policy and syntax handling.
* **Token & Quality Impact**: Predict restored TSX caller discovery with the same scope guarantees as TypeScript. Reject if a true TSX caller remains missing, unrelated local-name callers leak, or other language routing regresses. Correctness only; economic impact remains unmeasured.
* **Implementation Surface**: pkg/context.NewAdapterRegistry. Evidence: E042 parent/candidate matrices and static registry inspection. Validate with the E042 scope candidate as parent, plus independent TSX/JSX alias fixtures.

#### H71: Recover Aged Tool Results from Native Session History
* **Type**: Architectural
* **Target Bottleneck**: E013 proves context-hook delivery, but duplicates tool results into temporary files without storage bounds or lifecycle management.
* **Mechanism**: Replace eligible old text results with pointers to exact results already present in the current native session branch. A narrow recovery tool verifies the call ID and content hash. Keep recent results, errors, images and unmatched history unchanged. Bound processing per request and create no additional persistent result store.
* **Token & Quality Impact**: Predict reduced repeated tool-result exposure without temporary-file growth. Reject on inexact recovery, cross-branch disclosure, lost session continuity, excessive processing, or unchanged exposure. Extra tool-schema cost and prefix-cache invalidation require separate natural economic measurement.
* **Implementation Surface**: pkg/hooks/pi_extension.ts context event and a native-history recovery tool. Provenance: E012 exposure replay, E013 recovery prototype, and installed Pi 0.74.2 sessionManager/getBranch documentation. This is an alternative to temporary-file quotas, not evidence of workflow savings.

#### H72: State the Execute Argument Contract in Native Tool Guidance
* **Type**: Pragmatic
* **Target Bottleneck**: E048 passed an awk shell command as the execute graph-file argument. The native dispatcher advertises execute but omits its argument form from the description.
* **Mechanism**: Describe execute as accepting a graph JSON file or stdin. Keep shell execution on the existing bash tool. Preserve validation and never reinterpret malformed graph arguments as shell commands.
* **Token & Quality Impact**: Predict fewer invalid execute calls and recovery decisions. Reject if the failure persists or added guidance costs more than the avoided errors. One observed failure establishes a candidate, not its frequency or economic value.
* **Implementation Surface**: pkg/hooks/pi_extension.ts native dispatcher description and ordinary CLI guidance. Provenance: E048 revenue_8192 trace and next35-paid-behavior-audit.json. Separate from E028 graph role-description cleanup.

#### H73: Distinguish Transformed Views from Physical Source Files
* **Type**: Pragmatic
* **Target Bottleneck**: E045 Standard billing searched service.go for a body-elision marker that existed only in the transformed tool response. The search failed and added a recovery step.
* **Mechanism**: Clarify in structural-view metadata that markers belong to the returned view and the physical file retains original source. Preserve exact hash expansion and explicit offset/limit reads.
* **Token & Quality Impact**: Predict fewer searches for virtual markers in physical files. Reject if models still make these searches, exact recovery regresses, or repeated metadata overhead exceeds the avoided work. Natural coverage remains uncertain.
* **Implementation Surface**: pkg/hooks/output.go structural-view header and matching toolkit response guidance. Provenance: E045-parent-screen Standard billing trace, audited during E050. No source-file mutation or benchmark-specific wording.

#### H74: Index C and C++ Callable Names through Declarator Nodes
* **Type**: Pragmatic
* **Target Bottleneck**: E047 recovers C/C++ bodies exactly, but neither parent nor candidate indexes the Target callable name. Their grammars expose callable names through declarators, while the current code reads a direct name field.
* **Mechanism**: Resolve callable names along the C/C++ declarator structure in both whole-file skeletons and declaration spans. Preserve qualified names, body identity and source anchors. Do not mistake parameter names or nested callback declarators for the enclosing callable.
* **Token & Quality Impact**: Predict correct named-symbol discovery and recovery across simple, pointer-returning and qualified C/C++ callables. Reject if names are wrong, bodies or anchors change, or existing languages regress. Correctness evidence would not establish workflow savings.
* **Implementation Surface**: pkg/ast/skeleton.go and pkg/ast/span.go shared callable-name extraction. Provenance: E047 ten-language matrix and subsequent source inspection. This does not add C/C++ impact reference adapters.

#### H75: Omit Nontext Files from Probe Results
* **Type**: Pragmatic
* **Target Bottleneck**: E052's CLI fixture placed a SQLite store inside the searched workspace. Probe searched its binary contents and emitted invalid UTF-8, causing the result consumer to fail.
* **Mechanism**: Skip permitted files containing NUL bytes or invalid UTF-8 before text matching and AST extraction. Report a skipped-file count without exposing their contents. Preserve valid Unicode text, source anchors, recovery and existing privacy checks.
* **Token & Quality Impact**: Predict valid UTF-8 output and no matches from the declared nontext inputs, with unchanged valid-text discovery. Reject if binary bytes leak, supported text disappears, source hashes change, or policy behavior regresses. This is an input-quality contract, not a complete MIME detector or encoding converter.
* **Implementation Surface**: pkg/probe.Probe shared CLI/graph path and ProbeReport omission metadata. Provenance: E052 initial-cli-store-in-workspace and fixture-note.md. No workflow savings claim before natural measurement.

#### H76: Recover Terminal Fenced Tool Requests without Executing Their Text
* **Type**: Pragmatic
* **Target Bottleneck**: E054's fresh Standard control stopped the rate-limiter task after returning a tool_call code fence containing tool.read(...) as plain assistant text. No read or edit executed, and the task failed. E024 only recognizes XML-shaped requests.
* **Mechanism**: Extend bounded turn-end recovery to an entire terminal tool_call fence whose body has a tool-request shape. Ask the model to use a native call if it intended execution; never parse or execute the text as a command. Limit recovery to once per task and distinguish ordinary explanations, examples, native calls, and explicit requests to produce tool-call text.
* **Token & Quality Impact**: Predict recovery of this observed premature stop at the cost of one extra decision. Reject on repeated recovery, execution of textual content, legitimate-output false positives, or failure to complete actual-client recovery. Natural frequency and economic benefit require later measurement.
* **Implementation Surface**: pkg/hooks/pi_extension.ts turn_end/before_agent_start lifecycle and actual Pi fixture. Provenance: E054 parent-rate-diagnostics.json; parent6/7 due to non-executed terminal markup. Preserve E024 XML evidence and exclusions rather than silently expanding its old contract.

#### H77: Authorize Pre-Agent Log Evidence through the Shared Workspace Policy
* **Type**: Pragmatic
* **Target Bottleneck**: E055 found that the retained E039 pre-agent hook injects packets from denied paths, blocked content, and malformed-policy workspaces. Workspace containment alone does not enforce the product privacy contract.
* **Mechanism**: Move automatic log-source authorization into a bounded shared local helper that checks logical and resolved paths, loads policy fail-closed, checks content before storage or compaction, and returns evidence only when permitted and exactly recoverable. Keep packet, file-size, timeout, and no-op limits.
* **Token & Quality Impact**: Predict zero forbidden injections while preserving the allowed packet and recovery. Reject on denied reads/storage/injection, path-alias bypass, malformed-policy fallback, or changed allowed-log evidence. Correctness is a prerequisite for economic confirmation, not a savings result.
* **Implementation Surface**: A shared Go source-policy helper and pkg/hooks/pi_extension.ts pre-agent adapter. Provenance: E055 boundaries.json; no paid confirmation occurred. Do not add independent skeleton-log fallback behavior to this treatment.
