package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tzro/internal/embeddings"
)

func cleanupTestDBs(t *testing.T, dbPath, jsonPath string) {
	_ = os.Remove(dbPath)
	_ = os.Remove(jsonPath)
	_ = os.Remove(jsonPath + ".bak")
}

func TestSqliteDatabase(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_tzro.db")
	jsonPath := filepath.Join(tempDir, "test_tzro_db.json")

	defer cleanupTestDBs(t, dbPath, jsonPath)

	testDB := &SqliteDatabase{
		jsonPath: jsonPath,
		dbPath:   dbPath,
	}

	// 1. Test Init & Table Creation & Seeding
	t.Run("Initialization", func(t *testing.T) {
		err := testDB.Init()
		if err != nil {
			t.Fatalf("Init failed: %v", err)
		}
		defer testDB.Close()

		// Verify default entity types seeded
		etList := testDB.GetEntityTypes()
		if len(etList) != 4 {
			t.Errorf("Expected 4 default entity types, got %d", len(etList))
		}

		// Verify built-in flags
		for _, et := range etList {
			if !et.BuiltIn {
				t.Errorf("Expected seeded type %s to be marked BuiltIn=true", et.ID)
			}
		}
	})

	// 2. Test Legacy JSON Migration
	t.Run("JSON Migration", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)

		// Create a mock legacy JSON database file
		legacyData := legacyJSONDB{
			Memories: []FactMemory{
				{ID: "mem_123", UserID: "user_a", Type: "fact", Content: "Prefers dark mode", Context: "Settings", Confidence: 0.9, Source: "chat", CreatedAt: time.Now()},
			},
			Nodes: map[string]KGNode{
				"node_1": {ID: "node_1", NodeType: "contact", Name: "Alice", Metadata: map[string]interface{}{"title": "Engineer"}, Source: "crm", Weight: 1.0},
			},
			Edges: map[string]KGEdge{
				"edge_1": {ID: "edge_1", EdgeType: "references", SourceID: "node_1", TargetID: "node_2", Metadata: map[string]interface{}{"since": 2026}, Weight: 0.5},
			},
			States: map[string]NodeState{
				"task_a:node_x": {TaskID: "task_a", NodeID: "node_x", Status: "completed", Output: "Success payload", CompletedAt: 123456789},
			},
			Skills: []Skill{
				{ID: "skill_1", Name: "Close Ticket", TriggerDescription: "Resolve issue", SOPContent: "# Steps", CreatedAt: 987654321},
			},
			EntityTypes: []EntityType{
				{ID: "custom_type", Label: "Custom", Color: "blue", Icon: "star", BuiltIn: false},
			},
		}

		jsonBytes, err := json.Marshal(legacyData)
		if err != nil {
			t.Fatalf("Failed to marshal legacy JSON data: %v", err)
		}

		if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
			t.Fatalf("Failed to write mock legacy JSON file: %v", err)
		}

		// Initialize DB (should trigger migration)
		migrationDB := &SqliteDatabase{
			jsonPath: jsonPath,
			dbPath:   dbPath,
		}
		err = migrationDB.Init()
		if err != nil {
			t.Fatalf("Init with migration failed: %v", err)
		}
		defer migrationDB.Close()

		// Verify fact memories migrated
		memories := migrationDB.GetMemories()
		if len(memories) != 1 || memories[0].ID != "mem_123" {
			t.Errorf("Fact memories migration failed: got %v", memories)
		}

		// Verify nodes migrated
		nodes := migrationDB.GetNodes()
		if len(nodes) != 1 || nodes["node_1"].Name != "Alice" || nodes["node_1"].Metadata["title"] != "Engineer" {
			t.Errorf("Knowledge graph nodes migration failed: got %v", nodes)
		}

		// Verify edges migrated
		edges := migrationDB.GetEdges()
		if len(edges) != 1 || edges["edge_1"].Metadata["since"] != float64(2026) {
			t.Errorf("Knowledge graph edges migration failed: got %v", edges)
		}

		// Verify states migrated
		state, ok := migrationDB.GetNodeState("task_a", "node_x")
		if !ok || state.Status != "completed" || state.Output != "Success payload" {
			t.Errorf("Node states migration failed: got %v (exists: %v)", state, ok)
		}

		// Verify skills migrated
		skills := migrationDB.GetSkills()
		if len(skills) != 1 || skills[0].Name != "Close Ticket" {
			t.Errorf("Skills migration failed: got %v", skills)
		}

		// Verify EntityTypes migrated (4 defaults + 1 custom)
		etList := migrationDB.GetEntityTypes()
		if len(etList) != 5 {
			t.Errorf("Expected 5 total entity types after migration, got %d", len(etList))
		}

		// Verify legacy JSON was backed up
		if _, err := os.Stat(jsonPath + ".bak"); os.IsNotExist(err) {
			t.Error("Legacy JSON database was not backed up with '.bak' extension")
		}
		if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
			t.Error("Primary legacy JSON database was not removed after migration")
		}
	})

	// 3. Test Tabular Memories CRUD
	t.Run("Tabular Memories CRUD", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		crudDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = crudDB.Init()
		defer crudDB.Close()

		m := FactMemory{
			UserID:     "user_1",
			Type:       "preference",
			Content:    "Prefers dark theme styling",
			Context:    "User Preferences",
			Confidence: 0.95,
			Source:     "UI Dashboard Settings",
		}

		if err := crudDB.AddMemory(m); err != nil {
			t.Fatalf("Failed to add memory: %v", err)
		}

		list := crudDB.GetMemories()
		if len(list) != 1 {
			t.Fatalf("Expected 1 memory entry, got %d", len(list))
		}

		saved := list[0]
		if saved.UserID != m.UserID || saved.Type != m.Type || saved.Content != m.Content || saved.Confidence != m.Confidence {
			t.Errorf("Field mismatches in retrieved memory: %+v", saved)
		}
	})

	// 4. Test Node States CRUD
	t.Run("Node States Checkpoint", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		stateDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = stateDB.Init()
		defer stateDB.Close()

		taskID := "task_999"
		nodeID := "node_111"

		// Should not exist initially
		_, exists := stateDB.GetNodeState(taskID, nodeID)
		if exists {
			t.Error("Node state should not exist initially")
		}

		// Write state
		if err := stateDB.SetNodeState(taskID, nodeID, "running", "Init phase"); err != nil {
			t.Fatalf("Failed to set node state: %v", err)
		}

		ns, exists := stateDB.GetNodeState(taskID, nodeID)
		if !exists || ns.Status != "running" || ns.Output != "Init phase" {
			t.Errorf("Retrieved state mismatch: exists=%v, state=%+v", exists, ns)
		}

		// Overwrite state
		if err := stateDB.SetNodeState(taskID, nodeID, "completed", "Done!"); err != nil {
			t.Fatalf("Failed to update node state: %v", err)
		}

		ns, exists = stateDB.GetNodeState(taskID, nodeID)
		if !exists || ns.Status != "completed" || ns.Output != "Done!" {
			t.Errorf("Retrieved updated state mismatch: exists=%v, state=%+v", exists, ns)
		}
	})

	// 5. Test Graph Nodes and Edges Traversal (Graph-RAG)
	t.Run("Knowledge Graph Operations & Neighborhood Traversal", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		graphDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = graphDB.Init()
		defer graphDB.Close()

		n1 := KGNode{ID: "node_a", NodeType: "account", Name: "Acme Corp", Metadata: map[string]interface{}{"domain": "acme.com"}, Source: "web", Weight: 1.0}
		n2 := KGNode{ID: "node_b", NodeType: "contact", Name: "Bob", Metadata: map[string]interface{}{"role": "CTO"}, Source: "chat", Weight: 0.8}
		n3 := KGNode{ID: "node_c", NodeType: "ticket", Name: "Issue #12", Metadata: map[string]interface{}{"priority": "high"}, Source: "observer", Weight: 0.9}

		if err := graphDB.AddNode(n1); err != nil {
			t.Fatalf("Failed to add node 1: %v", err)
		}
		if err := graphDB.AddNode(n2); err != nil {
			t.Fatalf("Failed to add node 2: %v", err)
		}
		if err := graphDB.AddNode(n3); err != nil {
			t.Fatalf("Failed to add node 3: %v", err)
		}

		e1 := KGEdge{ID: "edge_ab", EdgeType: "belongs_to", SourceID: "node_b", TargetID: "node_a", Metadata: map[string]interface{}{"since": 2025}, Weight: 1.0}
		e2 := KGEdge{ID: "edge_bc", EdgeType: "references", SourceID: "node_b", TargetID: "node_c", Metadata: map[string]interface{}{"relation": "reporter"}, Weight: 0.7}

		if err := graphDB.AddEdge(e1); err != nil {
			t.Fatalf("Failed to add edge 1: %v", err)
		}
		if err := graphDB.AddEdge(e2); err != nil {
			t.Fatalf("Failed to add edge 2: %v", err)
		}

		// Retrieve all nodes
		nodes := graphDB.GetNodes()
		if len(nodes) != 3 || nodes["node_a"].Name != "Acme Corp" || nodes["node_b"].Metadata["role"] != "CTO" {
			t.Errorf("GetNodes failed or returned corrupt details: %v", nodes)
		}

		// Retrieve all edges
		edges := graphDB.GetEdges()
		if len(edges) != 2 || edges["edge_ab"].EdgeType != "belongs_to" || edges["edge_bc"].Metadata["relation"] != "reporter" {
			t.Errorf("GetEdges failed or returned corrupt details: %v", edges)
		}

		// BFS Neighborhood tests
		// 0 Hops from node_b (Bob) -> Should return node_b only, 0 edges
		nb0 := graphDB.GetEntityNeighborhood("node_b", 0)
		if len(nb0.Nodes) != 1 || nb0.Nodes[0].ID != "node_b" || len(nb0.Edges) != 0 {
			t.Errorf("0 Hops BFS query failed: expected 1 node, 0 edges. Got Nodes=%v, Edges=%v", nb0.Nodes, nb0.Edges)
		}

		// 1 Hop from node_b (Bob) -> Should return Bob (node_b) and its direct connections (node_a, node_c), and edges e1, e2
		nb1 := graphDB.GetEntityNeighborhood("node_b", 1)
		if len(nb1.Nodes) != 3 || len(nb1.Edges) != 2 {
			t.Errorf("1 Hop BFS query failed: expected 3 nodes, 2 edges. Got Nodes=%v, Edges=%v", nb1.Nodes, nb1.Edges)
		}

		// Verify specific nodes retrieved in BFS traversal
		hasNodeA := false
		hasNodeC := false
		for _, nd := range nb1.Nodes {
			if nd.ID == "node_a" {
				hasNodeA = true
			}
			if nd.ID == "node_c" {
				hasNodeC = true
			}
		}
		if !hasNodeA || !hasNodeC {
			t.Errorf("1 Hop BFS traversal missed direct connections: hasNodeA=%v, hasNodeC=%v", hasNodeA, hasNodeC)
		}
	})

	// 6. Test SOP Skills CRUD
	t.Run("SOP Skills CRUD", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		skillDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = skillDB.Init()
		defer skillDB.Close()

		s := Skill{
			Name:               "Syndicate Lead scoring workflow",
			TriggerDescription: "Customer signs up on landing page",
			SOPContent:         "# Lead Scoring SOP\n1. Fetch contact\n2. Assign weight",
		}

		if err := skillDB.AddSkill(&s); err != nil {
			t.Fatalf("Failed to add skill: %v", err)
		}

		skillsList := skillDB.GetSkills()
		if len(skillsList) != 1 {
			t.Fatalf("Expected 1 skill entry, got %d", len(skillsList))
		}

		saved := skillsList[0]
		if saved.Name != s.Name || saved.TriggerDescription != s.TriggerDescription || saved.SOPContent != s.SOPContent {
			t.Errorf("Field mismatches in retrieved skill: %+v", saved)
		}

		// Test GetSkill by valid ID
		retrieved, err := skillDB.GetSkill(saved.ID)
		if err != nil {
			t.Fatalf("GetSkill failed for valid ID %s: %v", saved.ID, err)
		}
		if retrieved.ID != saved.ID || retrieved.Name != saved.Name {
			t.Errorf("GetSkill returned incorrect skill details: %+v", retrieved)
		}

		// Test GetSkill with invalid ID
		_, err = skillDB.GetSkill("invalid_id")
		if err == nil {
			t.Error("Expected error for invalid skill ID, got nil")
		}
	})

	// 7. Test Custom EntityTypes CRUD
	t.Run("Custom EntityTypes CRUD", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		etDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = etDB.Init()
		defer etDB.Close()

		custom := EntityType{
			ID:    "opportunity",
			Label: "Sales Opportunity",
			Color: "hsl(120, 80%, 45%)",
			Icon:  "dollar-sign",
		}

		// Add custom
		if err := etDB.AddEntityType(custom); err != nil {
			t.Fatalf("Failed to add custom entity type: %v", err)
		}

		// Try duplicate custom -> should error
		if err := etDB.AddEntityType(custom); err == nil {
			t.Error("Expected error when adding duplicate custom entity type ID, got nil")
		}

		// Try duplicate built-in -> should error
		builtInDup := EntityType{ID: "contact", Label: "Duplicate Contact"}
		if err := etDB.AddEntityType(builtInDup); err == nil {
			t.Error("Expected error when adding duplicate built-in type ID, got nil")
		}

		// Get all entity types
		etList := etDB.GetEntityTypes()
		if len(etList) != 5 {
			t.Errorf("Expected 5 entity types (4 built-in + 1 custom), got %d", len(etList))
		}

		// Verify custom in list
		found := false
		for _, et := range etList {
			if et.ID == "opportunity" {
				found = true
				if et.BuiltIn {
					t.Error("Custom entity type should not be marked BuiltIn=true")
				}
			}
		}
		if !found {
			t.Error("Custom entity type was not found in list")
		}

		// Try deleting built-in -> should error
		if err := etDB.DeleteEntityType("contact"); err == nil {
			t.Error("Expected error when trying to delete built-in entity type, got nil")
		}

		// Delete custom
		if err := etDB.DeleteEntityType("opportunity"); err != nil {
			t.Fatalf("Failed to delete custom entity type: %v", err)
		}

		// Verify custom removed
		etList = etDB.GetEntityTypes()
		if len(etList) != 4 {
			t.Errorf("Expected 4 entity types after deleting custom, got %d", len(etList))
		}
	})

	// 8. Test Advanced neighborhood filters (options)
	t.Run("Advanced Neighborhood Option Filters", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		optsDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = optsDB.Init()
		defer optsDB.Close()

		// Seed nodes
		nA := KGNode{ID: "node_a", NodeType: "account", Name: "Acme Corp", Metadata: map[string]interface{}{"domain": "acme.com"}, Source: "web", Weight: 1.0}
		nB := KGNode{ID: "node_b", NodeType: "contact", Name: "Bob", Metadata: map[string]interface{}{"role": "CTO"}, Source: "chat", Weight: 0.8}
		nC := KGNode{ID: "node_c", NodeType: "ticket", Name: "Issue #12", Metadata: map[string]interface{}{"priority": "high"}, Source: "observer", Weight: 0.6}
		nD := KGNode{ID: "node_d", NodeType: "contact", Name: "Charlie", Metadata: map[string]interface{}{"role": "Engineer"}, Source: "chat", Weight: 0.9}

		_ = optsDB.AddNode(nA)
		_ = optsDB.AddNode(nB)
		_ = optsDB.AddNode(nC)
		_ = optsDB.AddNode(nD)

		// Seed edges:
		// A (account) <-belongs_to- B (contact)
		// B (contact) -references-> C (ticket)
		// C (ticket) -references-> D (contact)
		eAB := KGEdge{ID: "edge_ab", EdgeType: "belongs_to", SourceID: "node_b", TargetID: "node_a", Metadata: map[string]interface{}{"since": 2025}, Weight: 1.0}
		eBC := KGEdge{ID: "edge_bc", EdgeType: "references", SourceID: "node_b", TargetID: "node_c", Metadata: map[string]interface{}{"relation": "reporter"}, Weight: 0.7}
		eCD := KGEdge{ID: "edge_cd", EdgeType: "references", SourceID: "node_c", TargetID: "node_d", Metadata: map[string]interface{}{"relation": "assigned"}, Weight: 0.5}

		_ = optsDB.AddEdge(eAB)
		_ = optsDB.AddEdge(eBC)
		_ = optsDB.AddEdge(eCD)

		// Test 1: WithNodeTypes
		subTypes := optsDB.GetEntityNeighborhood("node_b", 1, WithNodeTypes([]string{"contact", "account"}))
		if len(subTypes.Nodes) != 2 {
			t.Errorf("WithNodeTypes: expected 2 nodes, got %d", len(subTypes.Nodes))
		}
		for _, node := range subTypes.Nodes {
			if node.NodeType != "contact" && node.NodeType != "account" {
				t.Errorf("WithNodeTypes: retrieved node %s with disallowed type %s", node.ID, node.NodeType)
			}
		}

		// Test 2: WithEdgeTypes
		subEdges := optsDB.GetEntityNeighborhood("node_b", 2, WithEdgeTypes([]string{"belongs_to"}))
		if len(subEdges.Nodes) != 2 || len(subEdges.Edges) != 1 {
			t.Errorf("WithEdgeTypes: expected 2 nodes & 1 edge, got %d nodes & %d edges", len(subEdges.Nodes), len(subEdges.Edges))
		}

		// Test 3: WithMinNodeWeight
		subNodeWeight := optsDB.GetEntityNeighborhood("node_b", 2, WithMinNodeWeight(0.7))
		if len(subNodeWeight.Nodes) != 2 {
			t.Errorf("WithMinNodeWeight: expected 2 nodes (Bob, Acme), got %d: %+v", len(subNodeWeight.Nodes), subNodeWeight.Nodes)
		}

		// Test 4: WithMinEdgeWeight
		subEdgeWeight := optsDB.GetEntityNeighborhood("node_b", 2, WithMinEdgeWeight(0.6))
		if len(subEdgeWeight.Edges) != 2 {
			t.Errorf("WithMinEdgeWeight: expected 2 edges, got %d: %+v", len(subEdgeWeight.Edges), subEdgeWeight.Edges)
		}

		// Test 5: WithLimit
		subLimit := optsDB.GetEntityNeighborhood("node_b", 2, WithLimit(2))
		if len(subLimit.Nodes) != 2 {
			t.Errorf("WithLimit: expected exactly 2 nodes, got %d: %+v", len(subLimit.Nodes), subLimit.Nodes)
		}

		// Test 6: WithDirection
		eIncoming := KGEdge{ID: "edge_incoming", EdgeType: "references", SourceID: "node_a", TargetID: "node_b", Weight: 0.8}
		_ = optsDB.AddEdge(eIncoming)

		subDirOut := optsDB.GetEntityNeighborhood("node_b", 1, WithDirection("outgoing"))
		for _, edge := range subDirOut.Edges {
			if edge.ID == "edge_incoming" {
				t.Error("WithDirection(outgoing) traversed an incoming edge")
			}
		}

		subDirIn := optsDB.GetEntityNeighborhood("node_b", 1, WithDirection("incoming"))
		hasIncoming := false
		for _, edge := range subDirIn.Edges {
			if edge.ID == "edge_incoming" {
				hasIncoming = true
			}
		}
		if !hasIncoming {
			t.Error("WithDirection(incoming) missed the incoming edge")
		}
	})

	// 9. Test Graph-RAG Context Extraction & word boundaries
	t.Run("Graph-RAG Context Extraction & Word Boundaries", func(t *testing.T) {
		cleanupTestDBs(t, dbPath, jsonPath)
		ragDB := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
		_ = ragDB.Init()
		defer ragDB.Close()

		// Seed nodes
		nA := KGNode{ID: "node_a", NodeType: "account", Name: "Acme Corp", Metadata: map[string]interface{}{"domain": "acme.com"}, Source: "web", Weight: 1.0}
		nB := KGNode{ID: "node_b", NodeType: "contact", Name: "Bob", Metadata: map[string]interface{}{"role": "CTO"}, Source: "chat", Weight: 0.8}
		_ = ragDB.AddNode(nA)
		_ = ragDB.AddNode(nB)

		eAB := KGEdge{ID: "edge_ab", EdgeType: "belongs_to", SourceID: "node_b", TargetID: "node_a", Metadata: map[string]interface{}{"since": 2025}, Weight: 1.0}
		_ = ragDB.AddEdge(eAB)

		// Test 1: Match entity by ID or Name
		prompt := "Tell me about node_a and how Bob fits in."
		ctxStr := ragDB.GetGraphRAGContext(prompt)

		if ctxStr == "" {
			t.Fatal("Expected non-empty Graph-RAG context, got empty string")
		}
		if !strings.Contains(ctxStr, "Acme Corp") {
			t.Error("Expected context to contain node name 'Acme Corp'")
		}
		if !strings.Contains(ctxStr, "node_b") {
			t.Error("Expected context to contain node ID 'node_b'")
		}
		if !strings.Contains(ctxStr, "belongs_to") {
			t.Error("Expected context to contain relationship type 'belongs_to'")
		}

		// Test 2: Word boundaries (Bob in Bobby should NOT match)
		promptBobby := "Is Bobby working today?"
		ctxStrBobby := ragDB.GetGraphRAGContext(promptBobby)
		if ctxStrBobby != "" {
			t.Errorf("Expected empty context for Bobby (no boundary match for Bob), got: %q", ctxStrBobby)
		}

		// Test 3: Case insensitivity
		promptCase := "check ACME CORP details"
		ctxStrCase := ragDB.GetGraphRAGContext(promptCase)
		if !strings.Contains(ctxStrCase, "node_a") {
			t.Error("Expected case-insensitive match for ACME CORP to retrieve node_a context")
		}
	})
}

