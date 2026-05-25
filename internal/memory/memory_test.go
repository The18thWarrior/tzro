package memory

import (
	"context"
	"database/sql"
	"encoding/json"
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
		jsonPath:         jsonPath,
		dbPath:           dbPath,
		EmbeddingEngine:  engine,
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
		ID:        "edge_ab",
		EdgeType:  "belongs_to",
		SourceID:  "node_b",
		TargetID:  "node_a",
		Weight:    1.0,
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

