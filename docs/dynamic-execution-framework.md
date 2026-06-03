# Dynamic Agentic Execution Framework: Standalone Blueprint & Engineering Manual

Modern enterprise automation requires a transition from raw agentic chat loops to highly durable, structured, and predictable execution systems. AI agents operating in complex environments must coordinate multiple tools, handle massive context payloads, retrieve context-rich memories, adapt dynamically to system anomalies, and utilize local models efficiently to reduce operating costs.

This document serves as a production-grade, language-agnostic **Architectural Blueprint and Engineering Manual** for the core **Dynamic Agentic Execution Framework**. It provides developers and architects with complete structural specs, SQL schemas, Go data representations, system prompts, GBNF rules, and pipeline mathematics required to build a standalone, enterprise-ready execution engine.

---

## High-Level Architecture

The execution framework acts as an orchestrated Nerve Center. Incoming requests are classified by intent and complexity before entering either a fast-path direct execution loop or a **Durable Directed Acyclic Graph (DAG)** compiler and execution runner. Throughout this lifecycle, the agent leverages hybrid memory systems, dynamic context compaction, and Model Context Protocol (MCP) bridges to access data, all monitored by a non-blocking background Observer.

```mermaid
graph TD
    subgraph "Ingest & Classification"
        User[NL Prompt / Input] --> IC[Intent Classifier]
        IC -->|chat / task / workflow| CC[Complexity Classifier]
        CC -->|T0: Direct Fallback| Direct[Direct Tool Loop / Local Model]
        CC -->|T1 / T2: Orchestrated| Planner[Cloud Planner v2]
    end

    subgraph "Durable DAG Execution Engine"
        Planner -->|1. Generate Abstract Graph JSON| Compiler[Go Graph Compiler]
        Compiler -->|2. Kahn Topo-Sort & Deterministic Nodes| Runner[Go Graph Executor v2]
        Runner -->|3. GBNF Structured Translation| Local[Local Step Executor]
        Local -->|4. Tool Invocation| Registry[MCP Dynamic + Static Registry]
    end

    subgraph "Context & Memory Systems"
        Registry -->|Large Result (>12K)| Cache[Disk-Backed JQ Cache]
        Registry -->|Standard Result| Compactor[5-Layer Compaction Pipeline]
        Compactor -->|Compacted Context| Local
        Local -->|Memory Read/Write| Mem[Tabular KV Memory]
        Local -->|Relational Graph-RAG| KG[Knowledge Graph SQLite]
    end

    subgraph "Proactive Feedback & Synthesis"
        Runner -->|Chronological Events Sync| Channel[Observer Sync Channel]
        Channel -->|Debounce & Eager Execute| Observer[Proactive Observer Agent]
        Observer -->|Deactivate Completed Entities| Registry
        Runner -->|Successful Trajectory| Skill[Procedural Micro-Skills Synthesizer]
        Skill -->|Commit SOP| DB[(Local SQLite DB)]
        DB -->|Index Injection| Planner
        DB -->|Full SOP Injection| Local
    end
```

---

## Pillar 1: Request & Complexity Classification

To optimize latency, cost, and safety, the framework routes requests through a two-pass classification layer before allocating planning resources.

```
       NL Prompt
           │
           ▼
┌──────────────────────┐
│  Intent Classifier   │ ──► [chat] ─────► Conversational Fast-Path (T0)
└──────────────────────┘
           │
           ▼ [task / workflow]
┌──────────────────────┐
│ Heuristic Pre-Class  │ ──► [matches] ──► Immediate Skip LLM Inference
└──────────────────────┘
           │
           ▼ [inconclusive]
┌──────────────────────┐
│ Complexity LLM (T0)  │ ──► [T0 / T1 / T2]
└──────────────────────┘
```

### 1. Intent Classification

The **Intent Classifier** evaluates the raw natural language prompt and classifies it into a core entity type, extracting target parameters.

- **chat:** Standard Q&A or looking up information in a single turn. (Fallback default).
- **task:** A single multi-step goal (deep research, background analysis, heartbeat syncs).
- **workflow:** High-level campaign or initiative requiring persistent coordination over days or weeks.

