package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"tzro/internal/embeddings"
)

// Tabular KV Memory methods
func (sdb *SqliteDatabase) AddMemory(m FactMemory) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	m.ID = fmt.Sprintf("mem_%d", time.Now().UnixNano())
	m.CreatedAt = time.Now()

	embStr := ""
	if len(m.Embedding) > 0 {
		b, _ := json.Marshal(m.Embedding)
		embStr = string(b)
	}

	query := sdb.dialect.InsertMemoryQuery()
	_, err := sdb.db.Exec(query, m.ID, m.UserID, m.Type, m.Content, m.Context, m.Confidence, m.Source, m.CreatedAt, embStr)
	return err
}

func (sdb *SqliteDatabase) GetMemories() []FactMemory {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	rows, err := sdb.db.Query("SELECT id, user_id, type, content, context, confidence, source, created_at, embedding FROM fact_memories ORDER BY created_at DESC")
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query memories: %v\n", err)
		return []FactMemory{}
	}
	defer rows.Close()

	var list []FactMemory
	for rows.Next() {
		var m FactMemory
		var embStr sql.NullString
		err := rows.Scan(&m.ID, &m.UserID, &m.Type, &m.Content, &m.Context, &m.Confidence, &m.Source, &m.CreatedAt, &embStr)
		if err != nil {
			fmt.Printf("[Memory Error] Failed to scan memory row: %v\n", err)
			continue
		}
		if embStr.Valid && embStr.String != "" {
			_ = json.Unmarshal([]byte(embStr.String), &m.Embedding)
		}
		list = append(list, m)
	}
	if list == nil {
		list = []FactMemory{}
	}
	return list
}

// Relational Knowledge Graph methods
func (sdb *SqliteDatabase) AddNode(n KGNode) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	embStr := ""
	if len(n.Embedding) > 0 {
		b, _ := json.Marshal(n.Embedding)
		embStr = string(b)
	}

	metaStr := serializeMetadata(n.Metadata)
	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO kg_nodes (id, node_type, name, metadata, source, weight, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, n.ID, n.NodeType, n.Name, metaStr, n.Source, n.Weight, embStr)
	return err
}

func (sdb *SqliteDatabase) AddEdge(e KGEdge) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	metaStr := serializeMetadata(e.Metadata)
	_, err := sdb.db.Exec(`INSERT OR REPLACE INTO kg_edges (id, edge_type, source_id, target_id, metadata, weight)
		VALUES (?, ?, ?, ?, ?, ?)`, e.ID, e.EdgeType, e.SourceID, e.TargetID, metaStr, e.Weight)
	return err
}

func (sdb *SqliteDatabase) GetNodes() map[string]KGNode {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	nodes, err := sdb.getNodesMapLocked()
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query nodes: %v\n", err)
		return make(map[string]KGNode)
	}
	return nodes
}

func (sdb *SqliteDatabase) getNodesMapLocked() (map[string]KGNode, error) {
	rows, err := sdb.db.Query("SELECT id, node_type, name, metadata, source, weight, embedding FROM kg_nodes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make(map[string]KGNode)
	for rows.Next() {
		var n KGNode
		var metaStr string
		var embStr sql.NullString
		err := rows.Scan(&n.ID, &n.NodeType, &n.Name, &metaStr, &n.Source, &n.Weight, &embStr)
		if err != nil {
			return nil, err
		}
		n.Metadata = deserializeMetadata(metaStr)
		if embStr.Valid && embStr.String != "" {
			_ = json.Unmarshal([]byte(embStr.String), &n.Embedding)
		}
		nodes[n.ID] = n
	}
	return nodes, nil
}

func (sdb *SqliteDatabase) GetEdges() map[string]KGEdge {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query edges: %v\n", err)
		return make(map[string]KGEdge)
	}

	edgeMap := make(map[string]KGEdge)
	for _, e := range edges {
		edgeMap[e.ID] = e
	}
	return edgeMap
}

