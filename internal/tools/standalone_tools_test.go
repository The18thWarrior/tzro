package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"tzro/internal/memory"
)

func TestListTools_RED(t *testing.T) {
	// Initialize registry with blank so we can register mock tools
	mutex.Lock()
	registry = make(map[string]Tool)
	mutex.Unlock()

	// Register a mock tool to test discovery
	mockTool := &BaseAgentTool{
		name:        "test_namespace_dummy_tool",
		description: "A dummy tool for namespace testing",
		schema: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"param": { "type": "string" }
					},
					"required": ["param"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			return ToolSuccess("dummy_success"), nil
		},
	}
	Register(mockTool)

	// Prepare list_tools (we will implement it next)
	listTool := &ListToolsTool{}
	Register(listTool)

	ctx := context.Background()

	// Test listing tools
	resultStr, err := Call(ctx, "list_tools", map[string]interface{}{})
	if err != nil {
		t.Fatalf("failed to call list_tools: %v", err)
	}

	var result ToolResult
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v (raw: %s)", err, resultStr)
	}

	if !result.Success {
		t.Fatalf("list_tools was unsuccessful: %s", result.Error)
	}

	dataMap, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result data to be a map, got: %T", result.Data)
	}

	toolsSlice, ok := dataMap["tools"].([]interface{})
	if !ok {
		t.Fatalf("expected tools key to be a slice, got: %T", dataMap["tools"])
	}

	// Verify we discovered the registered mock tool (excluding list_tools itself)
	foundMock := false
	for _, item := range toolsSlice {
		itemMap := item.(map[string]interface{})
		name := itemMap["name"].(string)
		if name == "test_namespace_dummy_tool" {
			foundMock = true
			if itemMap["description"].(string) != "A dummy tool for namespace testing" {
				t.Errorf("incorrect description: %s", itemMap["description"])
			}
		}
		if name == "list_tools" {
			t.Errorf("list_tools must exclude itself from output list")
		}
	}

	if !foundMock {
		t.Errorf("failed to discover registered mock tool 'test_namespace_dummy_tool'")
	}
}

func TestStandaloneToolsSuite(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_standalone_suite_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_standalone_suite_test.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	// Register all real implementations
	Register(NewWebSearchTool())
	Register(NewSearchKBTool())
	Register(NewQueryKGTool())
	Register(NewIngestKGTool())
	Register(NewExploreEntityTool())
	Register(NewSaveMemoryTool())
	Register(NewRecallMemoryTool())
	Register(NewForgetMemoryTool())
	Register(NewCreateTaskTool())

	ctx := context.Background()

	// 1. Test web_search
	resStr, err := Call(ctx, "web_search", map[string]interface{}{"query": "Go TDD", "maxResults": 1})
	if err != nil {
		t.Fatalf("web_search Call failed: %v", err)
	}
	var res1 ToolResult
	json.Unmarshal([]byte(resStr), &res1)
	if !res1.Success {
		t.Errorf("web_search unsuccessful: %s", res1.Error)
	}

	// 2. Test search_knowledge_base (populate virtual table first)
	db := memory.DB.RawDB()
	_, _ = db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS kb_documents USING fts5(document_name, excerpt, chunk_index UNINDEXED)")
	_, err = db.Exec("INSERT INTO kb_documents (document_name, excerpt, chunk_index) VALUES (?, ?, ?)", "SOP.md", "This is the TDD red green refactor process.", 1)
	if err != nil {
		t.Fatalf("failed to insert doc: %v", err)
	}

	resStr, err = Call(ctx, "search_knowledge_base", map[string]interface{}{"query": "red green", "limit": 1})
	if err != nil {
		t.Fatalf("search_knowledge_base Call failed: %v", err)
	}
	var res2 ToolResult
	json.Unmarshal([]byte(resStr), &res2)
	if !res2.Success {
		t.Errorf("search_knowledge_base unsuccessful: %s", res2.Error)
	}

	// 3. Test ingest_to_knowledge_graph
	resStr, err = Call(ctx, "ingest_to_knowledge_graph", map[string]interface{}{
		"source": "integration_test",
		"entities": []interface{}{
			map[string]interface{}{
				"id":   "entity_test_1",
				"type": "contact",
				"name": "Alice Bob",
			},
		},
		"relations": []interface{}{},
	})
	if err != nil {
		t.Fatalf("ingest_to_knowledge_graph Call failed: %v", err)
	}
	var res3 ToolResult
	json.Unmarshal([]byte(resStr), &res3)
	if !res3.Success {
		t.Errorf("ingest_to_knowledge_graph unsuccessful: %s", res3.Error)
	}

	// 4. Test query_knowledge_graph
	resStr, err = Call(ctx, "query_knowledge_graph", map[string]interface{}{"query": "Alice"})
	if err != nil {
		t.Fatalf("query_knowledge_graph Call failed: %v", err)
	}
	var res4 ToolResult
	json.Unmarshal([]byte(resStr), &res4)
	if !res4.Success {
		t.Errorf("query_knowledge_graph unsuccessful: %s", res4.Error)
	}

	// 5. Test explore_entity
	resStr, err = Call(ctx, "explore_entity", map[string]interface{}{"entityId": "entity_test_1", "maxHops": 1})
	if err != nil {
		t.Fatalf("explore_entity Call failed: %v", err)
	}
	var res5 ToolResult
	json.Unmarshal([]byte(resStr), &res5)
	if !res5.Success {
		t.Errorf("explore_entity unsuccessful: %s", res5.Error)
	}

	// 6. Test save_memory
	resStr, err = Call(ctx, "save_memory", map[string]interface{}{
		"type":       "preference",
		"content":    "User prefers precise, detailed answers.",
		"context":    "Initial discussion",
		"confidence": 0.9,
	})
	if err != nil {
		t.Fatalf("save_memory Call failed: %v", err)
	}
	var res6 ToolResult
	json.Unmarshal([]byte(resStr), &res6)
	if !res6.Success {
		t.Errorf("save_memory unsuccessful: %s", res6.Error)
	}

	// 7. Test recall_memory
	resStr, err = Call(ctx, "recall_memory", map[string]interface{}{"query": "prefers", "type": "preference"})
	if err != nil {
		t.Fatalf("recall_memory Call failed: %v", err)
	}
	var res7 ToolResult
	json.Unmarshal([]byte(resStr), &res7)
	if !res7.Success {
		t.Errorf("recall_memory unsuccessful: %s", res7.Error)
	}
	dataMap := res7.Data.(map[string]interface{})
	if dataMap["count"].(float64) != 1 {
		t.Errorf("expected 1 recalled memory, got: %v", dataMap["count"])
	}
	resultsSlice := dataMap["results"].([]interface{})
	firstMem := resultsSlice[0].(map[string]interface{})
	memID := firstMem["id"].(string)

	// 8. Test forget_memory
	resStr, err = Call(ctx, "forget_memory", map[string]interface{}{"memoryId": memID, "reason": "obsolete"})
	if err != nil {
		t.Fatalf("forget_memory Call failed: %v", err)
	}
	var res8 ToolResult
	json.Unmarshal([]byte(resStr), &res8)
	if !res8.Success {
		t.Errorf("forget_memory unsuccessful: %s", res8.Error)
	}

	// 9. Test create_task
	resStr, err = Call(ctx, "create_task", map[string]interface{}{
		"name": "Integration Task",
		"goal": "Verify all standalone tools are functional",
	})
	if err != nil {
		t.Fatalf("create_task Call failed: %v", err)
	}
	var res9 ToolResult
	json.Unmarshal([]byte(resStr), &res9)
	if !res9.Success {
		t.Errorf("create_task unsuccessful: %s", res9.Error)
	}
}