#### Intent Classification System Prompt

```
You are an intent classification agent for the Dynamic platform. Your job is to classify a user's natural language request into exactly one of five entity types and extract the necessary parameters to create that entity.

## Entity Types
1. chat — Conversational AI session for questions, data queries, or general assistance. Default fallback.
   Params: { "title": "<short descriptive title>", "firstMessage": "<user text>" }
2. workflow — Multi-agent orchestrated task with a goal, objective, and optional schedule.
   Params: { "name": "<short name>", "goal": "<high-level goal>", "objective": "<optional detailed objective>" }
3. research — Deep research session for comprehensive, multi-source analysis.
   Params: { "query": "<research question>", "depth": "shallow|standard|deep" }
4. heartbeat — Scheduled recurring task running on a cron schedule.
   Params: { "name": "<task name>", "cronExpression": "<5-field cron>", "prompt": "<instructions>", "taskType": "prompt|prompt_tool" }
5. mission — Persistent, long-running business goal coordinating sub-agents over weeks.
   Params: { "name": "<short name>", "goal": "<the high-level goal in full detail>" }

## Rules
- Respond with ONLY valid JSON matching the schema below. No markdown fences.
- If intent is ambiguous, default to "chat".

## Response Schema
{
  "type": "chat" | "workflow" | "research" | "heartbeat" | "mission",
  "confidence": 0.0-1.0,
  "summary": "Plain English summary of what will be created",
  "params": { ... type-specific parameters ... }
}
```

#### Normalization Mapping

To ensure compatibility with downstream systems, the classified intents are normalized into three primary runtime engines:

- `workflow` / `research` / `heartbeat` $\rightarrow$ **task**
- `mission` $\rightarrow$ **workflow**
- `chat` $\rightarrow$ **chat**

---

### 2. Complexity Classification (T0 / T1 / T2)

Once classified as a structured task, the **Complexity Classifier** maps the prompt to an execution tier. To eliminate LLM inference latency (~200ms) on simple messages, a **heuristic pre-classifier** runs regex and word checks first.

| Tier                | Definition                                                         | Planning Resource | Execution Resource                     |
| :------------------ | :----------------------------------------------------------------- | :---------------- | :------------------------------------- |
| **T0 (Direct)**     | Zero or one tool call; conversational, creative, or quick lookups. | None              | Direct Tool Loop (Local Model)         |
| **T1 (Planned)**    | $2+$ tool calls, multi-step queries, sequential operations.        | Cloud Planner v2  | Local Step Executor (GBNF-constrained) |
| **T2 (Supervised)** | Bulk edits, risky writes, migrations, deletion routines.           | Cloud Planner v2  | Cloud LLM Oversight & Guardrail Gate   |

#### Heuristic Pre-Classifier Algorithm

```go
func heuristicClassify(requestText string, toolNames []string) string {
    lower := strings.ToLower(strings.TrimSpace(requestText))
    words := strings.Fields(lower)

    // Rule 1: Very short messages are conversational
    if len(words) <= 2 {
        return "T0"
    }

    // Rule 2: Check for definite T1/T2 bulk or multi-step operations
    t1Patterns := []string{
        "delete all", "update all", "bulk ", "for each", "migrate ",
        "find all", "export all", "import all", "and then", "after that",
    }
    for _, p := range t1Patterns {
        if strings.Contains(lower, p) {
            return "" // Inconclusive: Fall through to LLM classification
        }
    }

    // Rule 3: Map semantic keywords to active tool schemas to verify tool-dependence
    referencesTool := false
    for _, tn := range toolNames {
        normalized := strings.ReplaceAll(strings.ToLower(tn), "_", " ")
        if strings.Contains(lower, normalized) {
            referencesTool = true
            break
        }
    }

    // Rule 4: If no tools are referenced and it has conversational prefixes, it's T0
    t0Prefixes := []string{
        "tell me", "what is", "explain", "describe", "hello", "write ", "create a ",
    }
    for _, prefix := range t0Prefixes {
        if strings.HasPrefix(lower, prefix) && !referencesTool {
            return "T0"
        }
    }

    return "" // Inconclusive: Let Local LLM decide
}
```

