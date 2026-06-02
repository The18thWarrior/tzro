# ADR-0006: Hybrid Memory & Relational Knowledge Graph

## Context & Problem Statement

To coordinate effectively over long business initiatives, AI agents must retain memory. However, flat vector similarity search (the typical approach in RAG pipelines) fails to capture structured context. 

For instance, an agent cannot easily understand:
1. **User Preferences/Insights:** Direct rules or corrections the user stated previously (e.g. "Do not email prospects on weekends").
2. **Relational Connections:** Knowing that a Salesforce Account links to a particular Jira Ticket, which relates to a Slack Message, and a Google Spreadsheet.

Flat embeddings do not preserve these complex graph topologies.

## Proposed Decision

We choose to implement a **Hybrid Memory System** consisting of two distinct architectures stored locally in SQLite:

1. **Tabular Key-Value Memory (`agent_memories`):** Stored under a structured KV SQLite layout to track user preferences, facts, corrections, insights, and anti-patterns. Reflected on asynchronously after long conversation segments.
2. **Relational Knowledge Graph (SQLite Graph-RAG):** Uses a lightweight relational node/edge SQLite layout to link business objects across enterprise systems.
3. **Go Multi-Hop Traversal:** Relational entities are traversed up to $N$ hops at runtime via an explicit Go neighborhood algorithm, assembling a contextual subgraph to inject directly into the LLM context.

---

## Technical Specifications

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
    metadata    TEXT NOT NULL DEFAULT '{}', -- JSON fields
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

---

### 2. Go Neighborhood Multi-Hop Traversal Algorithm

This engine algorithm gathers relational context within $N$ steps from a starting node to synthesize the local Graph-RAG context:

```go
package memory

import (
	"context"
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

// GetEntityNeighborhood traverses connected nodes up to maxHops.
func (s *MemoryServer) GetEntityNeighborhood(entityID string, maxHops int) KGSubGraph {
	visited := map[string]bool{entityID: true}
	var allNodes []KGNode
	var allEdges []KGEdge
	frontier := []string{entityID}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string
		
		// Query edges starting from or pointing to nodes in the frontier queue
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
	// Utility mapping SQL query to retrieve node data for IDs
	return nil
}
```

---

### 3. Self-Reflection Extract Prompt

This prompt is executed asynchronously by the Observer Agent when a conversation thread surpasses 6 messages, synthesizing new memories to commit to SQLite:

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

## Consequences

* **Pros:**
  * **Unified Relational Context:** Bridges the gap between disconnected database fields, enabling deep relational understanding (e.g. Account -> Contacts -> Invoices).
  * **User Adaptation:** Automatic memory reflection ensures the engine grows personalized to the user's specific workflow style and corrections.
  * **Deterministic Traversal:** Explicit multi-hop graph retrieval avoids expensive vector indexes while maintaining precise relational relevance.
* **Cons:**
  * **Relational Maintenance:** Adding/removing node records requires updating foreign key relations dynamically to prevent graph fragmentation.
  * **Context Size Inflation:** As neighbor depths ($N$) grow, neighborhood subgraphs can quickly inflate, requiring compaction.
