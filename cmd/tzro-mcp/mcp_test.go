package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/memory"
)

func TestMCPServer_HandshakeAndTools(t *testing.T) {
	// 1. Build the binary first (to make sure it's up to date)
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	// Divert stderr so it doesn't pollute the test output, but capture it in case of errors
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	// Read stderr in a goroutine to prevent blocking
	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	// Helper to send a request and read a response line
	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		// Read line from stdout
		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 3. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	initRespStr := sendAndReceive(initReq)

	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(initRespStr), &initResp); err != nil {
		t.Fatalf("failed to unmarshal init response: %v. Raw: %s", err, initRespStr)
	}
	if initResp["error"] != nil {
		t.Fatalf("init response returned error: %v", initResp["error"])
	}

	// 4. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 5. Send tools/list request
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	listRespStr := sendAndReceive(listReq)

	var listResp map[string]interface{}
	if err := json.Unmarshal([]byte(listRespStr), &listResp); err != nil {
		t.Fatalf("failed to unmarshal list response: %v. Raw: %s", err, listRespStr)
	}

	result, ok := listResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result object in list response: %s", listRespStr)
	}

	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("missing tools array in result: %s", listRespStr)
	}

	expectedTools := map[string]bool{
		"tzro_run":                   false,
		"tzro_status":                false,
		"tzro_list_tasks":            false,
		"tzro_configure_tools":       false,
		"tzro_web_search":            false,
		"tzro_memory_query":          false,
		"tzro_memory_ingest":         false,
		"tzro_kg_neighborhood":       false,
		"tzro_kg_add_entity":         false,
		"tzro_rag_context":           false,
		"tzro_skills_list":           false,
		"tzro_skills_get":            false,
		"tzro_skills_relevant":       false,
		"tzro_skills_add":            false,
		"tzro_hook_list":             false,
		"tzro_hook_approve":          false,
		"tzro_resume":                false,
		"tzro_observer_events":       false,
		"tzro_observer_memories":     false,
		"tzro_register_client_tools": false,
		"tzro_client_tool_list":      false,
		"tzro_client_tool_submit":    false,
		"tzro_model_list":            false,
		"tzro_model_set":             false,
		"tzro_completion":            false,
		"tzro_classification":        false,
	}

	for _, toolItem := range tools {
		toolMap, ok := toolItem.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := toolMap["name"].(string)
		if _, exists := expectedTools[name]; exists {
			expectedTools[name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("Expected tool %s not found in list response", name)
		}
	}

	// 6. Send tools/call for tzro_list_tasks
	callReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tzro_list_tasks","arguments":{"limit":5}}}`
	callRespStr := sendAndReceive(callReq)

	var callResp map[string]interface{}
	if err := json.Unmarshal([]byte(callRespStr), &callResp); err != nil {
		t.Fatalf("failed to unmarshal call response: %v. Raw: %s", err, callRespStr)
	}

	callResult, ok := callResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result object in call response: %s", callRespStr)
	}

	content, ok := callResult["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("missing or empty content in call result: %s", callRespStr)
	}

	textMap, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected content format: %v", content[0])
	}

	text, _ := textMap["text"].(string)
	if !strings.Contains(text, "[]") {
		t.Errorf("Expected empty JSON array '[]' in tasks list, got: %s", text)
	}

	// 7. Test tzro_memory_ingest
	ingestReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tzro_memory_ingest","arguments":{"type":"preference","content":"User prefers dark mode preference","confidence":0.9}}}`
	ingestRespStr := sendAndReceive(ingestReq)
	if !strings.Contains(ingestRespStr, "success") {
		t.Errorf("Expected successful memory ingest, got: %s", ingestRespStr)
	}

	// 8. Test tzro_memory_query
	queryReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tzro_memory_query","arguments":{"query":"dark mode preference","limit":5}}}`
	queryRespStr := sendAndReceive(queryReq)
	if !strings.Contains(queryRespStr, "prefers dark mode") {
		t.Errorf("Expected queried memory to contain 'prefers dark mode', got: %s", queryRespStr)
	}

	// 9. Test tzro_kg_add_entity
	addReq := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"tzro_kg_add_entity","arguments":{"node":{"id":"node_test_1","nodeType":"account","name":"Test Acme Corp","weight":1.0}}}}`
	addRespStr := sendAndReceive(addReq)
	if !strings.Contains(addRespStr, "success") {
		t.Errorf("Expected successful node addition, got: %s", addRespStr)
	}

	// 10. Test tzro_kg_neighborhood
	nbReq := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"tzro_kg_neighborhood","arguments":{"entityId":"node_test_1","maxHops":1}}}`
	nbRespStr := sendAndReceive(nbReq)
	if !strings.Contains(nbRespStr, "node_test_1") || !strings.Contains(nbRespStr, "Test Acme Corp") {
		t.Errorf("Expected neighborhood traversal to contain node_test_1 details, got: %s", nbRespStr)
	}

	// 11. Test tzro_rag_context
	ragReq := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"tzro_rag_context","arguments":{"query":"Test Acme Corp","maxChars":2000}}}`
	ragRespStr := sendAndReceive(ragReq)
	if !strings.Contains(ragRespStr, "Test Acme Corp") {
		t.Errorf("Expected RAG context to include node Name 'Test Acme Corp', got: %s", ragRespStr)
	}

	// 12. Test tzro_skills_add
	addSkillReq := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"tzro_skills_add","arguments":{"name":"Acme Deployment SOP","triggerDescription":"trigger docker container sync on aws","sopContent":"# SOP Acme\nStep 1: Deploy"}}}`
	addSkillRespStr := sendAndReceive(addSkillReq)
	if !strings.Contains(addSkillRespStr, "success") {
		t.Errorf("Expected successful skill addition, got: %s", addSkillRespStr)
	}

	// Parse generated skill ID
	var addSkillResp map[string]interface{}
	if err := json.Unmarshal([]byte(addSkillRespStr), &addSkillResp); err != nil {
		t.Fatalf("failed to unmarshal add skill response: %v. Raw: %s", err, addSkillRespStr)
	}
	addResult, ok := addSkillResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing result in add skill response: %s", addSkillRespStr)
	}
	addContent, ok := addResult["content"].([]interface{})
	if !ok || len(addContent) == 0 {
		t.Fatalf("missing content in add skill response: %s", addSkillRespStr)
	}
	addTextMap, ok := addContent[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected content format: %v", addContent[0])
	}
	addText, _ := addTextMap["text"].(string)

	var innerSkill struct {
		Status string `json:"status"`
		Skill  struct {
			ID string `json:"id"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(addText), &innerSkill); err != nil {
		t.Fatalf("failed to unmarshal inner skill JSON: %v. Raw: %s", err, addText)
	}
	skillID := innerSkill.Skill.ID
	if skillID == "" {
		t.Fatalf("failed to extract skill ID from text: %s", addText)
	}

	// 13. Test tzro_skills_get
	getReq := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"tzro_skills_get","arguments":{"id":"` + skillID + `"}}}`
	getRespStr := sendAndReceive(getReq)
	if !strings.Contains(getRespStr, "Acme Deployment SOP") {
		t.Errorf("Expected skill details in get response, got: %s", getRespStr)
	}

	// 14. Test tzro_skills_list
	listSkillsReq := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"tzro_skills_list","arguments":{"limit":5}}}`
	listSkillsRespStr := sendAndReceive(listSkillsReq)
	if !strings.Contains(listSkillsRespStr, "Acme Deployment SOP") {
		t.Errorf("Expected skill to be in list, got: %s", listSkillsRespStr)
	}

	// 15. Test tzro_skills_relevant
	relReq := `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"tzro_skills_relevant","arguments":{"prompt":"docker container on aws","limit":5}}}`
	relRespStr := sendAndReceive(relReq)
	if !strings.Contains(relRespStr, "Acme Deployment SOP") {
		t.Errorf("Expected relevant skill 'Acme Deployment SOP' to match prompt, got: %s", relRespStr)
	}
}

func TestMCPServer_ApprovalHookAndResume(t *testing.T) {
	// 1. Build the binary first (to make sure it's up to date)
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-approval-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Set up DB path in test process and initialize the database.
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbPath := filepath.Join(tmpDir, "tzro.db")
	memory.DB.SetDBPathForTesting(dbPath)
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB in test: %v", err)
	}

	// 3. Pre-insert the execution graph into disk_cache
	graph := &compiler.ExecutionGraph{
		TaskID:    "task-test-approval",
		MaxCycles: 5,
		CreatedAt: time.Now().Unix(),
		Nodes: []compiler.GraphNode{
			{
				ID:              "node1",
				Type:            "deterministic",
				Action:          "list_tools",
				Instructions:    `{"tool_arguments": {"namespace": ""}}`,
				AllowedTools:    []string{"list_tools"},
				Status:          "pending",
				RequireApproval: true,
			},
		},
		Edges: []compiler.GraphEdge{},
	}

	graphBytes, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("failed to marshal graph: %v", err)
	}

	db := memory.DB.RawDB()
	if db == nil {
		t.Fatalf("database raw DB is nil")
	}

	_, err = db.Exec("INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
		"graph_task-test-approval", string(graphBytes), "", time.Now().Unix())
	if err != nil {
		t.Fatalf("failed to insert graph into disk_cache: %v", err)
	}

	// 4. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	// Read stderr in a goroutine to prevent blocking
	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	// Helper to send a request and read a response line
	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		// Read line from stdout
		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 5. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	initRespStr := sendAndReceive(initReq)

	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(initRespStr), &initResp); err != nil {
		t.Fatalf("failed to unmarshal init response: %v. Raw: %s", err, initRespStr)
	}
	if initResp["error"] != nil {
		t.Fatalf("init response returned error: %v", initResp["error"])
	}

	// 6. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 7. Send tzro_resume to trigger background execution of cached graph
	resumeReq := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tzro_resume","arguments":{"taskId":"task-test-approval"}}}`
	resumeRespStr := sendAndReceive(resumeReq)
	t.Logf("resumeRespStr: %s", resumeRespStr)
	if !strings.Contains(resumeRespStr, "success") {
		t.Fatalf("expected tzro_resume to succeed, got: %s", resumeRespStr)
	}

	// 8. Give it a moment to run and pause, then call tzro_hook_list
	time.Sleep(500 * time.Millisecond)
	listReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tzro_hook_list","arguments":{}}}`
	listRespStr := sendAndReceive(listReq)
	t.Logf("listRespStr: %s", listRespStr)

	if !strings.Contains(listRespStr, "task-test-approval") || !strings.Contains(listRespStr, "node1") {
		t.Fatalf("expected approval request to be listed in tzro_hook_list, got: %s", listRespStr)
	}

	// 9. Approve the node using tzro_hook_approve
	approveReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tzro_hook_approve","arguments":{"taskId":"task-test-approval","nodeId":"node1"}}}`
	approveRespStr := sendAndReceive(approveReq)
	t.Logf("approveRespStr: %s", approveRespStr)
	if !strings.Contains(approveRespStr, "success") {
		t.Fatalf("expected tzro_hook_approve to succeed, got: %s", approveRespStr)
	}

	// 10. Poll tzro_status until task transitions to completed
	statusReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tzro_status","arguments":{"taskId":"task-test-approval"}}}`
	var statusRespStr string
	completed := false
	for i := 0; i < 30; i++ {
		statusRespStr = sendAndReceive(statusReq)
		if strings.Contains(statusRespStr, `\"status\": \"completed\"`) {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !completed {
		t.Errorf("expected task to complete after approval, last status: %s", statusRespStr)
	}
}

func TestMCPServer_ClientToolDispatch(t *testing.T) {
	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-client-tool-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Set up DB path in test process and initialize the database.
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbPath := filepath.Join(tmpDir, "tzro.db")
	memory.DB.SetDBPathForTesting(dbPath)
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB in test: %v", err)
	}

	// 3. Pre-insert the execution graph into disk_cache
	graph := &compiler.ExecutionGraph{
		TaskID:    "task-test-client-tool",
		MaxCycles: 5,
		CreatedAt: time.Now().Unix(),
		Nodes: []compiler.GraphNode{
			{
				ID:           "node1",
				Type:         "deterministic",
				Action:       "send_slack",
				Instructions: `{"tool_arguments": {"channel": "#alerts", "message": "hello slack"}}`,
				AllowedTools: []string{"send_slack"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{},
	}

	graphBytes, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("failed to marshal graph: %v", err)
	}

	db := memory.DB.RawDB()
	if db == nil {
		t.Fatalf("database raw DB is nil")
	}

	_, err = db.Exec("INSERT OR REPLACE INTO disk_cache (cache_id, raw_payload, envelope_json, created_at) VALUES (?, ?, ?, ?)",
		"graph_task-test-client-tool", string(graphBytes), "", time.Now().Unix())
	if err != nil {
		t.Fatalf("failed to insert graph into disk_cache: %v", err)
	}

	// 4. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	// Read stderr in a goroutine to prevent blocking
	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	// Helper to send a request and read a response line
	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		// Read line from stdout
		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 5. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	initRespStr := sendAndReceive(initReq)

	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(initRespStr), &initResp); err != nil {
		t.Fatalf("failed to unmarshal init response: %v. Raw: %s", err, initRespStr)
	}
	if initResp["error"] != nil {
		t.Fatalf("init response returned error: %v", initResp["error"])
	}

	// 6. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 7. Register the client-side tool
	registerReq := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tzro_register_client_tools","arguments":{"tools":[{"name":"send_slack","description":"Send a message to a slack channel","inputSchema":{"type":"object","properties":{"channel":{"type":"string"},"message":{"type":"string"}},"required":["channel","message"]}}]}}}`
	registerRespStr := sendAndReceive(registerReq)
	t.Logf("registerRespStr: %s", registerRespStr)
	if !strings.Contains(registerRespStr, "send_slack") {
		t.Fatalf("expected send_slack to be registered, got: %s", registerRespStr)
	}

	// 8. Send tzro_resume to trigger background execution of cached graph
	resumeReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tzro_resume","arguments":{"taskId":"task-test-client-tool"}}}`
	resumeRespStr := sendAndReceive(resumeReq)
	t.Logf("resumeRespStr: %s", resumeRespStr)
	if !strings.Contains(resumeRespStr, "success") {
		t.Fatalf("expected tzro_resume to succeed, got: %s", resumeRespStr)
	}

	// 9. Give it a moment to run and pause, then call tzro_client_tool_list
	time.Sleep(500 * time.Millisecond)
	listReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tzro_client_tool_list","arguments":{}}}`
	listRespStr := sendAndReceive(listReq)
	t.Logf("listRespStr: %s", listRespStr)

	if !strings.Contains(listRespStr, "task-test-client-tool") || !strings.Contains(listRespStr, "node1") || !strings.Contains(listRespStr, "#alerts") {
		t.Fatalf("expected client tool request to be listed with extracted arguments, got: %s", listRespStr)
	}

	// 10. Submit client tool execution result
	submitReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tzro_client_tool_submit","arguments":{"taskId":"task-test-client-tool","nodeId":"node1","output":"slack message sent successfully"}}}`
	submitRespStr := sendAndReceive(submitReq)
	t.Logf("submitRespStr: %s", submitRespStr)
	if !strings.Contains(submitRespStr, "success") {
		t.Fatalf("expected tzro_client_tool_submit to succeed, got: %s", submitRespStr)
	}

	// 11. Poll tzro_status until task transitions to completed
	statusReq := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"tzro_status","arguments":{"taskId":"task-test-client-tool"}}}`
	var statusRespStr string
	completed := false
	for i := 0; i < 30; i++ {
		statusRespStr = sendAndReceive(statusReq)
		if strings.Contains(statusRespStr, `\"status\": \"completed\"`) {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !completed {
		t.Errorf("expected task to complete after submitting client tool output, last status: %s", statusRespStr)
	}

	if !strings.Contains(statusRespStr, "slack message sent successfully") {
		t.Errorf("expected status output to contain submitted slack output, got: %s", statusRespStr)
	}
}