---

## Pillar 2: Durable Directed Acyclic Graph (DAG) System

Complex tasks are compiled into a Directed Acyclic Graph. This enforces rigorous separation between **planning** (Cloud Strategy) and **execution** (Local GBNF Action Translation), preventing infinite tool loops and ensuring extreme resilience.

```
       NL Task Request
             │
             ▼
┌──────────────────────────┐
│    Cloud Planner v2      │ ──► Generates Graph JSON & Allowed Tools
└──────────────────────────┘
             │
             ▼
┌──────────────────────────┐
│    Go Graph Compiler     │ ──► Kahn's Topological Sort & Validations
└──────────────────────────┘
             │
             ▼
┌──────────────────────────┐
│   Go Graph Executor v2   │ ◄── [Deterministic Nodes Bypass LLM]
└──────────────────────────┘
      │              │
      ▼ (Logical)    ▼ (Action Node)
   [Branch]    ┌──────────────────────────┐
   [Merge]     │   Local Step Executor    │ ──► Invokes mcp_call / static tools
               └──────────────────────────┘
```

### 1. Separation of Concerns

1. **Cloud Planner v2 (The Strategist):** Running on a high-capability cloud model, it parses the goal, analyzes available tools, injects learned SOP micro-skills, and outputs a complete Abstract Graph JSON. It **never** executes tools itself.
2. **Go Graph Compiler (The Structurer):** Parses the Graph JSON, runs validation gates (checking for illegal cycle loops, unresolvable node parents), compiles it, and topological-sorts the execution levels using **Kahn's Algorithm**.
3. **Go Graph Executor v2 (The Driver):** Orchestrates node execution. When an action node fires, it feeds the node context, allowed tools, and instructions to the **Local Step Executor** to invoke tools.
4. **Local Step Executor (The GBNF Translator):** Runs on a lightweight local model (e.g. Qwen 4B). It takes the single instruction (e.g. _"Read from spreadsheet"_), executes the tool, and returns the result back to the Go Executor.

---

### 2. Graph Data Representation

The graph is represented by Go structures mapping execution steps, dependency edges, and logical conditions:

```go
type GraphNode struct {
	ID             string                 `json:"id"`
	Type           string                 `json:"type"`            // "action" | "deterministic" | "branch" | "merge"
	Action         string                 `json:"action"`          // Target tool name
	Instructions   string                 `json:"instructions"`    // Core instruction for this step
	AllowedTools   []string               `json:"allowedTools"`    // Strict subset of tools allowed for this node
	Condition      string                 `json:"condition,omitempty"` // For branch nodes
	DefaultTarget  string                 `json:"defaultTarget,omitempty"`
	SuggestedSkills []string              `json:"suggestedSkillIds,omitempty"`
	Status         string                 `json:"status"`          // "pending" | "running" | "completed" | "failed" | "skipped"
	Output         string                 `json:"output,omitempty"`
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type ExecutionGraph struct {
	TaskID    string       `json:"taskId"`
	Nodes     []GraphNode  `json:"nodes"`
	Edges     []GraphEdge  `json:"edges"`
	MaxCycles int          `json:"maxCycles"`
	CreatedAt int64        `json:"createdAt"`
}
```

---

### 3. Deterministic Nodes

To bypass LLM latency and non-deterministic hallucination, the Go Executor supports **Deterministic Nodes**. These are executed directly by Go code without routing through an LLM:

- **Branch Nodes:** Evaluates basic boolean logic expressions against parent outputs (e.g., `parent.recordCount > 0`) to dynamically branch the graph.
- **Merge Nodes:** Resolves multi-path outputs, compiling them into a structured layout using static templating.
- **Static Transformer Nodes:** Runs mathematical aggregations, array lengths, or TSV parses directly.

---

### 4. Fault Tolerance & Loop Mitigation

- **Cycle Budget:** To prevent runaway loops, the execution engine decrements `MaxCycles` on every node transition. If `Cycles == 0`, execution is aborted and marked `interrupted`.
- **State Checkpointing:** The output of every node is flushed to the database under the `graph_node_states` table. If the desktop app crashes, the runner can resume from the last completed Kahn level without re-running prior API writes.
- **Concurrency Gates:** Steps within the same Kahn topo-level are executed concurrently using Go goroutines up to the local RAM slot limit, while steps between levels maintain strict sequential sync gates.

