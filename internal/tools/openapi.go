package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"tzro/internal/memory"
)

// OpenAPITool represents an individual endpoint operation within an OpenAPI integration,
// implementing the Tools interface natively within tzro.
type OpenAPITool struct {
	IntegrationID string                 `json:"integrationId"`
	OperationID   string                 `json:"operationId"`
	Path          string                 `json:"path"`
	Method        string                 `json:"method"`
	Description   string                 `json:"description"`
	BaseURL       string                 `json:"baseUrl"`
	AuthType      string                 `json:"authType"`
	AuthKey       string                 `json:"authKey,omitempty"`
	AuthValue     string                 `json:"authValue,omitempty"`
	Properties    map[string]interface{} `json:"properties"`
	Required      []string               `json:"required"`
	ParamSources  map[string]string      `json:"paramSources"` // mapping of property key -> "path" | "query" | "body"
}

func (t *OpenAPITool) Name() string {
	return t.OperationID
}

// GetSchema compiles properties and required elements into a valid, GBNF-wrapped JSON Schema
func (t *OpenAPITool) GetSchema() (string, error) {
	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_arguments": map[string]interface{}{
				"type":       "object",
				"properties": t.Properties,
				"required":   t.Required,
			},
		},
		"required": []string{"tool_arguments"},
	}

	bytes, err := json.Marshal(schemaMap)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// Call executes the endpoint operation natively by interpolating parameters, appending queries, and injecting authentications
func (t *OpenAPITool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	pathParams := make(map[string]string)
	queryParams := make(map[string]string)
	bodyParams := make(map[string]interface{})

	// Segment arguments according to parameter sources extracted during load
	for k, v := range args {
		source, found := t.ParamSources[k]
		if !found {
			if t.Method == "GET" {
				source = "query"
			} else {
				source = "body"
			}
		}

		switch source {
		case "path":
			pathParams[k] = fmt.Sprintf("%v", v)
		case "query":
			queryParams[k] = fmt.Sprintf("%v", v)
		case "body":
			bodyParams[k] = v
		}
	}

	// 1. Interpolate path parameters
	actualPath := t.Path
	for pk, pv := range pathParams {
		actualPath = strings.ReplaceAll(actualPath, "{"+pk+"}", pv)
	}

	// 2. Build full URL
	fullURL := strings.TrimSuffix(t.BaseURL, "/") + "/" + strings.TrimPrefix(actualPath, "/")

	// 3. Append query parameters
	if len(queryParams) > 0 {
		var qParts []string
		for qk, qv := range queryParams {
			qParts = append(qParts, fmt.Sprintf("%s=%s", qk, qv))
		}
		if strings.Contains(fullURL, "?") {
			fullURL += "&" + strings.Join(qParts, "&")
		} else {
			fullURL += "?" + strings.Join(qParts, "&")
		}
	}

	// 4. Construct request body
	var bodyReader io.Reader
	if len(bodyParams) > 0 && t.Method != "GET" {
		bodyBytes, err := json.Marshal(bodyParams)
		if err != nil {
			return "", fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// 5. Create HTTP request
	req, err := http.NewRequestWithContext(ctx, t.Method, fullURL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("failed to build http request: %w", err)
	}

	// 6. Set Content-Type and custom auth headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tzro-openapi-mcp/1.0")

	switch strings.ToLower(t.AuthType) {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+t.AuthValue)
	case "apikey":
		headerName := t.AuthKey
		if headerName == "" {
			headerName = "Authorization"
		}
		req.Header.Set(headerName, t.AuthValue)
	}

	// 7. Execute request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request dispatch failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response stream: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("server responded with status %d: %s", resp.StatusCode, string(respBytes))
	}

	return string(respBytes), nil
}

// LoadOpenAPITools queries SQLite openapi_integrations table, parses specs, and registers dynamic tools
func LoadOpenAPITools() error {
	integrations, err := memory.DB.GetOpenAPIIntegrations()
	if err != nil {
		return err
	}

	for _, oi := range integrations {
		err := RegisterOpenAPISpec(oi)
		if err != nil {
			fmt.Printf("[OpenAPI Loader Warning] Skipping integration '%s': %v\n", oi.ID, err)
		}
	}
	return nil
}