func TestMCPServer_ResourceTemplatesList(t *testing.T) {
	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-templates-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 3. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	_ = sendAndReceive(initReq)

	// 4. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 5. Send resources/templates/list request
	listReq := `{"jsonrpc":"2.0","id":2,"method":"resources/templates/list","params":{}}`
	listRespStr := sendAndReceive(listReq)
	t.Logf("templates list response: %s", listRespStr)

	// Verify we got the template paths
	if !strings.Contains(listRespStr, "tzro://tasks/{taskId}/output") {
		t.Errorf("expected template 'tzro://tasks/{taskId}/output' not found in response: %s", listRespStr)
	}
	if !strings.Contains(listRespStr, "tzro://tasks/{taskId}/nodes/{nodeId}/output") {
		t.Errorf("expected template 'tzro://tasks/{taskId}/nodes/{nodeId}/output' not found in response: %s", listRespStr)
	}
}

func TestMCPServer_ResourceReadCompact(t *testing.T) {
	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-read-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Set up DB path in test process and initialize the database.
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbPath := filepath.Join(tmpDir, "tzro.db")
	memory.DB.SetDBPathForTesting(dbPath)
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB in test: %v", err)
	}

	// 3. Pre-insert task and node state into SQLite
	err = memory.DB.SetNodeState("task-test-read", "node-test-1", "completed", "[Local Tactician] compacted node output")
	if err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	err = memory.DB.SetNodeRawOutput("task-test-read", "node-test-1", "raw node output here")
	if err != nil {
		t.Fatalf("failed to set node raw output: %v", err)
	}

	// 4. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 5. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	_ = sendAndReceive(initReq)

	// 6. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 7. Read task output resource (compact by default)
	readTaskReq := `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"tzro://tasks/task-test-read/output"}}`
	readTaskResp := sendAndReceive(readTaskReq)
	t.Logf("readTaskResp: %s", readTaskResp)

	if !strings.Contains(readTaskResp, "node-test-1") || !strings.Contains(readTaskResp, "compacted node output") {
		t.Errorf("read task output response missing node-test-1 or compacted state details: %s", readTaskResp)
	}

	// 8. Read node output resource (compact by default)
	readNodeReq := `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"tzro://tasks/task-test-read/nodes/node-test-1/output"}}`
	readNodeResp := sendAndReceive(readNodeReq)
	t.Logf("readNodeResp: %s", readNodeResp)

	if !strings.Contains(readNodeResp, "compacted node output") || strings.Contains(readNodeResp, "raw node output") {
		t.Errorf("read node output response should contain compacted output and NOT contain raw output: %s", readNodeResp)
	}
}