---

## Pillar 3: Local Model Delegation & GBNF Constraints

Local worker models (2B–4B) lack the capability to output complex structured JSON reliably without structure validation. To enforce 100% syntactic coherence, the framework uses **GBNF (GGML BNF) Grammar Constraints** at the engine interface level.

### 1. GBNF Grammar Definition (Structured Tool Extraction)

When the Local Step Executor is called, the request is restricted to outputting _only_ valid JSON conforming to the selected tool's schema. Below is the production GBNF grammar compiled dynamically at runtime for tool argument extraction:

```ebnf
# Production Grammar for Tool Call Constraint
root   ::= object
object ::= "{" ws member (ws "," ws member)* ws "}"
member ::= string ws ":" ws value
value  ::= object | array | string | number | "true" | "false" | "null"
array  ::= "[" ws (value (ws "," ws value)*)? ws "]"

# Scalar types
string ::= "\"" ([^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F]{4}))* "\""
number ::= "-"? ([0-9] | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [+-]? [0-9]+)?

# Whitespace handling
ws     ::= [ \t\n\r]*
```

When this GBNF grammar is bound to the llama.cpp server, the model is physically incapable of emitting markdown wrappers, conversational pleasantries, or malformed braces — guaranteeing 100% parsing success.

---

### 2. RAM & Hardware-Aware Optimizations

To run execution graphs entirely on standard office laptops without lag:

- **Speculative Decoding:** Uses a tiny 135M draft model to project text sequences, accelerating generation speed on local CPUs by $1.8\times$.
- **Prefix-Sharing Context:** Warm system prompts (containing the base tool descriptions and static memories) are locked into the model's KV Cache slot `0`. Sub-steps reuse this slot, dropping cold-start prompt processing times from $8\text{s}$ to $< 100\text{ms}$.
- **Active Cache GC:** A background worker monitors CPU/GPU RAM utilization. If idle context usage persists for $>10\text{ minutes}$, the KV slots are forcefully flushed to liberate system resources.

---

## Pillar 4: Proactive Observer Agent Design

Rather than building complex cron frameworks inside tool code, the platform delegates background audits, memory consolidation, and scheduling hygiene to a dedicated, non-blocking **Observer Agent**.

```
           System Event (Task Success / Error / Sync)
                              │
                              ▼
                 ┌─────────────────────────┐
                 │  observerChan (Cap:500) │
                 └─────────────────────────┘
                              │
                              ▼ [Debounce Pipeline]
         Wait 5 mins OR Accumulate 10 events concurrently
                              │
                              ▼
                 ┌─────────────────────────┐
                 │   Observer LLM Worker   │
                 └─────────────────────────┘
                  /           │           \
                 /            │            \
                ▼             ▼             ▼
         [Save Memory]  [Setup Monitor]  [Audit Lifecycle]
```

### 1. Non-Blocking Event-Debouncer Pipeline

To prevent observer processing overhead from slowing down critical execution paths, all system events are pushed into a buffered Go channel (`observerChan`) with a capacity of `500`.

The Observer runs a debouncer loop:

- It accumulates events and waits for **5 minutes** of absolute system inactivity before initiating review.
- If **10 events** accumulate concurrently, it bypasses the timer and triggers an eager evaluation batch immediately.

```go
func StartObserverLoop(ctx context.Context, db *sql.DB) {
	const debounceDuration = 5 * time.Minute
	const maxBatchSize = 10

	var batch []ObserverEvent
	timer := time.NewTimer(debounceDuration)
	timer.Stop() // Initialize in idle state

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-observerChan:
			batch = append(batch, evt)
			if len(batch) >= maxBatchSize {
				timer.Stop()
				b := append([]ObserverEvent(nil), batch...)
				batch = nil
				go evaluateBatch(db, b)
			} else {
				timer.Stop()
				timer.Reset(debounceDuration)
			}
		case <-timer.C:
			if len(batch) > 0 {
				b := append([]ObserverEvent(nil), batch...)
				batch = nil
				go evaluateBatch(db, b)
			}
		}
	}
}
```

