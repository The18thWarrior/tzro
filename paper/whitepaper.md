# Infrastructure Over Parameters: Engineering Patterns for Reliable Small-Model Agents

**Jordan Paul**
*August 2026*
*https://github.com/The18thWarrior/tzro*

---

## Abstract

Large language model agents typically rely on frontier models for all inference calls, resulting in high costs, cloud dependency, and opacity. This paper presents seven engineering patterns—discovered through 70+ benchmark iterations—that make a 4-billion-parameter local model a reliable execution engine for agentic workflows. The key insight: most agentic work is *routing* (tool selection, parameter extraction, navigation), not *reasoning* (code generation, architectural judgment, novel synthesis). When an orchestration harness decomposes work into properly-scoped routing decisions, a 4B model handles the execution pipeline reliably. The narrow residual cases requiring frontier reasoning are handled through surgical cloud escalation.

We report results from TZRO, an open-source durable execution engine that operationalizes these patterns. The system achieves 4.53/5.0 average quality on a 25-task benchmark suite while routing 86% of inference calls locally, at a total cost of $0.10/task. Each benchmark task executes against an isolated, empty database—no learned state carries between tasks. The entire improvement arc is attributable to infrastructure code changes, not accumulated system learning.

The patterns are presented with a formal structure—Problem, Solution, Why Alternatives Fail, Prerequisites, Production Data—to enable adoption independent of any specific framework.

---

## Table of Contents