func TestMCPServer_ResourceReadRaw(t *testing.T) {
	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-read-raw-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Set up DB path in test process and initialize the database.
	oldDBPath := memory.DB.GetDBPathForTesting()
	dbPath := filepath.Join(tmpDir, "tzro.db")
	memory.DB.SetDBPathForTesting(dbPath)
	defer func() {
		memory.DB.Close()
		memory.DB.SetDBPathForTesting(oldDBPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init DB in test: %v", err)
	}

	// 3. Pre-insert task and node state into SQLite
	err = memory.DB.SetNodeState("task-test-raw", "node-test-1", "completed", "[Local Model] compacted node output")
	if err != nil {
		t.Fatalf("failed to set node state: %v", err)
	}
	err = memory.DB.SetNodeRawOutput("task-test-raw", "node-test-1", "raw node output here")
	if err != nil {
		t.Fatalf("failed to set node raw output: %v", err)
	}

	// 4. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 5. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	_ = sendAndReceive(initReq)

	// 6. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 7. Read task output resource with format=raw
	readTaskReq := `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"tzro://tasks/task-test-raw/output?format=raw"}}`
	readTaskResp := sendAndReceive(readTaskReq)
	t.Logf("readTaskResp with raw: %s", readTaskResp)

	if !strings.Contains(readTaskResp, "raw node output here") {
		t.Errorf("read task output response with format=raw should contain raw output: %s", readTaskResp)
	}

	// 8. Read node output resource with format=raw
	readNodeReq := `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"tzro://tasks/task-test-raw/nodes/node-test-1/output?format=raw"}}`
	readNodeResp := sendAndReceive(readNodeReq)
	t.Logf("readNodeResp with raw: %s", readNodeResp)

	if !strings.Contains(readNodeResp, "raw node output here") {
		t.Errorf("read node output response with format=raw should contain raw output: %s", readNodeResp)
	}
}

func TestMCPServer_ResourceSubscribe(t *testing.T) {
	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-sub-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Start the binary
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 3. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	_ = sendAndReceive(initReq)

	// 4. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 5. Send resources/subscribe request
	subReq := `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"tzro://tasks/task-test-sub/output"}}`
	subResp := sendAndReceive(subReq)
	t.Logf("subscribe response: %s", subResp)

	if !strings.Contains(subResp, `"result":`) || strings.Contains(subResp, "error") {
		t.Errorf("expected successful empty result for subscribe, got: %s", subResp)
	}

	// 6. Send resources/unsubscribe request
	unsubReq := `{"jsonrpc":"2.0","id":3,"method":"resources/unsubscribe","params":{"uri":"tzro://tasks/task-test-sub/output"}}`
	unsubResp := sendAndReceive(unsubReq)
	t.Logf("unsubscribe response: %s", unsubResp)

	if !strings.Contains(unsubResp, `"result":`) || strings.Contains(unsubResp, "error") {
		t.Errorf("expected successful empty result for unsubscribe, got: %s", unsubResp)
	}
}

func TestMCPServer_ResourceSubscribeEventBridge(t *testing.T) {
	// Start a mock SSE server to stream daemon events
	messageChan := make(chan string, 1)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-messageChan:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			}
		}
	}))
	defer mockServer.Close()

	// 1. Build the binary first
	tmpDir, err := os.MkdirTemp("", "tzro-mcp-test-bridge-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "tzro-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build tzro-mcp binary: %v", err)
	}

	// 2. Start the binary, passing TZRO_DAEMON_URL pointing to the mock server
	cmd := exec.Command(binPath)
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "TZRO_DAEMON_URL="+mockServer.URL)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start tzro-mcp: %v", err)
	}

	go func() {
		_, _ = io.Copy(os.Stderr, stderrPipe)
	}()

	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
	}()

	reader := bufio.NewReader(stdout)

	sendAndReceive := func(reqJSON string) string {
		_, err := stdin.Write([]byte(reqJSON + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		lineChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			lineChan <- line
		}()

		select {
		case line := <-lineChan:
			return line
		case err := <-errChan:
			t.Fatalf("failed to read line from stdout: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for response to: %s", reqJSON)
		}
		return ""
	}

	// 3. Send initialize request
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`
	_ = sendAndReceive(initReq)

	// 4. Send initialized notification
	initializedNotification := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initializedNotification + "\n"))

	// 5. Send resources/subscribe request
	subReq := `{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"tzro://tasks/task-test-bridge/output"}}`
	subResp := sendAndReceive(subReq)
	t.Logf("subscribe response: %s", subResp)

	// 6. Push a mock Event to mockServer via SSE stream channel
	mockChunkJSON := `{"taskId":"task-test-bridge","source":"executor","type":"node_state","content":"mock node execution completed"}`
	messageChan <- mockChunkJSON

	// 7. Wait and read the notification from stdout (server subprocess output)
	notificationChan := make(chan string, 1)
	errChan := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errChan <- err
				return
			}
			if strings.Contains(line, "notifications/resources/updated") {
				notificationChan <- line
				return
			}
		}
	}()

	select {
	case notifLine := <-notificationChan:
		t.Logf("received notification line: %s", notifLine)
		if !strings.Contains(notifLine, "tzro://tasks/task-test-bridge/output") {
			t.Errorf("expected notification URI to match, got: %s", notifLine)
		}
	case err := <-errChan:
		t.Fatalf("failed to read notification line: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for notification from event bridge")
	}
}