---

### 2. Proactive Auditing & Entity Lifecycle GC

Once triggered, the Observer executes a **Full System Audit** (rate-limited to 2x daily) to prevent "zombie" scheduled heartbeats or orphaned background tasks from draining cloud API budgets.

- **Audit Workflow:**
  1. Calls `list_heartbeat_tasks` and active `tasks` listings.
  2. Synthesizes parent metrics: reviews if a scheduled sync task has returned zero changes or failed for 5 consecutive runs.
  3. If verified as obsolete, it invokes `deactivate_heartbeat_task`.
  4. Automatically emits a structured Markdown notification to the user's dashboard explaining the deactivation.

---

## Pillar 5: Event-Driven Procedural Micro-Skills

To eliminate zero-shot LLM hallucination of API syntaxes (such as Salesforce SOQL or HubSpot Search), the framework incorporates an **Event-Driven Micro-Skills Pipeline**. This system autonomously _extracts_, _deduplicates_, and _injects_ Standard Operating Procedures (SOPs).

```
   [Execution Graph Completes Successfully]
                     │
                     ▼
       ┌───────────────────────────┐
       │   Deterministic Filter    │ ── [Completed Steps < 3] ──► Abort
       └───────────────────────────┘
                     │ [Passed]
                     ▼
       ┌───────────────────────────┐
       │    Semantic Deduplicator  │ ── [Overlap with Existing] ─► Abort
       └───────────────────────────┘
                     │ [Passed]
                     ▼
       ┌───────────────────────────┐
       │     LLM SOP Synthesizer   │ ──► Writes Markdown SOP
       └───────────────────────────┘
                     │
                     ▼
       ┌───────────────────────────┐
       │ SQLite synthesized_skills │
       └───────────────────────────┘
```

### 1. Procedural SOP Extraction

When a Compiled DAG task finishes successfully, it triggers the extraction pass:

1. **Deterministic Gate:** The engine scans the trajectory. If the task completed in $< 3$ steps, it is aborted as "too trivial to represent a reusable procedure".
2. **Semantic Deduplication Gate:** The engine fetches existing triggers. If a semantic overlap score $>0.8$ is detected against saved trigger descriptions, the process aborts to prevent duplicate bloat.
3. **LLM Synthesis Pass:** The LLM receives the prompt, the execution trajectory, and the output, compiling a highly structured Markdown SOP.

#### Synthesized SOP Schema (`synthesized_skills` Table)

```sql
CREATE TABLE synthesized_skills (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    trigger_description TEXT NOT NULL,
    sop_content         TEXT NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
```

#### Rendered SOP Markdown Output Structure

```markdown
# Trigger

When the user requests a bulk merge of duplicate Salesforce accounts using email addresses.

# Context

Salesforce query outputs must exclude deleted contacts. Ensure to query the Account ID alongside the email to establish relational mapping before executing the merge API.

# Steps

1. Call `salesforce_query` with query: "SELECT Id, Email, AccountId FROM Contact WHERE Email != NULL AND IsDeleted = false"
2. Group the contact maps inside memory by Email key.
3. For every contact list having size > 1, execute `salesforce_merge_contacts` using the primary Account ID.
```

---

### 2. Dual-Inject Architecture

To maximize planning efficiency while protecting local model context size:

1. **Cloud Planner (Index-Only Injection):**
   The Cloud Planner is injected with a compact _index_ of Trigger Descriptions (e.g. `[id: 44] trigger: "bulk account merge..."`). It maps user goals to trigger signatures and returns up to 3 IDs in its `suggestedSkillIds` array.
2. **Local Step Executor (Full-Text Injection):**
   The Local Executor receives _only_ the full-text Markdown SOPs matching the IDs selected by the planner. This keeps system prompts highly compact, saving valuable RAM and KV cache slots.

---

## Pillar 6: Context Management & Large-Payload Compaction

When dealing with large API payloads (like JQL returns or database tables), the local model's context window can be quickly overwhelmed. The framework uses a **5-Layer Compaction Pipeline** to recursively compress inputs.