1. [The Problem](#1-the-problem)
2. [The Thesis: Infrastructure Over Parameters](#2-the-thesis-infrastructure-over-parameters)
3. [Pillar I: Execution Architecture](#pillar-i-execution-architecture)
   - [Pattern 1: Two-Pass Thought Chain](#pattern-1-two-pass-thought-chain)
   - [Pattern 2: Phase Runners](#pattern-2-phase-runners)
   - [Pattern 3: Task Sizing as a Design Principle](#pattern-3-task-sizing-as-a-design-principle)
4. [Pillar II: Context Engineering](#pillar-ii-context-engineering)
   - [Pattern 4: Deterministic Floors with Optional Refinement](#pattern-4-deterministic-floors-with-optional-refinement)
   - [Pattern 5: Content-Aware Compaction](#pattern-5-content-aware-compaction)
   - [Pattern 6: Scoped Micro-Skill Injection](#pattern-6-scoped-micro-skill-injection)
5. [Pillar III: Quality Assurance](#pillar-iii-quality-assurance)
   - [Pattern 7: Verified Task Execution](#pattern-7-verified-task-execution)
6. [Evidence](#6-evidence)
7. [Limitations](#7-limitations)
8. [Lessons Learned](#8-lessons-learned)

---

## 1. The Problem

The dominant paradigm for LLM-powered agents treats frontier models as the default compute substrate. Every tool selection, parameter extraction, and synthesis step is routed to a cloud-hosted model with hundreds of billions of parameters. This approach produces capable agents but at significant cost—both financial ($0.01–0.03 per inference call, hundreds of calls per complex task) and architectural (cloud dependency, latency, opacity, privacy constraints).

Small language models (SLMs) with 1–8 billion parameters are now competitive on many of the subtasks that compose agentic workflows: text classification, entity extraction, structured output generation, and short-form synthesis. However, deploying SLMs as general-purpose agents without architectural support produces unreliable results. The failure modes are well-characterized:

- **Attention capacity**: SLMs cannot sustain coherent generation beyond ~300–400 tokens of high-quality output. After step 8–10 of an exploration loop, the model loses its plan.
- **World knowledge**: SLMs lack the breadth needed for novel domain reasoning, unfamiliar APIs, and complex architectural judgment.
- **Instruction following**: Complex multi-constraint prompts that combine reasoning, structuring, and tool selection in a single pass fail at unacceptable rates (~90% on structured action extraction).
- **Output variance**: The same model, same framework, same task can produce 5.0-quality output on one run and 1.0 on the next. The capability exists in the weights; the problem is variance with no verification mechanism.

The conventional response is to scale up—use a bigger model. This paper argues for a different path: *decompose properly*.

### What This Paper Is Not

This paper is not a peer-reviewed research paper with controlled experiments and statistical significance tests. It is not a product pitch. It is not a literature survey.

It is an engineering deep-dive: seven design patterns discovered through building and iterating on a real system over months, supported by production data, presented with honest limitations. The contribution is architectural—patterns that practitioners can evaluate, adapt, and adopt.

---

## 2. The Thesis: Infrastructure Over Parameters

### The Core Claim

Above a baseline capability threshold—tool-use fine-tuning, structured output support, basic instruction-following reliability—infrastructure improvements dominate parameter scaling for agentic reliability.

This is a strong claim, so let us be precise about what it means and what it does not mean.

It means: the quality improvement from 2.64 to 4.53 on our 25-task benchmark was achieved with *zero model changes*. The same 4B GGUF weights were used for every run. Every improvement came from infrastructure—better context management, structured execution phases, binding splices, validation gates, and verification pipelines.

It does not mean: you can take any arbitrary 4B model and achieve these results. The baseline model (Agents-A1-4B, a fine-tune of Qwen-3.5 4B optimized for agentic tool-use) already meets a capability threshold. Infrastructure raises the ceiling; the model must be in the right ballpark.

### Routing vs. Reasoning

The distinction that makes this work is between *routing* and *reasoning*:

**Routing** is a classification problem. Given accumulated context, tool schemas, and a task goal, which tool should be called? What parameters should be extracted? Which file should be read next? The correct action can be determined from the available context without requiring world knowledge or complex multi-constraint reasoning. A 4B model handles these decisions reliably.

**Reasoning** is a generation problem. What code should be written? Is this synthesis factually complete? What architecture should this system use? These decisions require synthesis, judgment, or knowledge beyond the immediate context. They benefit from frontier model capacity.

Analysis of our benchmark data confirms this split empirically: 86% of inference calls (412 of 479) were handled locally. The 67 cloud calls concentrated in three narrow categories: task-level planning, terminal output verification, and parameter extraction failures.

The analogy is staffing. You assign an intern properly-scoped tasks, not entire features. The intern can execute reliably when each unit of work requires routing rather than open-ended reasoning. The orchestration harness bears the same responsibility for SLMs.

### System Overview: TZRO

The patterns in this paper were developed in the context of TZRO, an open-source durable execution engine. TZRO compiles natural language prompts into checkpointed directed acyclic graph (DAG) workflows and executes them primarily on a 4B local model, escalating to frontier models only when necessary.

The system architecture has five key components:

1. **Durable DAG Compilation.** The Kahn Compiler takes an Abstract Graph (nodes and edges with sequential dependencies) and produces an Execution DAG through topological sorting. Nodes with no mutual dependencies are grouped into parallel execution layers. The compilation is deterministic—the same graph always produces the same DAG—which is essential for debugging.

2. **Plan Template Registry.** For routine tasks, the local model does not plan from scratch. Instead, a GBNF-constrained classification selects one of seven structural templates (explore-only, docgen, research, data-analysis, multi-probe-synthesis, codegen, action-chain), and the model mutates the template to fit the specific task. This reduces the cognitive burden from "design and serialize" to "edit and serialize."

3. **Durable Checkpointing.** All state is persisted to SQLite at node boundaries. Within Probe Nodes, checkpointing operates at finer granularity—each thought step is committed individually. If the process crashes mid-task, restarting picks up from the last completed checkpoint.

4. **Node Strategy Architecture.** The executor has zero hardcoded knowledge of node types. All behavior lives in a Strategy Registry where each strategy declares an execution method, compilation rules, and a context role. Built-in strategies include `probe`, `analyze`, `recall`, `synthesis`, `semantic_validator`, `branch`, and `action`. Custom strategies can be installed without modifying the core.

5. **Background Agents.** The Observer Agent fires on task completion telemetry, synthesizing memories and extracting micro-skills from execution trajectories. The Sentinel Agent runs on a periodic heartbeat, correlating activity patterns against the knowledge graph to generate retrieval-grounded alerts. Both operate under a 5-level Proactivity Ladder that constrains autonomous action.

---

## Pillar I: Execution Architecture

---

### Pattern 1: Two-Pass Thought Chain

#### Problem

Agentic exploration requires a model to do two things simultaneously: *reason* about what to do next (free-text deliberation) and *structure* the result into a machine-readable action (tool name, parameters, JSON format). When a 4B model attempts both in a single inference pass, it fails approximately 90% of the time on structured action extraction.

The failure mode is specific: the model produces coherent reasoning but fails to emit the required `<ACTION>` tags or valid JSON. A rescue fallback path—re-running inference with a truncated 1500-character context window—recovers some cases but loses critical information from the discarded context.

#### Solution

Each step of the exploration loop executes three inference passes, each targeting a specific model:

**Pass 1: Worker (unconstrained reasoning).** The 4B Worker model receives the probe goal, semantically retrieved prior thoughts (top-K via embedding cosine similarity), the current step's task, and the previous tool output. It produces free-text reasoning. Critically, it does *not* see the full exploration history—only the latest tool output and retrieved thoughts—which bounds context pressure regardless of exploration depth.

**Pass 2: Router (GBNF-constrained extraction).** A 1B Router model extracts a structured action from the Worker's reasoning using a GBNF grammar that constrains output to: `{action: "tool_call" | "synthesize", tool: string, arguments: object}`. This pass *always runs*—it is the designed extraction path, not a fallback.

**Pass 3: Synthesis Validation Gate.** When the Router signals `synthesize`, a third pass sends the Worker a validation prompt containing the current step position, successful tool call count, and unused tools. The Worker returns a structured judgment: `{ready: bool, reason: string, additionalSteps: int}`. If the Worker returns `ready: false`, synthesis is rejected (up to 2 rejections), the step budget is extended, and exploration continues.

This gate was introduced because the 1B Router consistently signaled premature synthesis—as early as step 8 of 20—because it cannot judge information completeness. The Worker's larger context window and reasoning capacity make it a more reliable arbiter.

**Durability.** Each thought step is committed to SQLite immediately. Rolling compaction merges every 3 thoughts into a running text summary, serving both as a context-efficient history representation and a crash-recovery checkpoint.

#### Why Alternatives Fail

- **Single-pass (reason + structure together):** ~90% failure rate on action tag emission. The model's attention is split between two incompatible objectives: producing coherent deliberation and conforming to a strict output grammar.
- **Rescue fallback with truncated context:** Recovers some failures but discards context, leading to worse decisions downstream.
- **Constrained-only generation:** Applying GBNF grammar during the reasoning phase degrades the quality of the reasoning itself—the grammar constraints interfere with natural language generation.

#### Prerequisites

Any multi-model or multi-pass inference setup. The key insight is that reasoning and structuring are distinct cognitive tasks that should not share an inference pass. This applies to any model pair where a larger model reasons and a smaller (or same) model structures, regardless of the underlying framework.

#### Production Data

- Near-100% extraction reliability (replacing ~90% failure rate)
- Synthesis Validation Gate catches premature termination at step 8/20, extending exploration by 3–5 additional steps in typical cases
- Rolling compaction reduces context size by ~3× per 3-step window while preserving crash-recovery capability

---

### Pattern 2: Phase Runners

#### Problem

Multi-step exploration tasks (codebase analysis, web research, data analysis) require the model to navigate through distinct phases of work. A flat exploration loop—the standard ReAct pattern of "think → act → observe → repeat"—hits an attention ceiling at step 8–10 for a 4B model. After this point, the model loses track of its exploration plan, begins repeating tool calls, or enters futile loops. Quality ceiling: approximately 2.5/5.0 on research tasks under the flat loop.

The root cause is that each new step adds to the accumulated context. By step 10, the model is making decisions against a context that exceeds its effective attention window, and earlier decisions (the exploration plan) get pushed out of high-attention positions.

#### Solution

The Phase Runner is a state machine that replaces the flat exploration loop with a structured pipeline of *Phases*. Each Phase declares:

- **Scoped tools.** A Phase restricts the available tool set. The Search phase of a research pipeline only exposes `web_search`; the Deep-Read phase only exposes `web_browse`. This prevents premature synthesis and tool misuse.
- **Step budget.** Per-phase budgets (typically 2–8 steps) enforce deterministic termination at each stage, replacing the single global 20-step budget.
- **Model target.** Per-phase routing to the Worker or Router model, allowing lightweight phases to use the faster 1B model.
- **Recovery strategy.** Per-phase `OnExhaustion` behavior: `retry`, `skip`, `fail`, or `backtrack` (re-enter an earlier phase with accumulated context).

Three concrete pipelines are implemented via closures that wire domain-specific hooks:

1. **Probe (codebase exploration):** Orient → Discover → Deep-Read → Synthesize. Orient identifies project structure, Discover navigates to key files, Deep-Read extracts details, Synthesize produces terminal output.

2. **Analyze (data analysis):** Schema-Orient → Query-Dev → Compute → Synthesize. Structural gates enforce ordering: `introspect_cache` must be called before Query-Dev; at least 2 analytical queries must execute before Compute concludes.

3. **Research (web research):** Search → Rank → Deep-Read → Cross-Ref → Synthesize. The 5-phase structure forces disciplined source evaluation rather than single-source synthesis.

**Tool hooks** keep the Phase Runner core generic. `ToolFixup` repairs arguments before dispatch (e.g., extracting SQL from reasoning text when the model writes a query in prose). `ToolPostProcess` tracks state after dispatch (e.g., marking URLs as visited, capturing analytical evidence). These hooks are wired per-node-type via builder closures.

**Substrate Mode Classification** provides a fast pre-routing optimization before the Phase Runner even starts. A keyword classifier routes probes to one of four modes:

- **Overview** (high-level summaries): Resolved via Directory Manifest + single-shot Direct Synthesis. No Thought Chain, no tool calls.
- **Focused** (specific components): Call Graph Index → entry-point traversal → Graph-Driven Context.
- **Aggregate** (content across many files): MapReduceSynthesis—chunk, summarize in parallel, reduce.
- **Unknown** (no keyword match): Falls through to standard Phase Runner. The classifier never blocks a probe.

#### Why Alternatives Fail

- **Flat Thought Chain loop:** Attention degradation after step 8–10. The model's effective planning horizon shrinks as context grows.
- **Scatter Nodes (runtime DAG expansion):** Violates the static DAG invariant, making the executor non-deterministic and non-debuggable.
- **Separate ReAct agent loop:** Creates a second execution mode that absorbs all work over time, causing the DAG mode to atrophy.

The Phase Runner avoids all three by containing autonomy *inside* a DAG node. From the parent DAG's perspective, a Probe Node is opaque: one input (a goal), one output (a synthesis). The parent DAG stays static, acyclic, and checkpointable.

#### Prerequisites

A DAG or node-level execution model where individual exploration nodes can be internally structured. The Phase Runner requires containment of exploration *inside* a deterministic node—it is not directly applicable to flat ReAct loops. However, the underlying principle—*scope tools and budgets per-phase to keep the model within its effective attention window*—applies to any system that can segment execution into stages.

#### Production Data

- Web research quality: 2.5 → 3.7 with Phase Runner (19 dedicated runs)
- Documentation generation: 1.00 → 4.75 in a single day once Substrate Mode Classification correctly routed between Direct Synthesis and MapReduceSynthesis
- Phase Manifests (per-phase step counts, tool calls, backtracks) provide full observability for debugging

---

### Pattern 3: Task Sizing as a Design Principle

#### Problem

Many apparent "limitations" of 4B models are actually failures of task decomposition by the orchestration harness. When the harness asks a SLM to simultaneously plan a workflow, serialize it as JSON, and validate the structure—a task that combines design and serialization—the model fails. When the harness asks a SLM to generate 500+ tokens of coherent output in a single pass—exceeding the model's effective attention window—the model fails. These are not capability gaps. They are decomposition failures.

#### Solution

Two complementary routing mechanisms decompose work along the routing/reasoning boundary:

**Complexity Tier (task-level routing).** At task intake, the local model classifies each prompt into one of three tiers:

- **T0 Direct:** Conversational Q&A requiring world knowledge or low latency. Routed directly to the cloud model. No DAG compilation.
- **T1 Planned:** Structured multi-step work. The local model selects a Plan Template via GBNF-constrained classification, mutates it, and executes all nodes locally.
- **T2 Supervised:** Complex architectural judgment. The cloud model generates the Abstract Graph from scratch; the Kahn Compiler compiles it; the local model executes the resulting nodes.

The key architectural decision: Complexity Tier routing is *non-exclusive*. It determines who *plans*, not who *executes*. T2 tasks use the cloud for planning but the local model for execution, keeping per-node inference costs at zero while leveraging frontier reasoning for graph design.

A complexity gate detects prompts that *sound* conversational but require multi-tool orchestration (e.g., "can you explain how the cache works?" for a codebase with a complex cache implementation). When classification resolves to T1/T2 despite a conversational surface form, the system promotes the prompt to a task.

**Confidence Tier (per-node routing).** Within a task, each action node faces a second routing decision. Before GBNF-constrained parameter extraction, the local model runs a self-assessment: can it extract the required parameters from the accumulated context and tool schema?

- **Sufficient:** Proceed locally.
- **Insufficient:** Escalate to the cloud model. If the cloud succeeds, extract a *Corrective Micro-Skill* from the diff between the failed local output and the successful cloud output.

The Confidence Tier is deliberately scoped to *parameter extraction only*. Terminal output quality is owned by Verified Task Execution (Pattern 7). This separation prevents the pre-flight from becoming a general-purpose quality gate, which would route too many nodes to the cloud.

A sticky escalation threshold (default: 3 consecutive `insufficient` results) forces cloud routing for the remainder of the task, preventing repeated local failures.

**Plan Template Registry** is a concrete example of proper task sizing for planning. Rather than asking the local model to design a DAG from scratch (which combines graph design and JSON serialization—two tasks that exceed its capacity when done simultaneously), the system provides a structurally sound template and asks the model to *edit* it. The model has full mutation authority—it can add, remove, or modify nodes, edges, tools, and configuration—but starts from a scaffold rather than a blank page.

**Corrective Micro-Skills** close the self-improvement loop. When the local model claims sufficient confidence but the tool call fails, the node surgically escalates to the cloud. If the cloud succeeds, the diff is extracted as an anti-pattern SOP: a structured Markdown document describing the specific failure pattern and its correction. These skills are persisted and injected on future invocations matching the same trigger. This design was chosen over historical success-rate injection, where per-tool success/failure ratios would bias routing—a blunt instrument that over-penalizes high-frequency tools for specific failure patterns.

#### Why Alternatives Fail

- **Single-tier routing (task-level only):** Cannot distinguish between a task that needs cloud planning and a node that needs cloud extraction. Over-routes to the cloud.
- **Success-rate routing:** A high-frequency tool with one specific failure pattern gets over-penalized across all uses, contradicting the goal of maximizing local execution.
- **No routing (all local):** The 4B model cannot plan complex multi-step workflows from scratch or handle genuinely novel API integrations.
- **No routing (all cloud):** Eliminates the cost and privacy benefits.

#### Prerequisites

Any system with a concept of task decomposition and a mechanism for separating planning from execution. The two-tier routing requires both a task-level classifier and a per-node pre-flight check. The Corrective Micro-Skill loop requires a mechanism for persisting and retrieving failure patterns. The sizing principle itself—scope each unit to routing, not reasoning—applies universally.

#### Production Data

- 86% of inference calls handled locally (412 of 479 total in Run 28)
- 67 cloud inference calls concentrated in: task planning (T2), terminal verification (VTE), parameter extraction failures
- Average task requires 2.7 cloud calls and 16.5 local calls
- Cloud-to-local inference ratio: 1:6.1
- Cost: $2.57 total for 25 tasks ($0.10/task average), with 1.66M local tokens at zero marginal cost

---

## Pillar II: Context Engineering

---

### Pattern 4: Deterministic Floors with Optional Refinement

#### Problem

Multi-step exploration generates large volumes of intermediate data—tool outputs, reasoning traces, extracted facts. When it comes time to synthesize a final output, the model must process this history. One-shot processing works for short explorations (5–8 steps), but at scale (15+ steps, 69K–99K characters), the model hits a *Synthesis Cliff*: output becomes incoherent or truncated.

The original approach—a Recall Node that attempted to process raw Thought Chain history in a single inference call—worked at small scale but failed catastrophically as probes grew deeper. The model simply could not hold that much context in its effective attention window.

#### Solution

The Recall Loop Inversion redesigns the synthesis contract from "agentic discovery" to "deterministic floor + optional refinement":

**Step 1: Deterministic compaction** builds a baseline `refinedContext` from all upstream Thought Chain steps before any agentic processing begins. Each step's tool output is classified and compacted using type-appropriate strategies:

- Code outputs → deterministic Code Skeleton extraction (see Pattern 5)
- Tabular data → schema-preserving sample rows
- Web and text content → LLM-driven fact extraction via the 1B Router

**Step 2: Refinement Pass** (the agentic loop) receives the pre-built baseline and can selectively fetch full details for steps where compaction lost important information, adding facts via an `update_refined_context` tool. In 89% of observed executions, the model signals immediate readiness—the deterministic baseline is sufficient. In the remaining 11%, the model refines the baseline additively.

**Step 3: Terminal synthesis** produces the final output from the `refinedContext`, the original goal, and a **Symbol Index**—a deterministic inventory of all public symbols extracted via tree-sitter AST parsing during the Probe's tool calls.

**Symbol Anchor Check.** A post-synthesis verification step diffs the symbols referenced in the synthesis against the Symbol Index. If more than 20% of referenced symbols are unanchored (not found in the index, excluding qualified external references like `context.Context`), a targeted correction pass is triggered. This mechanism was introduced after documentation benchmarks revealed that 67% of type names in the synthesis were hallucinated—a defect masked by LLM-as-judge scoring, which rewarded structural formatting without verifying factual accuracy.

The inter-rater gap was revealing: the cloud judge scored the hallucinated output 4.25/5.0; cross-verification by the local 4B model scored 2.65/5.0—a 38% inflation gap. The Symbol Anchor Check catches this category of error deterministically, without relying on either judge.

#### Why Alternatives Fail

- **One-shot synthesis:** Hits the Synthesis Cliff at 15+ steps. Context exceeds the model's effective attention window.
- **Full agentic discovery:** The model must decide what's important from scratch—this produces worse results than the deterministic baseline. Counterintuitively, more autonomy leads to lower quality.
- **Map-Reduce:** Works for homogeneous content (all text, all code) but fails on the heterogeneous outputs typical of agentic exploration.

#### Prerequisites

Upstream execution that produces structured, typed outputs—tool call results with content-type metadata. Any system where exploration generates intermediate artifacts that must be synthesized into a final output. The deterministic compaction step requires content-type detection; the refinement pass requires a mechanism for the model to selectively access full details.

#### Production Data

- 89% immediate-ready rate (deterministic baseline is sufficient)
- 11% additive refinement cases
- Symbol Anchor Check catches >20% hallucinated references in documentation tasks
- Inter-rater gap: 4.25 (cloud judge) vs. 2.65 (local cross-verification) on hallucinated output

---

### Pattern 5: Content-Aware Compaction

#### Problem

LLM context windows are finite, and attention quality degrades with length. Tool outputs are heterogeneous—JSON API responses, HTML web pages, source code files, tabular query results, free-text reasoning. Treating them uniformly (e.g., truncating all outputs at 2000 characters) wastes context capacity on low-information content while cutting off high-information content.

A 50-line JSON API response with deeply nested metadata and a 50-line Go function with inline documentation carry fundamentally different information densities. Uniform truncation ignores this.

#### Solution

Raw tool outputs pass through a 5-layer preprocessing pipeline before entering the Structured Compactor:

1. **JSON flattening** — Removes nested structure that wastes tokens
2. **HTML stripping** — Extracts text content from markup
3. **Base64 removal** — Strips binary-encoded data
4. **Whitespace normalization** — Eliminates redundant spacing
5. **Length capping** — Hard upper bound before classification

The Structured Compactor then classifies each output segment and applies type-appropriate strategies:

**Code** (`SegmentCode`): Deterministic Code Skeleton extraction—function signatures, type declarations, and comments are preserved; function bodies are replaced with fingerprints (`// [body: 42 lines, calls: foo(), bar()]`). Code is *never* LLM-compressed. The rationale: well-written code carries extensive inline comments and doc comments that explain functionality. The signatures and comments preserve understanding without the raw logic.

**Reasoning text** (`SegmentText`, from model output): The 1B Router extracts key conclusions per 500-character chunk, preserving the model's *decisions* while stripping its *deliberation*.

**Web/text content**: Fact extraction via the Router ("Extract all factual claims, statistics, names, comparisons, and URLs as a bulleted list"), achieving approximately 4:1 compression while preserving information density.

**Tabular data** (`SegmentTabular`): Schema-preserving sample rows for the context envelope, plus materialization into the **SQL Cache Store**—ephemeral SQLite tables that enable downstream Analyze Nodes to run actual SQL queries against the data rather than parsing raw text.

**Accumulated Context Budgeting.** A budgeting system controls total context size through three mechanisms: per-node-type weight budgets (Probe output receives more budget than Action output), node count windowing (only the N most recent nodes of each type are included), and content-aware per-node truncation via the Structured Compactor.

**Generation Guard.** A multi-tier degeneration detection system monitors inference output for quality collapse: character-level collapse (repeated characters/tokens), block-level repetition (n-gram analysis with length-scaled thresholds), and compression-ratio analysis (zlib ratio indicating low-information content). Thresholds are content-mode-aware—code outputs tolerate more structural repetition than prose.

#### Proactive Binding Splice

A complementary technique that approaches context engineering from the opposite direction—not reshaping what the model sees, but *removing* what the model should not decide.

Dynamic bindings (`{{nodes.probe_1.output.summary}}`) are resolved through a 5-tier Response Resolver cascade. The **Proactive Binding Splice** strips deterministically-resolved values from the tool schema before inference and splices them back after inference. The model never sees or generates these parameters—it cannot get them wrong.

This mechanism eliminated 57% of GBNF parameter failures in benchmark analysis by removing the model from the path entirely for known values. The principle: if the answer is already determined by the execution graph, do not ask the model to generate it.

#### Why Alternatives Fail

- **Uniform truncation:** Loses important information from dense content and wastes budget on sparse content.
- **LLM compression of code:** Destroys the inline documentation and comments that carry understanding. Models strip doc comments, rename variables, and "simplify" signatures in ways that lose critical context.
- **Uniform chunking (fixed-size):** Ignores information density differences. A 500-character chunk of tabular data carries far less unique information than a 500-character chunk of code comments.
- **No compaction (full context):** Exceeds the model's effective attention window, producing the Synthesis Cliff (Pattern 4).

#### Prerequisites

Content-type detection for tool outputs. A compaction pipeline that runs before context assembly. The strategies described here are general-purpose—Code Skeleton extraction requires tree-sitter or equivalent AST parsing; fact extraction requires any LLM (even a 1B model). Applicable to any system that accumulates context across multiple tool calls.

#### Production Data

- Code Skeleton: preserves signatures + comments + call fingerprints, dropping raw logic
- Fact extraction: ~4:1 compression ratio on web content
- Proactive Binding Splice: 57% reduction in GBNF parameter failures
- Generation Guard catches degenerate output before it reaches downstream nodes

---

### Pattern 6: Scoped Micro-Skill Injection

#### Problem

Agent frameworks typically inject all available skills, instructions, and tool documentation into every inference call. In a ReAct loop, the model sees the full skill set at every step regardless of relevance. For a 4B model with an effective attention window of approximately 300–400 tokens of high-quality generation, this is catastrophic.

Consider a task with a 5-node DAG and 3 available skills. Under global injection, each node receives all 3 skills in its prompt—15 total skill injections across the DAG, of which only 3 are relevant (one per node). The other 12 injections are pure attention dilution: they compete with the accumulated context, tool schemas, and task goal for the model's limited attention budget.

#### Solution

Skills are injected *per-node*, not globally. A skill is included in the context of only the DAG node that needs it.

Consider a task that involves exploring a codebase (Probe Node), querying a Salesforce API (Action Node), and synthesizing a report (Synthesis Node). A Salesforce API skill is injected only into the Action Node dispatching the Salesforce tool—not into the Probe Node exploring the codebase, not into the Recall Node synthesizing results.

Skill relevance is determined by semantic similarity between the node's interpolated prompt and the skill's trigger description, using ONNX embeddings for fast local similarity computation. An activation threshold (configurable, defaulting to a semantic match score that filters irrelevant skills; settable to 0.0 for deterministic routing when the target is known) controls injection.

**Two skill types interact:**

**Procedural Micro-Skills** are structured Markdown SOPs encoding successful execution patterns: a trigger description (when to activate), step-by-step instructions, expected input/output formats, and common pitfalls.

**Corrective Micro-Skills** encode *failure* patterns rather than success patterns (see Pattern 3 for the extraction mechanism). Where Procedural Micro-Skills say "here's how to do X correctly," Corrective Micro-Skills say "when you see pattern Y, don't do Z—do W instead." A node may receive both types: the general procedure and the specific anti-pattern, teaching the local model both the correct approach and the known pitfalls.

**The Observer Agent** closes the feedback loop. This reactive Background Agent fires on task completion telemetry and processes the execution trajectory:

1. A task completes and emits telemetry events
2. The Observer synthesizes memories and knowledge graph entities
3. It extracts Procedural Micro-Skills from successful patterns and Corrective Micro-Skills from escalation diffs
4. Future tasks receive these skills via scoped injection
5. The Observer watches those improved tasks, potentially extracting refined skills

This self-reinforcing cycle means the system improves with use—each task execution is a training signal for future executions, mediated through the skill index rather than model weights.

**Important caveat for evaluation:** In benchmark mode, each task executes against an isolated empty database. No skills carry between tasks. The self-improvement loop is a production feature, not a benchmark artifact.

#### Why Alternatives Fail

- **Global injection:** 5× unnecessary context load, directly competing with relevant information for the model's limited attention.
- **Per-task injection:** Too coarse—different nodes within a task need different skills.
- **No injection:** The model repeats known failure patterns that have already been diagnosed and corrected.
- **Fine-tuning on failure patterns:** Requires retraining, is slow, and the corrections are not inspectable or editable by humans.

#### Prerequisites

A node-level execution model where each inference call has an independent context window. A skill storage and retrieval system (database + embedding-based search). The principle—inject context only where relevant—applies universally, but the per-node granularity requires a system where inference calls are individually addressable, not a single long conversation thread.

#### Production Data

- Scoped injection replaced global injection during the August 5 improvement cycle—part of a 1.19-point quality recovery in a single day (combined with two other subsystems)
- The Observer Agent extracts both procedural and corrective skills autonomously from execution trajectories
- Activation threshold of 0.0 enables deterministic skill routing for known tool-skill pairings

---

## Pillar III: Quality Assurance

---

### Pattern 7: Verified Task Execution

#### Problem

SLMs produce high-variance output. Benchmark data shows the 4B model producing 5.0-quality output on some runs and 1.0 on others for the *same task, same model, same framework*. The capability exists in the weights; the problem is *variance with no verification mechanism*.

Before Verified Task Execution was introduced, the system contained nine separate mechanisms compensating for local model failures: Generation Guard, DRY sampling, meta-commentary detection, Synthesis Validation Gate, Two-Pass Extraction, Recall Loop Inversion, No-Action Retry, Exploration Queue, and SQL Auto-Extraction. Each mechanism was individually justified, but the interaction space was exponential—tuning one mechanism's thresholds caused regressions in another. This is the wrong abstraction.

#### Solution

Verified Task Execution is a single verification gate that accepts or rejects terminal output. All nine prior mechanisms are retained but *reframed*: their purpose shifts from "compensate for bad output" to "maximize the verification accept rate."

The pipeline has four stages:

**Stage 1: Structural Pre-Check** (local, deterministic, <1ms). Detects Generation Guard markers, meta-commentary degeneration ("As an AI…" patterns), and length/truncation anomalies. Rejects obviously degenerate output before any cloud cost is incurred.

**Stage 2: Pre-Flight Validation** (<10ms). Coverage verification via key-term matching against the original goal, and content validation (URL format checking, quote verification). Still fully local.

**Stage 3: Cloud Verification.** Evaluates the terminal synthesis against the original goal and the Recall Node's `refinedContext`. Returns a Verification Rubric with per-dimension float scores and an accept/reject decision.

**Stage 4: Cloud Re-Synthesis** (if rejected). The *same* cloud call that rejects also produces a re-synthesis from the `refinedContext`—no additional round-trip. This is a key design decision: combining verification and re-synthesis into a single call saves latency and cost.

**Code Generation Quality Gates** operate as additional VTE-adjacent mechanisms:

- **Compilation Gate:** Runs language-specific syntax and compilation verification (`go build`, `tsc --noEmit`) to catch structural errors before human review.
- **Spec Compliance Gate:** An LLM-evaluated checklist where each requirement from the original specification is scored as IMPLEMENTED or MISSING, producing a concrete gap analysis.
- **Preservation Assertion:** An AST-based diff that detects dropped public symbols—functions, types, methods that existed in the original file but are missing from the generated output. This catches the 4B model's tendency toward catastrophic symbol dropping during full-file generation.
- **Edit Loop:** For existing files (≥20 lines), hunk-based iterative editing replaces full-file regeneration. Each step produces a GBNF-constrained JSON hunk (`{searchContent, replaceContent, done}`) applied via surgical text replacement. This achieves 30–50× token reduction over full-file generation because only changed regions are generated.
- **Tiered escalation:** One local retry with corrective feedback, then cloud escalation if the retry fails.

A `strict-local` Privacy Level disables cloud verification entirely, running only the Structural Pre-Check and Pre-Flight Validation. The user accepts the output variance in exchange for complete data locality.

#### Why Alternatives Fail

- **No verification:** Accepting high variance means some outputs are unusable, and the user cannot predict which.
- **Multiple compensatory mechanisms without a gate:** The interaction space is exponential. Each mechanism has thresholds that interact with other mechanisms' thresholds. Tuning is fragile and regressions are common.
- **Cloud-only execution:** Eliminates the cost and privacy benefits that motivate local-first execution.
- **Multiple verification round-trips:** Each additional cloud call adds latency and cost. Combining verify + re-synthesize in one call is critical for practical cost.

#### Prerequisites

A verification-capable model (can be the same cloud model used for planning). A quality signal—even a binary accept/reject is sufficient. The core insight is architectural: one verification gate that accepts or rejects is better than nine mechanisms that individually compensate. Any system that produces variable-quality output can benefit from a verification stage.

#### Production Data

- +0.60 quality points when VTE came online (Run 25: 4.20 → 4.53 by Run 28)
- Cost: ~$0.03–0.05 per Probe/Analyze task in cloud verification
- 9 pre-existing mechanisms retained, reframed as "maximize accept rate"
- Edit Loop: 30–50× token reduction vs. full-file generation
- Preservation Assertion catches catastrophic symbol dropping in full-file codegen

---

## 6. Evidence

*[This section will be updated with results from the holdout set and decoupled evaluator runs.]*

### Evaluation Methodology

#### Per-Task Database Isolation

A critical methodological detail: each benchmark task executes against an isolated, empty SQLite database. No micro-skills, memories, knowledge graph entities, or any other learned state carries between tasks or between runs. The database is created fresh per task with a unique timestamp:

```go
dbFile := fmt.Sprintf("tzro_comparison_%s_%s_%d.db",
    conditionID, t.ID, time.Now().UnixNano())
```

This means the entire improvement arc—from 2.64 to 4.53 across 28 runs—is attributable exclusively to infrastructure code changes between runs (better compaction, Phase Runners, VTE, etc.), not accumulated system learning. The system does not "memorize heuristics" for the test suite.

#### Evaluation Design

The evaluation uses three complementary data sources:

1. **Original 25-task suite** (development set): 28 full-suite runs spanning July 27 – August 12, 2026. LLM-as-judge quality scoring on a 1.0–5.0 scale. Tasks span four categories: code generation (10), data analysis (5), documentation/exploration (5), and web research (5).

2. **Holdout set** (generalization test): 15–20 new tasks across existing and novel categories. Frozen code—no bug fixes between runs. Zero-state databases. Designed to test whether the patterns generalize beyond the tasks that motivated their creation.

3. **Decoupled evaluator**: Claude as independent judge, while VTE continues to use Gemini for re-synthesis. This directly tests whether quality scores hold across model preferences, ruling out LLM self-preference bias.

#### Hardware Configuration

All experiments ran on a single Apple MacBook Pro with Apple M-series processor and 24GB unified memory:

- **Worker model:** Agents-A1-4B (fine-tune of Qwen-3.5 4B; Q8_0 GGUF quantization), via llama.cpp
- **Router model:** Qwen-3.5 0.6B Instruct (Q8_0 GGUF), as speculative decoding draft
- **Cloud model:** Google Gemini 3.5 Flash (API)
- **Embedding:** Nomic Embed v1.5 (ONNX, local)
- **Database:** SQLite 3 (WAL mode)

### The Quality Improvement Arc

The improvement trajectory spans two evaluation phases:

**Phase 1 (May–June 2026):** BFCL-derived parameter extraction benchmarks. Binary pass/fail metric. Pass rate improved from 21% to 65% through infrastructure additions (schema coercion, accumulated context architecture, GBNF matching).

**Phase 2 (July–August 2026):** 25-task quality-scored suite. Three critical inflection points:

1. **August 4 regression (4.20 → 2.64):** The Recall Node architecture overhaul. The new Refinement Pass design (Pattern 4) was architecturally sound but initially misconfigured. Diagnosis took ~6 hours across 3 runs.

2. **August 5 recovery (2.64 → 3.83 in one day):** Three subsystems came online simultaneously: Spec Compliance Gate, Structural Pre-Check meta-commentary filter, and scoped Micro-Skill injection replacing global injection.

3. **August 11–12 final push (3.93 → 4.53):** Verified Task Execution (Pattern 7) completed the quality pipeline.

| Run | Date | Avg Quality | Δ | Key Change |
|-----|------|------------|---|------------|
| 1 | Jul 27 | 3.99 | — | Baseline |
| 7 | Jul 28 | 3.46 | -0.53 | Regression from refactor |
| 8 | Jul 29 | 4.22 | +0.76 | Recovery + context improvements |
| 11 | Aug 4 | **2.64** | -1.56 | Recall Node overhaul |
| 18 | Aug 5 | 3.83 | +1.19 | Three subsystems online |
| 25 | Aug 11 | 4.25 | +0.42 | VTE online |
| **28** | **Aug 12** | **4.53** | +0.28 | **Current best** |

### Quality Results by Category

| Category | Dedicated Runs | Best Avg | Notes |
|----------|---------------|----------|-------|
| Code generation | 2 | 4.54 | Strongest—quality gates effective |
| Documentation | 4 | 4.75 | 1.00 → 4.75 in one day |
| Data analysis | 17 | 3.55 | Hardest—SQL reasoning challenging |
| Web research | 19 | 3.70 | Phase Runner essential |
| **Full suite** | **28** | **4.53** | All categories combined |

Per-task distribution (Run 28): 16 tasks (64%) scored 5.00; 5 tasks (20%) scored 4.00–4.99; 3 tasks (12%) scored 3.00–3.99; 1 task (4%) scored below 3.00.

### Cost Analysis

| Metric | Value |
|--------|-------|
| Total cloud tokens | 243,911 |
| Total local tokens | 1,661,896 |
| Total estimated cost | $2.57 |
| Average cost per task | $0.10 |
| Cloud inference calls | 67 (14%) |
| Local inference calls | 412 (86%) |
| Cloud:Local ratio | 1:6.1 |

Latency correlates strongly with local token consumption (r = 0.98) and tool call count (r = 0.91), not with cloud token usage (r = 0.34). The bottleneck is local inference throughput, not cloud API latency.

### Holdout Set Results

*[To be added after holdout benchmark runs are complete.]*

### Decoupled Evaluator Results

*[To be added after Claude-as-judge benchmark runs are complete.]*

---

## 7. Limitations

Several significant limitations constrain the generality of these findings:

**Data analysis remains a genuine capability boundary.** The 4B model achieves only 3.55/5.0 on data analysis tasks—the lowest category by a significant margin. SQL query composition, particularly multi-step analytical reasoning where intermediate results must carry between queries, consistently exceeds the model's effective reasoning capacity. The Deterministic Query Path handles simple queries, but complex analytical questions (sequential aggregation with filtering) require frontier-level reasoning. This is a real capability gap, not a decomposition problem.

**LLM-as-judge has known limitations.** Cross-verification between the cloud judge and the local model revealed a 38% inter-rater disagreement on documentation tasks. The cloud judge rewards structural formatting and coherent prose without adequately verifying factual accuracy. The local model may be overly sensitive to outputs that differ from its own generation patterns. Without a human evaluation baseline, we cannot determine which judge is more accurate, only that the disagreement exists and is category-dependent. The Symbol Anchor Check partially addresses this for code-related outputs; no equivalent mechanism exists for web research or general knowledge synthesis.

**Single-model evaluation.** All results are from a single model: Agents-A1-4B (Qwen-3.5 4B fine-tune, GGUF Q8_0). While this strengthens the "infrastructure over parameters" claim (all improvement comes from infrastructure), it limits generalizability. The patterns may produce different improvement curves on other model families, particularly those with different attention characteristics or instruction-following behavior.

**Single-developer benchmark.** The 25-task suite was designed by a single developer for a specific codebase. The task distribution may not represent the broader space of agentic workloads. The suite lacks tasks requiring multi-turn user interaction, real-time system integration, or adversarial inputs.

**Privacy-quality tradeoff.** The `strict-local` privacy mode disables cloud verification, accepting higher output variance in exchange for complete data locality. The quality degradation under strict-local mode has not been formally quantified, though the 4.20 → 4.53 improvement attributable to VTE provides an upper-bound estimate of the gap.

**Hardware specificity.** All experiments ran on Apple Silicon with 24GB unified memory. Inference throughput, thermal throttling, and memory pressure are specific to this hardware. Performance on Linux servers with discrete GPUs, or on machines requiring smaller quantizations, remains unevaluated.

---

## 8. Lessons Learned

Four high-level findings emerged from building and evaluating this system:

### 1. Infrastructure dominates parameters (above a threshold)

The quality improvement from 2.64 to 4.53 was achieved with zero model changes. Every improvement came from infrastructure: better compaction, structured phases, binding splices, validation gates, and verification pipelines. This suggests the current discourse around model scaling underweights the contribution of execution infrastructure.

The qualifier matters. The baseline model must meet a capability threshold: tool-use fine-tuning, structured output support, basic instruction following. Below that threshold, no amount of infrastructure compensates. Above it, infrastructure improvements compound in ways that parameter scaling does not.

### 2. Nine compensatory mechanisms is the wrong abstraction

By the time Verified Task Execution was introduced, the system contained nine separate mechanisms compensating for local model quality variance. Each was individually justified, but the interaction space was exponential—tuning one mechanism's thresholds caused regressions in another.

The correct abstraction was a single verification gate that accepts or rejects, with all prior mechanisms reframed as "maximize the accept rate" rather than "compensate for bad output." This reframing eliminated the tuning problem: each mechanism operates independently to improve context quality, and the gate makes the final binary decision.

### 3. Domain language prevents architectural drift

The project maintains a formal vocabulary of ~120 terms with explicit *anti-terms* (e.g., "Avoid: pipeline, automation track" for the Kahn Compiler). Anti-terms serve two purposes: they prevent architectural drift by banning synonyms that carry incorrect connotations, and they function as onboarding documentation.

AI contributors in particular benefit from anti-terms. Without them, a model generating code or documentation uses natural synonyms that carry incorrect architectural connotations, gradually blurring the boundaries between concepts. With 76 Architecture Decision Records, terminological precision is not a luxury—it is load-bearing infrastructure.

### 4. Evaluation methodology must evolve with the system

Phase 1 (BFCL-derived parameter extraction) became inadequate when end-to-end quality, not individual tool calls, became the bottleneck. Phase 2 (LLM-as-judge quality scoring) revealed new blind spots (the documentation inter-rater gap). Each evaluation phase exposed the limitations of the previous methodology.

A longitudinal evaluation must treat its own methodology as a variable, not a constant. The holdout set and decoupled evaluator represent the next evolution—testing generalization and ruling out evaluator bias—but they will undoubtedly reveal their own blind spots in turn.

---

*TZRO is open source and available at [github.com/The18thWarrior/tzro](https://github.com/The18thWarrior/tzro). The full system, benchmark suite, and 76 Architecture Decision Records are available for inspection and extension.*