func TestLocalDatabaseCRUD(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_db_crud_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_db_crud_test.db")
		os.RemoveAll(".tzro/local_dbs")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	// Register CRUD implementations
	Register(NewCreateDatabaseTool())
	Register(NewCreateTableTool())
	Register(NewInsertTool())
	Register(NewUpdateTool())
	Register(NewDeleteTool())
	Register(NewQueryTool())

	ctx := context.Background()

	// 1. Create database
	resStr, err := Call(ctx, "local_db_create_database", map[string]interface{}{
		"name":        "test_sandbox",
		"description": "Tabular workspace for local CRM analysis",
	})
	if err != nil {
		t.Fatalf("local_db_create_database Call failed: %v", err)
	}
	var res1 ToolResult
	json.Unmarshal([]byte(resStr), &res1)
	if !res1.Success {
		t.Fatalf("local_db_create_database unsuccessful: %s", res1.Error)
	}
	dbData := res1.Data.(map[string]interface{})
	dbID := int(dbData["id"].(float64))

	// 2. Create table
	resStr, err = Call(ctx, "local_db_create_table", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "opportunities",
		"columns": []interface{}{
			map[string]interface{}{
				"name": "amount",
				"type": "REAL",
			},
			map[string]interface{}{
				"name": "stage",
				"type": "TEXT",
			},
		},
	})
	if err != nil {
		t.Fatalf("local_db_create_table Call failed: %v", err)
	}
	var res2 ToolResult
	json.Unmarshal([]byte(resStr), &res2)
	if !res2.Success {
		t.Fatalf("local_db_create_table unsuccessful: %s", res2.Error)
	}

	// 3. Insert row
	resStr, err = Call(ctx, "local_db_insert", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "opportunities",
		"data": map[string]interface{}{
			"amount": 25000.0,
			"stage":  "Qualification",
		},
	})
	if err != nil {
		t.Fatalf("local_db_insert Call failed: %v", err)
	}
	var res3 ToolResult
	json.Unmarshal([]byte(resStr), &res3)
	if !res3.Success {
		t.Fatalf("local_db_insert unsuccessful: %s", res3.Error)
	}

	// 4. Update row
	resStr, err = Call(ctx, "local_db_update", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "opportunities",
		"data": map[string]interface{}{
			"stage": "Proposal",
		},
		"where": map[string]interface{}{
			"amount": 25000.0,
		},
	})
	if err != nil {
		t.Fatalf("local_db_update Call failed: %v", err)
	}
	var res4 ToolResult
	json.Unmarshal([]byte(resStr), &res4)
	if !res4.Success {
		t.Fatalf("local_db_update unsuccessful: %s", res4.Error)
	}

	// 5. Query rows
	resStr, err = Call(ctx, "local_db_query", map[string]interface{}{
		"dbId": dbID,
		"sql":  "SELECT amount, stage FROM opportunities WHERE stage = 'Proposal'",
	})
	if err != nil {
		t.Fatalf("local_db_query Call failed: %v", err)
	}
	var res5 ToolResult
	json.Unmarshal([]byte(resStr), &res5)
	if !res5.Success {
		t.Fatalf("local_db_query unsuccessful: %s", res5.Error)
	}
	rowsList := res5.Data.([]interface{})
	if len(rowsList) != 1 {
		t.Fatalf("expected 1 opportunity record, got: %d", len(rowsList))
	}
	firstRow := rowsList[0].(map[string]interface{})
	if firstRow["stage"].(string) != "Proposal" || firstRow["amount"].(float64) != 25000.0 {
		t.Errorf("incorrect query row contents: %v", firstRow)
	}

	// 6. Test select-only read-only AST safety validation
	resStr, err = Call(ctx, "local_db_query", map[string]interface{}{
		"dbId": dbID,
		"sql":  "DROP TABLE opportunities",
	})
	if err != nil {
		t.Fatalf("local_db_query Call drop table failed with actual error: %v", err)
	}
	var res6 ToolResult
	json.Unmarshal([]byte(resStr), &res6)
	if res6.Success {
		t.Errorf("local_db_query should have rejectedDROP TABLE query successfully")
	}

	// 7. Test empty WHERE filter rejection validation in update
	resStr, err = Call(ctx, "local_db_update", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "opportunities",
		"data": map[string]interface{}{
			"stage": "Proposal",
		},
		"where": map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("local_db_update Call failed with actual error: %v", err)
	}
	var res7 ToolResult
	json.Unmarshal([]byte(resStr), &res7)
	if res7.Success {
		t.Errorf("local_db_update should have rejected empty WHERE filter successfully")
	}

	// 8. Delete row
	resStr, err = Call(ctx, "local_db_delete", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "opportunities",
		"where": map[string]interface{}{
			"amount": 25000.0,
		},
	})
	if err != nil {
		t.Fatalf("local_db_delete Call failed: %v", err)
	}
	var res8 ToolResult
	json.Unmarshal([]byte(resStr), &res8)
	if !res8.Success {
		t.Fatalf("local_db_delete unsuccessful: %s", res8.Error)
	}
}