// RegisterOpenAPISpec parses a spec string, parses its operations, and registers them in the global tool registry
func RegisterOpenAPISpec(oi memory.OpenAPIIntegration) error {
	var spec map[string]interface{}
	if err := json.Unmarshal([]byte(oi.OpenAPISpec), &spec); err != nil {
		return fmt.Errorf("failed to parse raw OpenAPI spec JSON: %w", err)
	}

	// 1. Resolve Base URL
	baseURL := ""
	if servers, ok := spec["servers"].([]interface{}); ok && len(servers) > 0 {
		if firstServer, ok := servers[0].(map[string]interface{}); ok {
			if urlStr, ok := firstServer["url"].(string); ok {
				baseURL = urlStr
			}
		}
	}
	if baseURL == "" {
		baseURL = "https://api.hubapi.com" // default fallback
	}

	// 2. Discover paths
	pathsVal, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing paths object in spec definitions")
	}

	for pathStr, pathVal := range pathsVal {
		methodsVal, ok := pathVal.(map[string]interface{})
		if !ok {
			continue
		}

		for methodStr, methodValObj := range methodsVal {
			methodClean := strings.ToUpper(methodStr)
			if methodClean != "GET" && methodClean != "POST" && methodClean != "PUT" && methodClean != "DELETE" && methodClean != "PATCH" {
				continue
			}

			methodVal, ok := methodValObj.(map[string]interface{})
			if !ok {
				continue
			}

			// Resolve unique tool Name
			operationID, _ := methodVal["operationId"].(string)
			if operationID == "" {
				operationID = generateOperationID(oi.ID, methodClean, pathStr)
			} else {
				operationID = oi.ID + "_" + cleanToolName(operationID)
			}

			description, _ := methodVal["description"].(string)
			if description == "" {
				description, _ = methodVal["summary"].(string)
			}

			properties := make(map[string]interface{})
			var required []string
			paramSources := make(map[string]string)

			// Parse parameters (path / query)
			if paramsList, ok := methodVal["parameters"].([]interface{}); ok {
				for _, pItem := range paramsList {
					pMap, ok := pItem.(map[string]interface{})
					if !ok {
						continue
					}

					name, _ := pMap["name"].(string)
					in, _ := pMap["in"].(string)
					reqBool, _ := pMap["required"].(bool)

					if name == "" || in == "" {
						continue
					}

					paramSources[name] = in

					var pSchema interface{}
					if schemaVal, ok := pMap["schema"].(map[string]interface{}); ok {
						pSchema = schemaVal
					} else {
						pSchema = map[string]interface{}{"type": "string"}
					}

					properties[name] = pSchema
					if reqBool {
						required = append(required, name)
					}
				}
			}

			// Parse JSON request body
			if requestBodyMap, ok := methodVal["requestBody"].(map[string]interface{}); ok {
				if contentMap, ok := requestBodyMap["content"].(map[string]interface{}); ok {
					if jsonContent, ok := contentMap["application/json"].(map[string]interface{}); ok {
						if bodySchema, ok := jsonContent["schema"].(map[string]interface{}); ok {
							bodyType, _ := bodySchema["type"].(string)
							if bodyType == "object" {
								if bodyProps, ok := bodySchema["properties"].(map[string]interface{}); ok {
									for bpKey, bpVal := range bodyProps {
										properties[bpKey] = bpVal
										paramSources[bpKey] = "body"
									}
								}
								if bodyReqs, ok := bodySchema["required"].([]interface{}); ok {
									for _, br := range bodyReqs {
										if brStr, ok := br.(string); ok {
											required = append(required, brStr)
										}
									}
								}
							} else {
								properties["body"] = bodySchema
								paramSources["body"] = "body"
								if reqBodyRequired, _ := requestBodyMap["required"].(bool); reqBodyRequired {
									required = append(required, "body")
								}
							}
						}
					}
				}
			}

			required = uniqueStrings(required)

			tool := &OpenAPITool{
				IntegrationID: oi.ID,
				OperationID:   operationID,
				Path:          pathStr,
				Method:        methodClean,
				Description:   description,
				BaseURL:       baseURL,
				AuthType:      oi.AuthType,
				AuthKey:       oi.AuthKey,
				AuthValue:     oi.AuthValue,
				Properties:    properties,
				Required:      required,
				ParamSources:  paramSources,
			}

			Register(tool)
		}
	}

	return nil
}

// UnregisterOpenAPITools removes all registered tools associated with a specific integration ID
func UnregisterOpenAPITools(integrationID string) {
	mutex.Lock()
	defer mutex.Unlock()
	for k, v := range registry {
		if ot, ok := v.(*OpenAPITool); ok && ot.IntegrationID == integrationID {
			delete(registry, k)
		}
	}
}

func generateOperationID(integrationID, method, path string) string {
	cleanPath := strings.ReplaceAll(path, "/", "_")
	cleanPath = strings.ReplaceAll(cleanPath, "{", "")
	cleanPath = strings.ReplaceAll(cleanPath, "}", "")
	cleanPath = strings.ReplaceAll(cleanPath, "-", "_")
	cleanPath = cleanToolName(cleanPath)
	return fmt.Sprintf("%s_%s%s", integrationID, strings.ToLower(method), cleanPath)
}

func cleanToolName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func uniqueStrings(slice []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