```
       Raw Verbose Tool Result
                  │
                  ▼
┌──────────────────────────────────┐
│  Layer 0: Strip Base64 Binary    │ ──► SGVsbG8... ──► [binary:unknown, 48KB]
└──────────────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────┐
│  Layer 1: Convert HTML to MD     │ ──► <div><span> ──► Plaintext / Markdown
└──────────────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────┐
│  Layer 2: Tabular JSON to TSV    │ ──► [{"id":1}] ──► ID \t Name \n 1 \t Alice
└──────────────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────┐
│  Layer 3: Single Object to KV    │ ──► {"user":"a"} ─► user: a
└──────────────────────────────────┘
                  │
                  ▼
┌──────────────────────────────────┐
│  Layer 4: Flatten Nested to Dot  │ ──► {"a":{"b":1}} ─► a.b: 1
└──────────────────────────────────┘
                  │
                  ▼
         [Final Compact Result]
```

### 1. 5-Layer Compaction Pipeline

- **Layer 0: Base64 Stripping:** Identifies base64 string signatures and replaces them with a metadata signature (e.g. `[binary:image/png,1.2MB]`).
- **Layer 1: Embedded HTML Extraction:** Strips HTML structures within JSON text fields (common in web scrapers) and converts them to markdown.
- **Layer 2: Tabular JSON-to-TSV Conversion:** Converts arrays of maps into Tabular TSV with a single header row. This is the highest-impact layer: all JSON syntax markers (`"{}[]:,`) are removed, and key names are written only once. Noise fields (e.g. `attributes`, `__typename`) are automatically omitted.
- **Layer 3: Single JSON Object to Key:Value lines:** Replaces JSON object brackets with a list of `key: value` lines.
- **Layer 4: Dot-Notation Flattening:** Flattens nested JSON trees to dot paths (e.g., `user.profile.address.zip: 94016`) up to a maximum depth of `3` hops.

---

### 2. Disk-Backed JQ Cache

If the payload remains $>12\text{KB}$ after compaction, the tool loop intercepts the data, saves it to an on-disk SQLite SQLite-backed tool cache, and returns a **Cache Envelope** instead.

```json
{
  "cacheId": "9fbb5166-7935-493a-ba",
  "dataType": "array",
  "rootPath": ".records",
  "fields": ["Id", "Name", "Email", "Amount"],
  "fieldTypes": {
    "Id": "string",
    "Name": "string",
    "Amount": "number"
  },
  "enumValues": {
    "Status": ["Closed Won", "Prospecting"]
  },
  "sampleRecord": {
    "Id": "0038W00001zKx4zQAC",
    "Name": "John Doe",
    "Amount": 15000
  }
}
```

When this envelope is returned, the engine injects a temporary instruction tutorial (**Cache Exploration Guide**) into the prompt. The agent then uses the `jq_cached_data` tool to execute precise JQ filters against the cache disk, retrieving _only_ the specific fields required.

#### Cache Exploration Guide (Prompt Injection)

```
## CACHED DATA EXPLORATION
A tool result was too large and has been cached on disk. You received a "cacheId".
To query this data without exceeding context limits, use the following tools:
1. introspect_cache — deep nested schema analysis
2. read_cached_data — paginated offset reads
3. jq_cached_data   — run targeted JQ query expressions

CRITICAL: Always use the "rootPath" from the envelope.
- If rootPath is ".records", JQ starts with: .records[] | select(.Amount > 500)
- NEVER assume .[] when rootPath is populated.
```

---

## Pillar 7: Hybrid Memory System

To act coherently over time, Dynamic splits memories into two architectures: **Tabular Key-Value Memories** for conversational style and facts, and a **SQLite-Backed Relational Knowledge Graph** for cross-system data linking.

```
                  Memory Extraction Channel
                              │
             ┌────────────────┴────────────────┐
             ▼                                 ▼
   [Tabular KV Database]            [SQLite Knowledge Graph]
   - Table: agent_memories          - Tables: kg_nodes, kg_edges
   - Purpose: User preferences,     - Purpose: Entity relationships,
     corrections, anti-patterns.      RAG Graph search, cross-system links.
```

