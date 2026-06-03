# tzro: Go-Powered Durable Agentic Execution Engine

## Comprehensive Technical Design & Architecture Specification

**Version:** 1.0.0  
**Status:** Approved  
**Author:** AI Pair Programming Agent

---

## 1. High-Level Architecture

`tzro` is a local-first, highly durable, and deterministic **Agentic Execution Engine** based on the X execution framework. It is designed to run complex multi-system workflow integrations on resource-constrained hardware by separating cognitive scheduling (Cloud Strategy Planning) from structured execution translation (Local GBNF Action Execution).

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
        Local -->|4. Tool Invocation| Host[Stdio MCP Host Daemon]
    end

    subgraph "Context & Memory Systems"
        Host -->|Large Result (>12K)| Cache[Disk-Backed JQ Cache]
        Host -->|Standard Result| Compactor[5-Layer Compaction Pipeline]
        Compactor -->|Compacted Context| Local
        Local -->|Memory Read/Write| Mem[Tabular KV Memory]
        Local -->|Relational Graph-RAG| KG[Knowledge Graph SQLite]
    end

    subgraph "Proactive Feedback & Synthesis"
        Runner -->|Chronological Events Sync| Channel[Observer Sync Channel]
        Channel -->|Debounce & Eager Execute| Observer[Proactive Observer Agent]
        Observer -->|Deactivate Completed Entities| Host
        Runner -->|Successful Trajectory| Skill[Procedural Micro-Skills Synthesizer]
        Skill -->|Commit SOP| DB[(Local SQLite DB)]
        DB -->|Index Injection| Planner
        DB -->|Full SOP Injection| Local
    end
```

---

## 2. Pillar 1: Request & Complexity Classification

To optimize latency, cost, and local processing overhead, `tzro` routes incoming natural language requests through a two-pass classification layer.

### 1. Intent Classification

The **Intent Classifier** evaluates the raw prompt, classifies it into a core entity type, and extracts structural parameters to build the target entity:

- **chat:** Conversational Q&A or database query lookups in a single turn. (Fallback default).
- **task:** A single multi-step goal (e.g., deep research, workflow compilation, syncs).
- **workflow:** High-level initiative requiring persistent coordination of multiple tasks over days/weeks.

#### Intent Classification Prompt

```
You are an intent classification agent for the tzro platform. Your job is to classify a user's natural language request into exactly one of five entity types and extract the necessary parameters to create that entity.

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

### 2. Complexity Classification (T0 / T1 / T2)

Once categorized as a structured task, the request is rated into an execution tier. To eliminate LLM latency (~200ms) on conversational prompts, a native Go **heuristic pre-classifier** runs regular expression checks first:

```go
package classifier

import (
	"strings"
)

func HeuristicClassify(requestText string, toolNames []string) string {
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

## 3. Pillar 2: Durable Directed Acyclic Graph (DAG) Engine

`tzro` enforces a strict separation between planning and execution. Complex tasks are compiled into a Directed Acyclic Graph to prevent infinite tool loops and allow execution recovery.

### 1. Compiled Data Structures

```go
package domain

type GraphNode struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`                // "action" | "deterministic" | "branch" | "merge"
	Action          string                 `json:"action"`              // Target tool name
	Instructions    string                 `json:"instructions"`        // Core step instruction
	AllowedTools    []string               `json:"allowedTools"`        // Whitelist of permitted tools
	Condition       string                 `json:"condition,omitempty"` // For logical branch nodes
	DefaultTarget   string                 `json:"defaultTarget,omitempty"`
	SuggestedSkills []string               `json:"suggestedSkillIds,omitempty"` // Injected micro-skills
	Status          string                 `json:"status"`              // "pending" | "running" | "completed" | "failed" | "skipped"
	Output          string                 `json:"output,omitempty"`
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
}

type ExecutionGraph struct {
	TaskID    string      `json:"taskId"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	MaxCycles int         `json:"maxCycles"`
	CreatedAt int64       `json:"createdAt"`
}
```

### 2. Kahn's Topological Sort Implementation

The Go compiler parses the abstract graph schema, validates that no cyclic relationships exist, and groups execution steps into parallel levels:

```go
package compiler