func (sdb *SqliteDatabase) getEdgesSliceLocked() ([]KGEdge, error) {
	rows, err := sdb.db.Query("SELECT id, edge_type, source_id, target_id, metadata, weight FROM kg_edges")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []KGEdge
	for rows.Next() {
		var e KGEdge
		var metaStr string
		err := rows.Scan(&e.ID, &e.EdgeType, &e.SourceID, &e.TargetID, &metaStr, &e.Weight)
		if err != nil {
			return nil, err
		}
		e.Metadata = deserializeMetadata(metaStr)
		edges = append(edges, e)
	}
	return edges, nil
}

// Skill Synthesizer methods
func (sdb *SqliteDatabase) AddSkill(s *Skill) error {
	sdb.mutex.Lock()
	defer sdb.mutex.Unlock()

	s.ID = fmt.Sprintf("skill_%d", time.Now().UnixNano())
	s.CreatedAt = time.Now().Unix()

	_, err := sdb.db.Exec(`INSERT INTO skills (id, name, trigger_description, sop_content, created_at)
		VALUES (?, ?, ?, ?, ?)`, s.ID, s.Name, s.TriggerDescription, s.SOPContent, s.CreatedAt)
	return err
}

func (sdb *SqliteDatabase) GetSkills() []Skill {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return []Skill{}
	}

	rows, err := sdb.db.Query("SELECT id, name, trigger_description, sop_content, created_at FROM skills ORDER BY created_at DESC")
	if err != nil {
		fmt.Printf("[Memory Error] Failed to query skills: %v\n", err)
		return []Skill{}
	}
	defer rows.Close()

	var list []Skill
	for rows.Next() {
		var s Skill
		err := rows.Scan(&s.ID, &s.Name, &s.TriggerDescription, &s.SOPContent, &s.CreatedAt)
		if err != nil {
			fmt.Printf("[Memory Error] Failed to scan skill row: %v\n", err)
			continue
		}
		list = append(list, s)
	}
	if list == nil {
		list = []Skill{}
	}
	return list
}

// GetRelevantSkills returns skills ranked by semantic relevance to the prompt, capped at maxSkills.
// Uses the EmbeddingEngine for vector similarity when available, falling back to string-based cosine similarity.
// If maxSkills <= 0, returns all skills unfiltered.
func (sdb *SqliteDatabase) GetRelevantSkills(prompt string, maxSkills int) []Skill {
	allSkills := sdb.GetSkills()

	if maxSkills <= 0 || len(allSkills) <= maxSkills {
		return allSkills
	}

	type scoredSkill struct {
		skill Skill
		score float64
	}

	var scored []scoredSkill

	// Try embedding-based similarity first
	if sdb.EmbeddingEngine != nil {
		promptVec, err := sdb.EmbeddingEngine.Embed(context.Background(), prompt)
		if err == nil {
			for _, s := range allSkills {
				// Embed the skill's trigger description for comparison
				triggerVec, err := sdb.EmbeddingEngine.Embed(context.Background(), s.TriggerDescription+" "+s.Name)
				if err == nil && len(triggerVec) > 0 {
					sim := float64(sdb.EmbeddingEngine.CosineSimilarity(promptVec, triggerVec))
					scored = append(scored, scoredSkill{skill: s, score: sim})
				} else {
					scored = append(scored, scoredSkill{skill: s, score: 0})
				}
			}
		}
	}

	// Fallback to string-based cosine similarity if embedding engine unavailable or failed
	if len(scored) == 0 {
		for _, s := range allSkills {
			sim := embeddings.CosineSimilarity(prompt, s.TriggerDescription+" "+s.Name)
			scored = append(scored, scoredSkill{skill: s, score: sim})
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top-N
	if len(scored) > maxSkills {
		scored = scored[:maxSkills]
	}

	result := make([]Skill, len(scored))
	for i, s := range scored {
		result[i] = s.skill
	}
	return result
}
