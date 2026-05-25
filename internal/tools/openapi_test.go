package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"tzro/internal/memory"
)

func TestOpenAPIIntegrationLifecycle(t *testing.T) {
	// 1. Setup isolated test SQLite database
	oldPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_openapi_test.db")
	defer func() {
		memory.DB.Close()
		os.Remove("tzro_openapi_test.db")
		memory.DB.SetDBPathForTesting(oldPath)
	}()

	if err := memory.DB.Init(); err != nil {
		t.Fatalf("failed to init database: %v", err)
	}

	// 2. Test DB Saving and Retrieval
	spec := `{
		"openapi": "3.0.0",
		"servers": [
			{"url": "https://api.hubapi.com"}
		],
		"paths": {
			"/contacts/{contactId}": {
				"get": {
					"operationId": "getContact",
					"summary": "Retrieve contact by ID",
					"parameters": [
						{
							"name": "contactId",
							"in": "path",
							"required": true,
							"schema": { "type": "string" }
						},
						{
							"name": "properties",
							"in": "query",
							"required": false,
							"schema": { "type": "string" }
						}
					]
				},
				"post": {
					"operationId": "updateContact",
					"summary": "Update contact body",
					"parameters": [
						{
							"name": "contactId",
							"in": "path",
							"required": true,
							"schema": { "type": "string" }
						}
					],
					"requestBody": {
						"required": true,
						"content": {
							"application/json": {
								"schema": {
									"type": "object",
									"properties": {
										"email": { "type": "string" },
										"firstName": { "type": "string" }
									},
									"required": ["email"]
								}
							}
						}
					}
				}
			}
		}
	}`

	integration := memory.OpenAPIIntegration{
		ID:          "hubspot",
		Name:        "HubSpot Sales CRM",
		OpenAPISpec: spec,
		AuthType:    "bearer",
		AuthValue:   "pat-token-123",
	}

	if err := memory.DB.SaveOpenAPIIntegration(integration); err != nil {
		t.Fatalf("SaveOpenAPIIntegration failed: %v", err)
	}

	list, err := memory.DB.GetOpenAPIIntegrations()
	if err != nil {
		t.Fatalf("GetOpenAPIIntegrations failed: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("expected 1 integration, got %d", len(list))
	}
	if list[0].ID != "hubspot" || list[0].AuthValue != "pat-token-123" {
		t.Errorf("unexpected integration values: %+v", list[0])
	}

	// 3. Test OpenAPI Tool Registration and Parsing
	if err := Init(""); err != nil {
		t.Fatalf("failed to init tools: %v", err)
	}

	// Dynamic registration
	if err := RegisterOpenAPISpec(list[0]); err != nil {
		t.Fatalf("RegisterOpenAPISpec failed: %v", err)
	}

	// Verify that tools are registered dynamically under registry
	schemaStr, err := GetSchema("hubspot_getContact")
	if err != nil {
		t.Fatalf("failed to retrieve schema for hubspot_getContact: %v", err)
	}

	// Schema should be wrapped with tool_arguments and contain contactId
	if !strings.Contains(schemaStr, "tool_arguments") || !strings.Contains(schemaStr, "contactId") || !strings.Contains(schemaStr, "properties") {
		t.Errorf("unexpected schema structure: %s", schemaStr)
	}

	schemaPost, err := GetSchema("hubspot_updateContact")
	if err != nil {
		t.Fatalf("failed to retrieve schema for hubspot_updateContact: %v", err)
	}
	if !strings.Contains(schemaPost, "email") || !strings.Contains(schemaPost, "firstName") || !strings.Contains(schemaPost, "contactId") {
		t.Errorf("unexpected updateContact schema structure: %s", schemaPost)
	}

	// 4. Test tool call construction and parameter mapping (no real HTTP call, verify interpolation)
	mutex.RLock()
	tTool, ok := registry["hubspot_getContact"].(*OpenAPITool)
	mutex.RUnlock()
	if !ok {
		t.Fatalf("failed to fetch OpenAPITool reference from registry")
	}

	if tTool.BaseURL != "https://api.hubapi.com" {
		t.Errorf("expected BaseURL 'https://api.hubapi.com', got '%s'", tTool.BaseURL)
	}
	if tTool.Path != "/contacts/{contactId}" {
		t.Errorf("expected Path '/contacts/{contactId}', got '%s'", tTool.Path)
	}
	if tTool.ParamSources["contactId"] != "path" || tTool.ParamSources["properties"] != "query" {
		t.Errorf("unexpected param sources: %v", tTool.ParamSources)
	}

	// Verify update tool properties and requestBody merging
	mutex.RLock()
	uTool, _ := registry["hubspot_updateContact"].(*OpenAPITool)
	mutex.RUnlock()
	if uTool.ParamSources["email"] != "body" || uTool.ParamSources["contactId"] != "path" {
		t.Errorf("unexpected update param sources: %v", uTool.ParamSources)
	}

	// Verify required field list
	hasContactId := false
	hasEmail := false
	for _, req := range uTool.Required {
		if req == "contactId" {
			hasContactId = true
		}
		if req == "email" {
			hasEmail = true
		}
	}
	if !hasContactId || !hasEmail {
		t.Errorf("expected contactId and email to be in required fields, got %v", uTool.Required)
	}

	// 5. Test Unregistering
	UnregisterOpenAPITools("hubspot")
	_, err = Call(context.Background(), "hubspot_getContact", map[string]interface{}{"contactId": "123"})
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Errorf("expected tool to be unregistered and throw error, got %v", err)
	}

	// 6. Test Delete
	if err := memory.DB.DeleteOpenAPIIntegration("hubspot"); err != nil {
		t.Fatalf("DeleteOpenAPIIntegration failed: %v", err)
	}
	listAfterDelete, err := memory.DB.GetOpenAPIIntegrations()
	if err != nil || len(listAfterDelete) != 0 {
		t.Errorf("expected 0 integrations after delete, got %v (err: %v)", listAfterDelete, err)
	}
}
