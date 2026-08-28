# ADR-0087: Orthogonal Plan Templates, Single-Decision Router Invariant, and Baseline Fallback

Refactors the Plan Template Registry (ADR-0048) by orthogonalizing graph topology from data source domain, enforces sequential single-decision GBNF routing passes on the 1B router sidecar, and introduces deterministic Baseline Template Fallback for zero-abort local DAG execution.

## Status

Accepted

## Context

Benchmark comparisons between ReAct and local DAG execution demonstrated superior quality, speed, and token efficiency for DAG execution. However, 5 tasks encountered zero-score aborts prior to tool execution due to failures in plan template selection, mutation unmarshaling, and surgical plan repair:

1. **Semantic Entanglement in Plan Templates (ADR-0048)**:
   The registry previously used monolithic categories (`explore-only`, `docgen`, `research`, `codegen`, `data-analysis`, `action-chain`, `multi-probe-synthesis`). `explore-only`, `docgen`, and `research` all shared the same underlying Probe-Gather-Synthesize topology (`[Probe Node] -> [Output Sink]`). The differences were purely **Source Modality** (local filesystem vs. web tools) and **Output Sink** (terminal synthesis vs. `write_file`). Forcing a 1B router model to pick between `research` and `docgen` created irreconcilable collisions on tasks like *"Research local AI frameworks on the web and write a summary to `report.md`"*.

2. **1B Router Sidecar Overload**:
   Attempting to resolve multi-attribute or compound routing decisions in a single inference call degrades small-model attention and causes logit confusion. The 1B parameter router excels at small, single-decision GBNF-constrained enumerations.

3. **Source-Blind Plan Repair**:
   When template mutation produced invalid tools or edges, `repairGraphWithProbe` hardcoded repair probe tools to `[]string{"read_file", "list_dir", "search_files"}`, breaking web research tasks during automated repair.

4. **Fatal Abort on Exhausted Plan Repair**:
   In `local_only` execution mode where cloud escalation is prohibited, failed repair caused `PlanWithEscalation` to abort the entire task with an error rather than executing a safe baseline DAG.

5. **StaticArgs Object Unmarshal Failure**:
   When the local model mutated a template emitting `"staticArgs": { "path": "README.md" }` as a JSON map instead of a raw string, Go's `json.Unmarshal` crashed with type mismatch on `GraphNode.StaticArgs`.

## Decision

### 1. Orthogonal Plan Template Registry

Deconstruct the Plan Template Registry into two orthogonal concerns: **Topology Archetypes** (pure graph shapes) and **Source Modalities** (tool scopes & context hints).

#### Topology Archetypes
Stored as canonical Go structs in `internal/templates/`:
* `probe-synthesis`: 1 probe node feeding terminal synthesis.
* `probe-and-write`: 1 probe node with a dependency edge to a `write_file` action node.
* `multi-probe-synthesis`: Parallel probe nodes with terminal join synthesis.
* `codegen`: Probe context gathering feeding a `tzro_code` action node.
* `data-analysis`: Read step feeding an `analyze` node (`sql_cached_data`, `introspect_cache`).
* `action-chain`: Linear sequence of discrete tool action nodes.

#### Source Modalities
Injected into probe nodes during template hydration:
* `local`: `AllowedTools: ["read_file", "list_dir", "search_files"]`, `SourceHint: "filesystem"`
* `web`: `AllowedTools: ["web_search", "web_browse"]`, `SourceHint: "web"`
* `hybrid`: `AllowedTools: ["read_file", "list_dir", "search_files", "web_search", "web_browse"]`, `SourceHint: "hybrid"`

### 2. Single-Decision Router Invariant & Sequential 2-Pass Routing

Establish the **Single-Decision Router Invariant**: The 1B parameter router sidecar must never be tasked with compound or multi-field decision schemas in a single prompt turn.

Intake classification executes as sequential single-decision GBNF passes:
* **Pass 1 (Topology)**: Classify user prompt into one of the 6 **Topology Archetypes**.
* **Pass 2 (Source Modality)**: When multiple tool domains exist in the active tool inventory (e.g. web search tools are registered), classify prompt into **Source Modality** (`local`, `web`, `hybrid`). If only local filesystem tools are provisioned, Pass 2 defaults deterministically to `local`.

### 3. Source-Aware Plan Repair

Update `repairGraphWithProbe` in `internal/routing/validate.go` to inspect the task's resolved `SourceModality` or tool inventory:
* If the task is web-oriented (`SourceModality == "web"`), configure the repair probe node with `web_search`, `web_browse`, and `SourceHint = "web"`.
* If local, configure with `read_file`, `list_dir`, `search_files`, and `SourceHint = "filesystem"`.

### 4. Deterministic Baseline Template Fallback

In `PlanWithEscalation` (`internal/routing/validate.go`), if template mutation fails validation and surgical repair attempts are exhausted in `local_only` mode, the engine falls back directly to the **unmodified base hydrated template** from the registry with the user's prompt injected into the probe's `Instructions`/`Goal`. This guarantees 100% DAG compilation survival without returning 0-score task aborts.

### 5. Polymorphic `StaticArgs` Normalization

Update `GraphNode` in `internal/compiler/compiler.go` with flexible JSON unmarshaling that accepts both JSON objects/maps and flat strings for `staticArgs`, normalizing objects to their JSON string representation without runtime unmarshal errors.

## Consequences

### Positive
* **Zero Router Semantic Collisions**: Decouples "what shape of work to do" from "where to get data", cleanly handling web research tasks that write files and code exploration tasks that produce reports.
* **1B Model Parity**: Sequential single-decision GBNF passes respect the capacity limits of the 1B router sidecar.
* **100% Local DAG Compilation Survival**: Baseline template fallback ensures no local task aborts before tool execution.
* **Resilient Plan Repair**: Repair probe nodes inherit the correct tool inventory for their source modality.

### Trade-offs
* Two sequential inference calls on the router sidecar for intake classification when web tools are present (~80ms additional latency).
* Baseline template fallback may drop model-added custom intermediate nodes if the mutation was irreparably malformed, executing the default archetype instead.