import (
	"fmt"
	"tzro/domain"
)

func CompileAndSort(graph *domain.ExecutionGraph) ([][]string, error) {
	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	for _, node := range graph.Nodes {
		inDegree[node.ID] = 0
		adjList[node.ID] = []string{}
	}

	for _, edge := range graph.Edges {
		adjList[edge.SourceID] = append(adjList[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var topoOrder []string
	var executionLevels [][]string

	for len(queue) > 0 {
		levelSize := len(queue)
		var currentLevel []string

		for i := 0; i < levelSize; i++ {
			u := queue[0]
			queue = queue[1:]

			currentLevel = append(currentLevel, u)
			topoOrder = append(topoOrder, u)

			for _, v := range adjList[u] {
				inDegree[v]--
				if inDegree[v] == 0 {
					queue = append(queue, v)
				}
			}
		}
		executionLevels = append(executionLevels, currentLevel)
	}

	if len(topoOrder) != len(graph.Nodes) {
		return nil, fmt.Errorf("compile error: execution graph contains cyclic loops")
	}

	return executionLevels, nil
}
```

### 3. Checkpointing & Loop Mitigation

- **Cycle Budgets:** `MaxCycles` is decremented on every node transition. If `MaxCycles == 0`, execution is aborted to prevent infinite runtime charges.
- **SQLite Checkpoint Table:** The output of every node is flushed to the SQLite database immediately. If the runner crashes, it reads this table to skip previously completed nodes.

```sql
CREATE TABLE graph_node_states (
    task_id        TEXT NOT NULL,
    node_id        TEXT NOT NULL,
    status         TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed', 'skipped')) NOT NULL,
    output_payload TEXT,
    completed_at   INTEGER,
    PRIMARY KEY (task_id, node_id)
);
```

---

## 4. Pillar 3: Local Model Delegation & GBNF Constraints

To force lightweight local worker models (2B-4B) to output 100% syntactically correct tool execution parameters, `tzro` binds **GBNF Grammar Constraints** directly to the local inference engine.

### 1. GBNF Dynamic JSON Grammar Ruleset

```ebnf
# Production grammar compiled dynamically at runtime for tool argument extraction
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

### 2. Local Performance Optimization Architecture

- **Speculative Decoding:** Employs a tiny 135M draft model running alongside the main 4B model to project token sequences forward, increasing local generation speed by up to $1.8\times$.
- **Warm KV Prefix-Sharing:** Warm system prompts (containing the base tool descriptions and static memories) are locked into KV Cache slot `0`. Sub-steps reuse this slot, bypassing cold-start processing times.
- **Active Cache GC:** A background service monitors memory boundaries. If a cached model context remains idle for $>10$ minutes, the KV slot is forcefully flushed to protect laptop RAM.

---

## 5. Pillar 4: Proactive Observer Agent

All background system checks, memory compilations, and cron health audits are handled by a non-blocking background Observer loop running over debounced event pipelines.

### 1. Go Event-Debouncer Implementation

The Observer loop listens to a buffered Go channel (`ObserverChan`) with a capacity of `500`. It aggregates events and triggers audits _only_ when the system has been inactive for 5 minutes, or when 10 events accumulate concurrently.

```go
package observer

import (
	"context"
	"database/sql"
	"time"
)

type ObserverEvent struct {
	ID        string
	Type      string // "task_success" | "task_failed" | "heartbeat_tick"
	Payload   string
	Timestamp time.Time
}

var ObserverChan = make(chan ObserverEvent, 500)

func StartObserverLoop(ctx context.Context, db *sql.DB) {
	const debounceDuration = 5 * time.Minute
	const maxBatchSize = 10

	var batch []ObserverEvent
	timer := time.NewTimer(debounceDuration)
	timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ObserverChan:
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

func evaluateBatch(db *sql.DB, events []ObserverEvent) {
	// Execute background audits, clean up dead processes, synthesize memories
}
```

---

## 6. Pillar 5: Event-Driven Procedural Micro-Skills

To prevent zero-shot LLM hallucinations of third-party API syntax, the engine extracts and indexes structured Markdown SOPs from successful trajectories.

### 1. Double-Gate Filters

- **Complexity Gate:** Scans completed task runs. If the execution trajectory took $<3$ steps, it is aborted as too simple to warrant a reusable procedure.
- **Semantic Deduplication:** Generates a vector embedding of the trigger description. If the semantic similarity overlap score against an existing SOP exceeds **0.8**, it is skipped to prevent duplicate database bloat.

### 2. SQLite Skill Database Schema

```sql
CREATE TABLE synthesized_skills (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    trigger_description TEXT NOT NULL,
    sop_content         TEXT NOT NULL, -- Standard Markdown procedural steps
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
```

### 3. Dual-Inject Architecture

To maximize planning efficiency while protecting local context space:

1. **Cloud Planner (Index-Only Injection):** Receives only a compact JSON index of trigger descriptions and IDs (e.g., `[{"id": 44, "trigger": "bulk contacts update..."}]`). It returns matched IDs inside `suggestedSkillIds`.
2. **Local Step Executor (Full-Text Injection):** Receives the full Markdown SOP text _only_ for the specific IDs matched by the planner.

---

## 7. Pillar 6: Context Management & Compaction

When dealing with large API payloads, the local context window can quickly become overwhelmed. `tzro` utilizes a 5-layer pipeline and disk-backed caching to manage context size.

### 1. The 5-Layer Compaction Pipeline

```
               Raw Verbose Tool Payload
                          │
                          ▼
┌────────────────────────────────────────────────────────┐
│  Layer 0: Base64 Strip                                 │
│  - Replaces raw byte streams with: [binary:png, 48KB]  │
└────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────┐
│  Layer 1: HTML-to-Markdown                             │
│  - Replaces raw HTML tags with clean Markdown formats  │
└────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────┐
│  Layer 2: Tabular JSON array to TSV                    │
│  - Converts lists of objects into header-mapped TSV    │
└────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────┐
│  Layer 3: Single JSON Object to KV lines               │
│  - Replaces brackets and quotes with: key: value       │
└────────────────────────────────────────────────────────┘
                          │
                          ▼
┌────────────────────────────────────────────────────────┐
│  Layer 4: Flat Dot Notation                            │
│  - Flattens deep trees: user.profile.address.zip: 94016│
└────────────────────────────────────────────────────────┘
```

### 2. Disk-Backed JQ Cache

If the payload exceeds **12KB** after compaction, it is saved to an on-disk SQLite database and the agent receives a compact **Cache Envelope**:

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

When this envelope is returned, the engine injects a temporary instruction tutorial (**Cache Exploration Guide**) into the prompt:

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

## 8. Pillar 7: Hybrid Memory System

Memories are partitioned into Tabular Key-Value facts and a Relational Knowledge Graph representing deep cross-system entities.

### 1. Database SQLite Schemas

#### Tabular Key-Value Memory Layout

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

#### Relational Knowledge Graph Layout

```sql
CREATE TABLE kg_nodes (
    id          TEXT PRIMARY KEY,
    node_type   TEXT NOT NULL, -- 'account' | 'contact' | 'ticket' | 'document'
    name        TEXT NOT NULL,
    metadata    TEXT NOT NULL DEFAULT '{}',
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

### 2. Neighborhood Multi-Hop Traversal Implementation

To feed context-rich relations directly into the LLM during Graph-RAG searches, the engine traverses nodes up to $N$ hops to assemble a context subgraph:

```go
package memory

import (
	"database/sql"
)

type KGNode struct {
	ID       string `json:"id"`
	NodeType string `json:"nodeType"`
	Name     string `json:"name"`
	Metadata string `json:"metadata"`
}

type KGEdge struct {
	ID       string  `json:"id"`
	EdgeType string  `json:"edgeType"`
	SourceID string  `json:"sourceId"`
	TargetID string  `json:"targetId"`
	Metadata string  `json:"metadata"`
	Weight   float64 `json:"weight"`
}

type KGSubGraph struct {
	Nodes []KGNode `json:"nodes"`
	Edges []KGEdge `json:"edges"`
}

type MemoryServer struct {
	db *sql.DB
}

func (s *MemoryServer) GetEntityNeighborhood(entityID string, maxHops int) KGSubGraph {
	visited := map[string]bool{entityID: true}
	var allNodes []KGNode
	var allEdges []KGEdge
	frontier := []string{entityID}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string

		rows, err := s.db.Query(`
			SELECT id, edge_type, source_id, target_id, metadata, weight
			FROM kg_edges WHERE source_id IN (?) OR target_id IN (?)`,
			frontier, frontier,
		)
		if err != nil {
			break
		}

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

func (s *MemoryServer) fetchNodesByIDs(ids []string) []KGNode {
	// Query to map and retrieve nodes
	return nil
}
```

---

## 9. Pillar 8: MCP Host Dynamic Integrations

`tzro` integrates third-party tools dynamically by executing standard Model Context Protocol (MCP) servers locally as child processes over standard I/O (stdio) streams.

### 1. Process LifeCycle & Daemon Spawning

MCP Host servers (Node or Python based) are spawned dynamically when a **Task** starts execution and are managed as stateful, persistent daemon processes. Go goroutines maintain open stdin/stdout pipes to allow dynamic JSON-RPC 2.0 communication under sub-10ms latency:

```go
package host

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

type MCPDaemon struct {
	Name    string
	Command string
	Args    []string
	Env     []string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner
	mutex   sync.Mutex
}

func NewMCPDaemon(name, command string, args []string) *MCPDaemon {
	return &MCPDaemon{
		Name:    name,
		Command: command,
		Args:    args,
	}
}

// Start spawns the persistent daemon process and sets up pipes
func (d *MCPDaemon) Start(ctx context.Context) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.cmd = exec.CommandContext(ctx, d.Command, d.Args...)
	d.cmd.Env = append(os.Environ(), d.Env...) // Inherits OS environment variables

	var err error
	d.stdin, err = d.cmd.StdinPipe()
	if err != nil {
		return err
	}

	d.stdout, err = d.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	d.scanner = bufio.NewScanner(d.stdout)

	if err := d.cmd.Start(); err != nil {
		return err
	}

	return nil
}

// Call routes synchronous JSON-RPC 2.0 requests over stdin/stdout
func (d *MCPDaemon) Call(method string, params map[string]interface{}) (string, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	rpcBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  method,
		"params":  params,
	}
	bodyBytes, _ := json.Marshal(rpcBody)

	// Send message over stdin
	if _, err := fmt.Fprintln(d.stdin, string(bodyBytes)); err != nil {
		return "", err
	}

	// Read response over stdout
	if d.scanner.Scan() {
		return d.scanner.Text(), nil
	}

	if err := d.scanner.Err(); err != nil {
		return "", err
	}

	return "", io.EOF
}

// Stop terminates the running daemon process
func (d *MCPDaemon) Stop() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		return d.cmd.Process.Kill()
	}
	return nil
}
```

### 2. Configuration & Secrets Injection

MCP Host processes are configured inside a standard JSON file located at `.tzro/mcp_config.json`. The engine automatically injects OS variables and loads a localized `.env` file at runtime, bypassing hardcoded credentials:

```json
{
  "mcpServers": {
    "slack-integration": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-slack"],
      "env": {
        "SLACK_BOT_TOKEN": "$SLACK_BOT_TOKEN"
      }
    },
    "postgres-db": {
      "command": "node",
      "args": ["/usr/local/bin/mcp-pg-server.js"],
      "env": {
        "PGPASSWORD": "$PGPASSWORD"
      }
    }
  }
}
```

---

## 10. Pillar 9: Verification & Testing Suite

- **Kahn Topological Sor-Test:** Local Go unit tests verify cyclic validation limits and topological parallel sorting:
  ```bash
  go test -v -run TestCompiler_TopologicalSort ./compiler
  ```
- **Context Compaction Benchmarks:** Asserts that compaction ratios stay within limits ($>3\text{x}$ for JSON arrays) and nested arrays are flattened under $200\text{ms}$:
  ```bash
  go test -v -run BenchmarkCompactToolResult ./compactor
  ```
- **GBNF Syntax Checking:** Loads dynamic tool GBNF templates to ensure any grammar drifts fail parsing boundaries before reaching downstream execution:
  ```bash
  go test -v -run TestGBNF_GrammarCompliance ./gbnf
  ```