### 1. Tabular Key-Value Memory

Stored under the `agent_memories` table, this holds structured reflections extracted during chat sessions or manually keyed.

#### Database Schema

```sql
CREATE TABLE agent_memories (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    type        TEXT CHECK(type IN ('fact', 'preference', 'insight', 'correction', 'anti_pattern', 'strategy')),
    content     TEXT NOT NULL,
    context     TEXT,
    confidence  REAL NOT NULL,
    source      TEXT NOT NULL, -- 'manual' | 'auto_reflection'
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
```

#### Self-Reflection Extract Prompt

Executed asynchronously when a conversation exceeds 6 messages.

```
You are a self-improvement reflection agent. Analyze the completed conversation and extract memories that will help the assistant perform better in the future.

Extract:
1. corrections — places where the user corrected the assistant's assumptions or approach. (Highest Priority).
2. anti_patterns — tools or sequences that failed or returned bad results.
3. preferences — user stated preferences for communication, tools, or styles.
4. strategies — approaches that worked well and got positive user feedback.
5. facts — objective facts about the user's environment or company.

Return valid JSON:
{
  "memories": [
    {
      "type": "correction|anti_pattern|preference|insight|strategy|fact",
      "content": "Self-contained statement of what was learned",
      "context": "Brief note of what triggered this learning",
      "confidence": 0.0-1.0
    }
  ]
}
```

---

### 2. Relational Knowledge Graph (SQLite Graph-RAG)

For mapping enterprise domain data (such as Salesforce Accounts linking to Jira Tickets and email threads), the framework uses a lightweight SQLite Knowledge Graph schema.

#### Database Schema

```sql
CREATE TABLE kg_nodes (
    id          TEXT PRIMARY KEY,
    node_type   TEXT NOT NULL, -- 'account' | 'contact' | 'ticket' | 'document'
    name        TEXT NOT NULL,
    metadata    TEXT NOT NULL DEFAULT '{}', -- JSON map
    source      TEXT,
    confidence  REAL,
    user_id     TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE kg_edges (
    id          TEXT PRIMARY KEY,
    edge_type   TEXT NOT NULL, -- 'belongs_to' | 'assigned_to' | 'references'
    source_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    target_id   TEXT NOT NULL REFERENCES kg_nodes(id) ON DELETE CASCADE,
    metadata    TEXT NOT NULL DEFAULT '{}',
    weight      REAL NOT NULL DEFAULT 1.0,
    user_id     TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
```

#### Neighborhood Multi-Hop Traversal Algorithm

To support Graph-RAG searches, the engine traverses nodes up to $N$ hops to assemble a context graph:

```go
func (s *Server) GetEntityNeighborhood(entityID string, maxHops int) KGSubGraph {
	visited := map[string]bool{entityID: true}
	var allNodes []KGNode
	var allEdges []KGEdge
	frontier := []string{entityID}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string

		// Query edges linking to frontier IDs
		rows, _ := s.db.Query(`
			SELECT id, edge_type, source_id, target_id, metadata, weight
			FROM kg_edges WHERE source_id IN (?) OR target_id IN (?)`,
			frontier, frontier,
		)

		for rows.Next() {
			var e KGEdge
			rows.Scan(&e.ID, &e.EdgeType, &e.SourceID, &e.TargetID, &e.Metadata, &e.Weight)
			allEdges = append(allEdges, e)

			for _, nid := range []string{e.SourceID, e.TargetID} {
				if !visited[nid] {
					visited[nid] = true
					nextFrontier = append(nextFrontier, nid)
				}
			}
		}
		rows.Close()

		if len(nextFrontier) > 0 {
			nodes := s.fetchNodesByIDs(nextFrontier)
			allNodes = append(allNodes, nodes...)
		}
		frontier = nextFrontier
	}
	return KGSubGraph{Nodes: allNodes, Edges: allEdges}
}
```

---

## Pillar 8: MCP-Driven Dynamic Integrations