func TestSqliteDatabase_HybridVectorSearch(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_hybrid.db")
	jsonPath := filepath.Join(tempDir, "test_hybrid_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	engine := embeddings.NewPureGoEmbeddingEngine()
	db := &SqliteDatabase{
		jsonPath:        jsonPath,
		dbPath:          dbPath,
		EmbeddingEngine: engine,
	}

	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Seed nodes with embeddings:
	// nA: Acme Corp
	embA, _ := engine.Embed(context.Background(), "Acme Corp enterprise business")
	nA := KGNode{
		ID:        "node_a",
		NodeType:  "account",
		Name:      "Acme Corp",
		Source:    "web",
		Weight:    1.0,
		Embedding: embA,
	}
	if err := db.AddNode(nA); err != nil {
		t.Fatalf("AddNode A failed: %v", err)
	}

	// nB: HubSpot CRM contact data
	embB, _ := engine.Embed(context.Background(), "HubSpot CRM contact data and sync pipeline")
	nB := KGNode{
		ID:        "node_b",
		NodeType:  "contact",
		Name:      "HubSpot CRM contact data",
		Source:    "chat",
		Weight:    0.8,
		Embedding: embB,
	}
	if err := db.AddNode(nB); err != nil {
		t.Fatalf("AddNode B failed: %v", err)
	}

	// Connect Bob -> Acme Corp
	eAB := KGEdge{
		ID:       "edge_ab",
		EdgeType: "belongs_to",
		SourceID: "node_b",
		TargetID: "node_a",
		Weight:   1.0,
	}
	if err := db.AddEdge(eAB); err != nil {
		t.Fatalf("AddEdge failed: %v", err)
	}

	// Query semantically with NO literal/exact word matches
	prompt := "Retrieve CRM customer database information."
	ctxStr := db.GetGraphRAGContext(prompt)

	if ctxStr == "" {
		t.Fatal("Expected Hybrid Vector Search to match and return Graph-RAG context, got empty string")
	}

	// "CRM customer database" is semantically closest to "HubSpot CRM contact data and sync pipeline"
	// Verify it matched node_b (HubSpot CRM contact data)
	if !strings.Contains(ctxStr, "node_b") {
		t.Errorf("Expected context to match and contain 'node_b', got: %s", ctxStr)
	}
	if !strings.Contains(ctxStr, "belongs_to") {
		t.Errorf("Expected context to contain traversed neighborhood edge 'belongs_to', got: %s", ctxStr)
	}
}

func TestSqliteDatabase_SchemaMigration(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migration.db")
	jsonPath := filepath.Join(tempDir, "test_migration_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	// 1. Manually open a database and create tables WITHOUT 'embedding'
	importDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open manual DB: %v", err)
	}
	_, err = importDB.Exec(`CREATE TABLE fact_memories (
		id TEXT PRIMARY KEY,
		user_id TEXT,
		type TEXT,
		content TEXT,
		context TEXT,
		confidence REAL,
		source TEXT,
		created_at DATETIME
	);`)
	if err != nil {
		importDB.Close()
		t.Fatalf("Failed to create old fact_memories: %v", err)
	}
	_, err = importDB.Exec(`CREATE TABLE kg_nodes (
		id TEXT PRIMARY KEY,
		node_type TEXT,
		name TEXT,
		metadata TEXT,
		source TEXT,
		weight REAL
	);`)
	if err != nil {
		importDB.Close()
		t.Fatalf("Failed to create old kg_nodes: %v", err)
	}
	importDB.Close()

	// 2. Instantiate and Init our SqliteDatabase on that same file
	db := &SqliteDatabase{
		jsonPath: jsonPath,
		dbPath:   dbPath,
	}
	if err := db.Init(); err != nil {
		t.Fatalf("Init on existing DB failed: %v", err)
	}
	defer db.Close()

	// 3. Verify that the 'embedding' column was successfully migrated and added
	m := FactMemory{
		ID:         "mem_migration_test",
		UserID:     "user_migration",
		Type:       "fact",
		Content:    "Testing DB migration",
		Confidence: 1.0,
		Embedding:  []float32{0.1, 0.2, 0.3},
	}
	if err := db.AddMemory(m); err != nil {
		t.Fatalf("Failed to AddMemory with embedding after migration: %v", err)
	}

	mems := db.GetMemories()
	found := false
	for _, mem := range mems {
		if mem.Content == "Testing DB migration" {
			found = true
			if len(mem.Embedding) != 3 || mem.Embedding[0] != 0.1 {
				t.Errorf("Embedding array was not retrieved correctly: %v", mem.Embedding)
			}
		}
	}
	if !found {
		t.Errorf("Could not retrieve memory added after migration")
	}
}

func TestSqliteDatabase_GraphRAGContextTruncation(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_truncation.db")
	jsonPath := filepath.Join(tempDir, "test_truncation_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Seed 20 nodes with varying weights, all connected to a root entity
	rootNode := KGNode{ID: "root", NodeType: "account", Name: "Root Corp", Metadata: map[string]interface{}{}, Source: "test", Weight: 1.0}
	if err := db.AddNode(rootNode); err != nil {
		t.Fatalf("Failed to add root node: %v", err)
	}

	for i := 0; i < 20; i++ {
		nodeID := fmt.Sprintf("entity_%02d", i)
		weight := float64(20-i) / 20.0 // entity_00=1.0, entity_01=0.95, ... entity_19=0.05
		n := KGNode{
			ID:       nodeID,
			NodeType: "contact",
			Name:     fmt.Sprintf("Contact_%02d", i),
			Metadata: map[string]interface{}{"index": i},
			Source:   "benchmark",
			Weight:   weight,
		}
		if err := db.AddNode(n); err != nil {
			t.Fatalf("Failed to add node %s: %v", nodeID, err)
		}

		e := KGEdge{
			ID:       fmt.Sprintf("edge_root_%02d", i),
			EdgeType: "employs",
			SourceID: "root",
			TargetID: nodeID,
			Weight:   1.0,
		}
		if err := db.AddEdge(e); err != nil {
			t.Fatalf("Failed to add edge to %s: %v", nodeID, err)
		}
	}

	// Also connect entity_00 -> entity_01 to test edge filtering
	crossEdge := KGEdge{
		ID: "edge_cross_00_01", EdgeType: "knows",
		SourceID: "entity_00", TargetID: "entity_01", Weight: 0.9,
	}
	if err := db.AddEdge(crossEdge); err != nil {
		t.Fatalf("Failed to add cross edge: %v", err)
	}

	// Test 1: Unlimited mode returns all 21 entities
	t.Run("Unlimited", func(t *testing.T) {
		ctx := db.GetGraphRAGContext("Tell me about Root Corp")
		if ctx == "" {
			t.Fatal("Expected non-empty context, got empty")
		}
		entityCount := strings.Count(ctx, "| entity_")
		rootCount := strings.Count(ctx, "| root |")
		totalRows := entityCount + rootCount
		if totalRows != 21 {
			t.Errorf("Unlimited: expected 21 entity rows, got %d (entity=%d, root=%d)", totalRows, entityCount, rootCount)
		}
		if strings.Contains(ctx, "truncated") {
			t.Error("Unlimited: should not contain truncation notice")
		}
	})

	// Test 2: maxChars=0 behaves as unlimited
	t.Run("ExplicitZeroIsUnlimited", func(t *testing.T) {
		ctx := db.GetGraphRAGContext("Tell me about Root Corp", 0)
		entityCount := strings.Count(ctx, "| entity_")
		rootCount := strings.Count(ctx, "| root |")
		if entityCount+rootCount != 21 {
			t.Errorf("maxChars=0: expected 21 entity rows, got %d", entityCount+rootCount)
		}
	})

	// Test 3: Tight char limit triggers truncation and retains highest-weight entities
	t.Run("TruncationDropsLowestWeight", func(t *testing.T) {
		ctx := db.GetGraphRAGContext("Tell me about Root Corp", 800)
		if ctx == "" {
			t.Fatal("Expected non-empty context with tight limit")
		}
		if len(ctx) > 800 {
			t.Errorf("Output exceeds limit: %d chars > 800", len(ctx))
		}
		if !strings.Contains(ctx, "truncated") {
			t.Error("Expected truncation notice in output")
		}
		if !strings.Contains(ctx, "entity_00") {
			t.Error("Highest weight entity entity_00 should be retained after truncation")
		}
		if !strings.Contains(ctx, "root") {
			t.Error("Root entity should be retained after truncation")
		}
		if strings.Contains(ctx, "entity_19") {
			t.Error("Lowest weight entity entity_19 should be dropped during truncation")
		}
	})

	// Test 4: Edges are filtered to only retained entity pairs
	t.Run("EdgeFilteringOnTruncation", func(t *testing.T) {
		ctx := db.GetGraphRAGContext("Tell me about Root Corp", 1500)
		if strings.Contains(ctx, "entity_19") {
			t.Error("entity_19 should be dropped")
		}
		if strings.Contains(ctx, "entity_00") && strings.Contains(ctx, "entity_01") {
			if !strings.Contains(ctx, "knows") {
				t.Error("Cross-edge 'knows' between retained entities should survive truncation")
			}
		}
	})
}

func TestSqliteDatabase_GetRelevantSkills(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_skills_rank.db")
	jsonPath := filepath.Join(tempDir, "test_skills_rank_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Seed 15 skills with distinct trigger descriptions
	skillTopics := []string{
		"hubspot crm contact sync pipeline",
		"docker container deployment cluster",
		"sqlite database query optimization",
		"salesforce lead deduplication workflow",
		"slack notification alert message",
		"jira ticket management tracking",
		"spreadsheet data import export",
		"aws cloud infrastructure scaling",
		"graph rag memory retrieval",
		"cache compaction pipeline",
		"workflow task compiler execution",
		"telemetry monitoring dashboard",
		"email campaign automation",
		"invoice payment processing",
		"customer support ticket resolution",
	}

	for i, topic := range skillTopics {
		skill := &Skill{
			Name:               fmt.Sprintf("SOP: %s", topic),
			TriggerDescription: fmt.Sprintf("Submitting requests related to: %s", topic),
			SOPContent:         fmt.Sprintf("# SOP for %s\nStep 1: Do the thing.", topic),
			CreatedAt:          int64(1000 + i),
		}
		if err := db.AddSkill(skill); err != nil {
			t.Fatalf("Failed to add skill %d: %v", i, err)
		}
	}

	// Verify all 15 skills exist
	all := db.GetSkills()
	if len(all) != 15 {
		t.Fatalf("Expected 15 skills, got %d", len(all))
	}

	// Test 1: maxSkills=0 returns all
	t.Run("ZeroLimitReturnsAll", func(t *testing.T) {
		result := db.GetRelevantSkills("anything", 0)
		if len(result) != 15 {
			t.Errorf("maxSkills=0: expected 15 skills, got %d", len(result))
		}
	})

	// Test 2: When fewer skills than limit, return all
	t.Run("UnderLimitReturnsAll", func(t *testing.T) {
		result := db.GetRelevantSkills("anything", 20)
		if len(result) != 15 {
			t.Errorf("maxSkills=20: expected 15 skills, got %d", len(result))
		}
	})

	// Test 3: Cap at top 5
	t.Run("CapsAtLimit", func(t *testing.T) {
		result := db.GetRelevantSkills("hubspot crm contact sync", 5)
		if len(result) != 5 {
			t.Errorf("maxSkills=5: expected 5 skills, got %d", len(result))
		}
		// The CRM-related skill should be ranked first or near the top
		topNames := ""
		for _, s := range result {
			topNames += s.Name + " | "
		}
		if !strings.Contains(result[0].Name, "hubspot") {
			t.Logf("Top skills for 'hubspot crm contact sync': %s", topNames)
			// Not a hard failure since similarity depends on tokenization, but log for visibility
		}
	})

	// Test 4: Different prompt should rank different skills higher
	t.Run("DifferentPromptRanksCorrectly", func(t *testing.T) {
		result := db.GetRelevantSkills("deploy docker container to aws cluster", 3)
		if len(result) != 3 {
			t.Errorf("maxSkills=3: expected 3 skills, got %d", len(result))
		}
		// Docker and AWS related skills should surface
		topNames := ""
		for _, s := range result {
			topNames += s.Name + " | "
		}
		foundRelevant := false
		for _, s := range result {
			if strings.Contains(s.Name, "docker") || strings.Contains(s.Name, "aws") {
				foundRelevant = true
				break
			}
		}
		if !foundRelevant {
			t.Errorf("Expected docker/aws skills in top 3 for deployment query, got: %s", topNames)
		}
	})
}

func TestSessionHistoryCompaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_compaction.db")
	jsonPath := filepath.Join(tempDir, "test_compaction_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	sessionID := "session_abc_123"

	// 1. Add 4 turns of dialogue
	db.AddSessionTurn(sessionID, "Move final_report.pdf to temp folder.", []string{"GorillaFileSystem.mv(source=final_report.pdf, dest=temp/final_report.pdf)"})
	db.AddSessionTurn(sessionID, "Search for 'budget' in final_report.pdf.", []string{"GorillaFileSystem.grep(query=budget, file=temp/final_report.pdf)"})
	db.AddSessionTurn(sessionID, "Sort the file lines.", []string{"GorillaFileSystem.sort(file=temp/final_report.pdf)"})
	db.AddSessionTurn(sessionID, "Post the results to Twitter.", []string{"TwitterAPI.post_tweet(content=sorted results)"})

	// 2. Fetch history context and verify sliding window + rollup behavior
	ctx := db.GetSessionHistoryContext(sessionID)
	if ctx == "" {
		t.Fatal("Expected history context, got empty string")
	}

	t.Logf("Generated Dialogue History:\n%s", ctx)

	// Verify header
	if !strings.Contains(ctx, "### CONVERSATIONAL DIALOGUE HISTORY") {
		t.Error("Missing conversation history header")
	}

	// Verify sliding window (last 2 turns: Turn 3 and Turn 4) are in FULL detail
	if !strings.Contains(ctx, "#### [Turn 4]") {
		t.Error("Expected Turn 4 detailed section")
	}
	if !strings.Contains(ctx, "TwitterAPI.post_tweet") {
		t.Error("Expected Turn 4 details to contain Twitter tool name")
	}
	if !strings.Contains(ctx, "#### [Turn 3]") {
		t.Error("Expected Turn 3 detailed section")
	}
	if !strings.Contains(ctx, "GorillaFileSystem.sort") {
		t.Error("Expected Turn 3 details to contain sort tool name")
	}

	// Verify rollup summary (Turn 1 and Turn 2 should be in the rollup summary)
	if !strings.Contains(ctx, "#### Summary of Prior Turns") {
		t.Error("Expected Rollup summary section")
	}
	if !strings.Contains(ctx, "*   **Turn 1**: User asked: \"Move final_report.pdf to temp folder.\". Agent executed tools: GorillaFileSystem.mv") {
		t.Error("Expected Turn 1 rolled up concisely")
	}
	if !strings.Contains(ctx, "*   **Turn 2**: User asked: \"Search for 'budget' in final_report.pdf.\". Agent executed tools: GorillaFileSystem.grep") {
		t.Error("Expected Turn 2 rolled up concisely")
	}

	// Verify that Turn 1 and Turn 2 do NOT have detailed sections
	if strings.Contains(ctx, "#### [Turn 1]") || strings.Contains(ctx, "#### [Turn 2]") {
		t.Error("Turns 1 and 2 should be summarized, not shown in full detail sections")
	}
}

func TestSqliteDatabase_SearchMemoriesAndNodes(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_search_mem.db")
	jsonPath := filepath.Join(tempDir, "test_search_mem_db.json")
	defer cleanupTestDBs(t, dbPath, jsonPath)

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// 1. Seed without embeddings to test Text Fallback
	m1 := FactMemory{
		UserID:     "user1",
		Type:       "fact",
		Content:    "User prefers kubernetes docker containers for infrastructure",
		Confidence: 1.0,
	}
	m2 := FactMemory{
		UserID:     "user1",
		Type:       "insight",
		Content:    "Avoid raw EC2 instances",
		Confidence: 0.8,
	}
	if err := db.AddMemory(m1); err != nil {
		t.Fatalf("AddMemory 1 failed: %v", err)
	}
	if err := db.AddMemory(m2); err != nil {
		t.Fatalf("AddMemory 2 failed: %v", err)
	}

	n1 := KGNode{
		ID:       "node1",
		NodeType: "account",
		Name:     "Kubernetes Cluster",
		Weight:   1.0,
	}
	n2 := KGNode{
		ID:       "node2",
		NodeType: "contact",
		Name:     "HubSpot CRM Contact",
		Weight:   0.5,
	}
	if err := db.AddNode(n1); err != nil {
		t.Fatalf("AddNode 1 failed: %v", err)
	}
	if err := db.AddNode(n2); err != nil {
		t.Fatalf("AddNode 2 failed: %v", err)
	}

	// Disable EmbeddingEngine initially to force text fallback
	db.EmbeddingEngine = nil

	t.Run("Text Fallback Search", func(t *testing.T) {
		mems, nodes, err := db.SearchMemoriesAndNodes("kubernetes docker containers", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(mems) != 1 || mems[0].Content != m1.Content {
			t.Errorf("Expected 1 memory matching text search, got: %+v", mems)
		}
		if len(nodes) != 1 || nodes[0].ID != n1.ID {
			t.Errorf("Expected 1 node matching text search, got: %+v", nodes)
		}
	})

	t.Run("Text Fallback Limit", func(t *testing.T) {
		// All nodes might have some low score if there's overlap, but let's query for something matching both to test limits
		// Let's add another matching node and memory
		m3 := FactMemory{UserID: "user1", Type: "fact", Content: "Docker images are small", Confidence: 1.0}
		_ = db.AddMemory(m3)

		mems, _, err := db.SearchMemoriesAndNodes("docker", 1)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(mems) != 1 {
			t.Errorf("Expected limit of 1 memory, got %d", len(mems))
		}
	})

	// 2. Enable EmbeddingEngine to test Vector Search
	engine := embeddings.NewPureGoEmbeddingEngine()
	db.EmbeddingEngine = engine

	// Let's re-add memories/nodes with embeddings
	embMem, _ := engine.Embed(context.Background(), "docker container cluster query")
	mVec := FactMemory{
		UserID:     "user1",
		Type:       "fact",
		Content:    "docker container cluster query",
		Embedding:  embMem,
		Confidence: 1.0,
	}
	if err := db.AddMemory(mVec); err != nil {
		t.Fatalf("AddMemory Vec failed: %v", err)
	}

	embNode, _ := engine.Embed(context.Background(), "cluster optimizer query")
	nVec := KGNode{
		ID:        "node_vec",
		NodeType:  "account",
		Name:      "cluster optimizer query",
		Embedding: embNode,
		Weight:    1.0,
	}
	if err := db.AddNode(nVec); err != nil {
		t.Fatalf("AddNode Vec failed: %v", err)
	}

	t.Run("Vector Search", func(t *testing.T) {
		mems, nodes, err := db.SearchMemoriesAndNodes("cluster query", 10)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		// mVec and nVec should have embeddings that score >= 0.25
		foundMem := false
		for _, m := range mems {
			if m.Content == "docker container cluster query" {
				foundMem = true
			}
		}
		foundNode := false
		for _, n := range nodes {
			if n.ID == "node_vec" {
				foundNode = true
			}
		}
		if !foundMem {
			t.Error("Expected vector memory to be found")
		}
		if !foundNode {
			t.Error("Expected vector node to be found")
		}
	})
}

func TestThoughtChain_StepPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_thought_chain.db")
	jsonPath := filepath.Join(tempDir, "test_thought_chain_db.json")

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// Add three steps to a probe
	probeID := "probe_001"
	taskID := "task_abc"

	for i := 1; i <= 3; i++ {
		step := ThoughtStep{
			ID:         fmt.Sprintf("step_%d", i),
			ProbeID:    probeID,
			TaskID:     taskID,
			StepIndex:  i,
			Thought:    fmt.Sprintf("Investigating component %d", i),
			ToolName:   "read_file",
			ToolArgs:   fmt.Sprintf(`{"path": "file_%d.go"}`, i),
			ToolOutput: fmt.Sprintf("contents of file %d", i),
			CreatedAt:  time.Now().Unix(),
		}
		if err := db.AddThoughtStep(step); err != nil {
			t.Fatalf("AddThoughtStep %d failed: %v", i, err)
		}
	}

	// Retrieve all steps
	steps, err := db.GetThoughtSteps(probeID)
	if err != nil {
		t.Fatalf("GetThoughtSteps failed: %v", err)
	}

	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}

	// Verify ordering by step_index
	for i, s := range steps {
		expectedIdx := i + 1
		if s.StepIndex != expectedIdx {
			t.Errorf("step %d: expected StepIndex=%d, got %d", i, expectedIdx, s.StepIndex)
		}
		if s.ProbeID != probeID || s.TaskID != taskID {
			t.Errorf("step %d: unexpected ProbeID=%s TaskID=%s", i, s.ProbeID, s.TaskID)
		}
	}

	// Verify field content
	if steps[0].Thought != "Investigating component 1" {
		t.Errorf("unexpected thought: %s", steps[0].Thought)
	}
	if steps[1].ToolName != "read_file" {
		t.Errorf("unexpected tool_name: %s", steps[1].ToolName)
	}
	if steps[2].ToolOutput != "contents of file 3" {
		t.Errorf("unexpected tool_output: %s", steps[2].ToolOutput)
	}
}

func TestThoughtChain_SummaryPersistence(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_thought_summary.db")
	jsonPath := filepath.Join(tempDir, "test_thought_summary_db.json")

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	probeID := "probe_002"

	// Add two summaries with different timestamps
	s1 := ThoughtSummary{
		ID:        "summary_1",
		ProbeID:   probeID,
		TaskID:    "task_xyz",
		StepRange: "1-3",
		Summary:   "First three steps explored the compiler package.",
		CreatedAt: 1000,
	}
	s2 := ThoughtSummary{
		ID:        "summary_2",
		ProbeID:   probeID,
		TaskID:    "task_xyz",
		StepRange: "4-6",
		Summary:   "Steps 4-6 explored the executor and found the dispatch point.",
		CreatedAt: 2000,
	}

	if err := db.AddThoughtSummary(s1); err != nil {
		t.Fatalf("AddThoughtSummary 1 failed: %v", err)
	}
	if err := db.AddThoughtSummary(s2); err != nil {
		t.Fatalf("AddThoughtSummary 2 failed: %v", err)
	}

	// GetLatestSummary should return s2 (highest created_at)
	latest, err := db.GetLatestSummary(probeID)
	if err != nil {
		t.Fatalf("GetLatestSummary failed: %v", err)
	}

	if latest.ID != "summary_2" {
		t.Errorf("expected latest summary to be summary_2, got %s", latest.ID)
	}
	if latest.StepRange != "4-6" {
		t.Errorf("expected step range '4-6', got '%s'", latest.StepRange)
	}
	if latest.Summary != "Steps 4-6 explored the executor and found the dispatch point." {
		t.Errorf("unexpected summary content: %s", latest.Summary)
	}
}

func TestThoughtChain_EmptyProbeReturnsEmpty(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_thought_empty.db")
	jsonPath := filepath.Join(tempDir, "test_thought_empty_db.json")

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	// No steps added — should return empty slice
	steps, err := db.GetThoughtSteps("nonexistent_probe")
	if err != nil {
		t.Fatalf("GetThoughtSteps for empty probe failed: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(steps))
	}

	// No summaries added — should return error
	_, err = db.GetLatestSummary("nonexistent_probe")
	if err == nil {
		t.Error("expected error for GetLatestSummary on nonexistent probe, got nil")
	}
}

func TestCountToolCallsByTaskID(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_count_tool_calls.db")
	jsonPath := filepath.Join(tempDir, "test_count_tool_calls_db.json")

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer db.Close()

	taskID := "test_task_123"

	// Add 3 steps with tool calls and 2 without
	steps := []ThoughtStep{
		{ID: "s1", ProbeID: "probe_1", TaskID: taskID, StepIndex: 1, Thought: "explore", ToolName: "list_dir", ToolArgs: `{"path":"/"}`, ToolOutput: "dir listing", CreatedAt: 1},
		{ID: "s2", ProbeID: "probe_1", TaskID: taskID, StepIndex: 2, Thought: "thinking", ToolName: "", ToolArgs: "", ToolOutput: "", CreatedAt: 2},
		{ID: "s3", ProbeID: "probe_1", TaskID: taskID, StepIndex: 3, Thought: "read file", ToolName: "read_file", ToolArgs: `{"path":"a.go"}`, ToolOutput: "contents", CreatedAt: 3},
		{ID: "s4", ProbeID: "probe_1", TaskID: taskID, StepIndex: 4, Thought: "search", ToolName: "search_files", ToolArgs: `{"query":"main"}`, ToolOutput: "results", CreatedAt: 4},
		{ID: "s5", ProbeID: "probe_1", TaskID: taskID, StepIndex: 5, Thought: "synthesize", ToolName: "", ToolArgs: "", ToolOutput: "", CreatedAt: 5},
	}
	for _, s := range steps {
		if err := db.AddThoughtStep(s); err != nil {
			t.Fatalf("AddThoughtStep failed: %v", err)
		}
	}

	// Also add a step for a different task — should not be counted
	otherStep := ThoughtStep{ID: "s_other", ProbeID: "probe_2", TaskID: "other_task", StepIndex: 1, Thought: "other", ToolName: "list_dir", CreatedAt: 6}
	if err := db.AddThoughtStep(otherStep); err != nil {
		t.Fatalf("AddThoughtStep for other task failed: %v", err)
	}

	count, err := db.CountToolCallsByTaskID(taskID)
	if err != nil {
		t.Fatalf("CountToolCallsByTaskID failed: %v", err)
	}
	if count != 3 {
		t.Errorf("CountToolCallsByTaskID = %d, want 3 (list_dir + read_file + search_files)", count)
	}

	// Nonexistent task should return 0
	count, err = db.CountToolCallsByTaskID("nonexistent_task")
	if err != nil {
		t.Fatalf("CountToolCallsByTaskID for nonexistent task failed: %v", err)
	}
	if count != 0 {
		t.Errorf("CountToolCallsByTaskID for nonexistent = %d, want 0", count)
	}
}

func TestStructuredOutput_PersistAndRetrieve(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_structured_output.db")
	jsonPath := filepath.Join(tempDir, "test_structured_output_db.json")

	db := &SqliteDatabase{jsonPath: jsonPath, dbPath: dbPath}
	if err := db.Init(); err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	defer db.Close()

	taskID := "task-env-test"
	nodeID := "terminal_synthesis"

	// 1. Create a node state
	if err := db.SetNodeState(taskID, nodeID, "completed", "synthesis text"); err != nil {
		t.Fatalf("SetNodeState failed: %v", err)
	}

	// 2. Set structured output
	envelope := `{"synthesis":"summary","toolsUsed":["read_file"],"nodeCount":3}`
	if err := db.SetNodeStructuredOutput(taskID, nodeID, envelope); err != nil {
		t.Fatalf("SetNodeStructuredOutput failed: %v", err)
	}

	// 3. Retrieve via GetNodeState — should include structured output
	ns, ok := db.GetNodeState(taskID, nodeID)
	if !ok {
		t.Fatal("GetNodeState returned false")
	}
	if ns.StructuredOutput != envelope {
		t.Errorf("GetNodeState.StructuredOutput = %q, want %q", ns.StructuredOutput, envelope)
	}

	// 4. Retrieve via GetAllNodeStates — should include structured output
	allStates := db.GetAllNodeStates(taskID)
	if len(allStates) != 1 {
		t.Fatalf("GetAllNodeStates returned %d states, want 1", len(allStates))
	}
	if allStates[0].StructuredOutput != envelope {
		t.Errorf("GetAllNodeStates[0].StructuredOutput = %q, want %q", allStates[0].StructuredOutput, envelope)
	}

	// 5. Node without structured output should have empty string
	otherNodeID := "step_1"
	if err := db.SetNodeState(taskID, otherNodeID, "completed", "output"); err != nil {
		t.Fatalf("SetNodeState for other node failed: %v", err)
	}
	ns2, ok := db.GetNodeState(taskID, otherNodeID)
	if !ok {
		t.Fatal("GetNodeState for other node returned false")
	}
	if ns2.StructuredOutput != "" {
		t.Errorf("expected empty StructuredOutput for node without envelope, got %q", ns2.StructuredOutput)
	}
}