func TestLocalDatabaseConcurrency(t *testing.T) {
	// Setup isolated test database
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_db_concurrency_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_db_concurrency_test.db")
		os.RemoveAll(".tzro/local_dbs")
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB: %v", err)
	}

	Register(NewCreateDatabaseTool())
	Register(NewCreateTableTool())
	Register(NewInsertTool())
	Register(NewQueryTool())

	ctx := context.Background()

	// 1. Create database
	resStr, err := Call(ctx, "local_db_create_database", map[string]interface{}{
		"name":        "concurrency_sandbox",
		"description": "Concurrency check sandbox",
	})
	if err != nil {
		t.Fatalf("create db failed: %v", err)
	}
	var res1 ToolResult
	json.Unmarshal([]byte(resStr), &res1)
	dbData := res1.Data.(map[string]interface{})
	dbID := int(dbData["id"].(float64))

	// 2. Create table
	resStr, err = Call(ctx, "local_db_create_table", map[string]interface{}{
		"dbId":      dbID,
		"tableName": "logs",
		"columns": []interface{}{
			map[string]interface{}{
				"name": "message",
				"type": "TEXT",
			},
		},
	})
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// 3. Spin up concurrent readers & writers
	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 10

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := Call(ctx, "local_db_insert", map[string]interface{}{
				"dbId":      dbID,
				"tableName": "logs",
				"data": map[string]interface{}{
					"message": fmt.Sprintf("Log message #%d", idx),
				},
			})
			if err != nil {
				t.Errorf("writer #%d failed: %v", idx, err)
			}
		}(i)
	}

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := Call(ctx, "local_db_query", map[string]interface{}{
				"dbId": dbID,
				"sql":  "SELECT COUNT(*) as count FROM logs",
			})
			if err != nil {
				t.Errorf("reader #%d failed: %v", idx, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify total count
	resStr, err = Call(ctx, "local_db_query", map[string]interface{}{
		"dbId": dbID,
		"sql":  "SELECT COUNT(*) as cnt FROM logs",
	})
	if err != nil {
		t.Fatalf("final query failed: %v", err)
	}
	var finalRes ToolResult
	json.Unmarshal([]byte(resStr), &finalRes)
	rows := finalRes.Data.([]interface{})
	firstRow := rows[0].(map[string]interface{})
	count := int(firstRow["cnt"].(float64))
	if count != numWriters {
		t.Errorf("expected %d logs inserted, got: %d", numWriters, count)
	}
}