To prevent having to rebuild hardcoded third-party connectors (for Slack, Salesforce, Jira, etc.) on every backend update, the framework implements a generic **Model Context Protocol (MCP) Dynamic Proxy Gateway**.

```
                [Local / Cloud Model]
                          │
                          ▼ [Tool Invocation]
                 ┌─────────────────┐
                 │    mcp_call     │
                 └─────────────────┘
                          │
            ┌─────────────┴─────────────┐
            ▼ [Static Tool]             ▼ [Dynamic MCP]
     [Local DB Registry]       [JSON-RPC 2.0 HTTP/SSE Proxy]
     - tools_local_db.go       - Address: MCP_SERVER_URL
     - tools_memory.go         - Schema discovery: tools/list
     - tools_task.go           - Invocation routing: tools/call
```

### 1. The Dynamic MCP Proxy Tool (`mcp_call`)

Built-in static core utilities (like memory managers, JQ caches, and file writers) remain registered in Go's native compilation space, while external tools are handled dynamically by `mcp_call` routing JSON-RPC 2.0 requests over HTTP/SSE.

#### Input Schema

```json
{
  "type": "object",
  "properties": {
    "server_url": {
      "type": "string",
      "description": "Target MCP server endpoint. Defaults to MCP_SERVER_URL env."
    },
    "method": {
      "type": "string",
      "enum": ["tools/list", "tools/call"],
      "description": "JSON-RPC method"
    },
    "tool_name": {
      "type": "string",
      "description": "Required for tools/call"
    },
    "tool_args": {
      "type": "string",
      "description": "JSON-formatted string of arguments for the tool"
    }
  },
  "required": ["method"]
}
```

---

### 2. JSON-RPC 2.0 Gateway Implementation

The proxy handles transport protocol translations, supporting standard JSON-RPC HTTP returns and Server-Sent Events (SSE) stream outputs:

```go
func (t mcpCallToolImpl) Execute(ctx context.Context, db *sql.DB, argsJSON string) (string, error) {
	var args struct {
		ServerURL string `json:"server_url"`
		Method    string `json:"method"`
		ToolName  string `json:"tool_name"`
		ToolArgs  string `json:"tool_args"`
	}
	json.Unmarshal([]byte(argsJSON), &args)

	url := args.ServerURL
	if url == "" {
		url = os.Getenv("MCP_SERVER_URL")
	}

	rpcParams := map[string]interface{}{}
	if args.Method == "tools/call" {
		var toolArgs map[string]interface{}
		json.Unmarshal([]byte(args.ToolArgs), &toolArgs)
		rpcParams["name"] = args.ToolName
		rpcParams["arguments"] = toolArgs
	}

	// Compile JSON-RPC Envelope
	rpcBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  args.Method,
		"params":  rpcParams,
	}
	bodyBytes, _ := json.Marshal(rpcBody)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("MCP_API_KEY"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")

	// SSE stream decoder fallback
	if strings.Contains(contentType, "text/event-stream") {
		return parseMCPSSEResponse(string(respBody)), nil
	}

	var rpcResp map[string]interface{}
	json.Unmarshal(respBody, &rpcResp)
	if errObj, ok := rpcResp["error"]; ok {
		return "", fmt.Errorf("mcp error: %v", errObj)
	}

	resultJSON, _ := json.Marshal(rpcResp["result"])
	return string(resultJSON), nil
}
```

---

## Pillar 9: Verification & Testing Suite

To ensure absolute reliability when executing standalone graphs:

### 1. Relational Graph Boundary Tests

Execute localized SQLite schema migrations and boundary node tests in memory. Check Kahn topologically-sorted levels manually:

```bash
go test -v -run TestGraphCompiler_TopologicalSort ./services/go-api/dataservice
```

### 2. Local Compaction Benchmarks

Assert that compaction ratios stay within optimal limits ($>3\text{x}$ for tabular JSON sets) and that nested arrays are flattened under $200\text{ms}$:

```bash
go test -v -run BenchmarkCompactToolResult ./services/go-api/dataservice
```

### 3. GBNF Compliance Checks

Load the GBNF grammars into your test loop runner and execute synthetic JSON insertions. Verify that any syntax drift immediately fails local parsing gates before tool routing is reached.
