package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebBrowse_BasicFetch(t *testing.T) {
	// Mock HTTP server returning HTML
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Test Page</title>
<script>var x = 1;</script>
<style>body { color: red; }</style>
</head>
<body>
<h1>Hello World</h1>
<p>This is a test paragraph with <strong>bold</strong> text.</p>
<ul>
<li>Item one</li>
<li>Item two</li>
</ul>
</body>
</html>`))
	}))
	defer server.Close()

	tool := NewWebBrowseTool()
	ctx := context.Background()

	args := map[string]interface{}{
		"url": server.URL,
	}
	result, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}

	var resp ToolResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Expected success, got error: %s", resp.Error)
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected map data, got %T", resp.Data)
	}

	content, ok := data["content"].(string)
	if !ok {
		t.Fatalf("Expected string content, got %T", data["content"])
	}

	// Should contain the visible text
	if !strings.Contains(content, "Hello World") {
		t.Errorf("Expected content to contain 'Hello World', got: %s", content)
	}
	if !strings.Contains(content, "test paragraph") {
		t.Errorf("Expected content to contain 'test paragraph', got: %s", content)
	}
	if !strings.Contains(content, "Item one") {
		t.Errorf("Expected content to contain 'Item one', got: %s", content)
	}

	// Should NOT contain script/style content
	if strings.Contains(content, "var x = 1") {
		t.Errorf("Content should not contain script content")
	}
	if strings.Contains(content, "color: red") {
		t.Errorf("Content should not contain style content")
	}
	// Should NOT contain HTML tags
	if strings.Contains(content, "<h1>") {
		t.Errorf("Content should not contain HTML tags")
	}
}

func TestWebBrowse_Truncation(t *testing.T) {
	// Generate a page with lots of content
	longContent := strings.Repeat("This is a sentence with some words. ", 500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><p>" + longContent + "</p></body></html>"))
	}))
	defer server.Close()

	tool := NewWebBrowseTool()
	ctx := context.Background()

	maxChars := 500
	args := map[string]interface{}{
		"url":      server.URL,
		"maxChars": float64(maxChars), // JSON numbers are float64
	}
	result, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}

	var resp ToolResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Expected success, got error: %s", resp.Error)
	}

	data := resp.Data.(map[string]interface{})
	content := data["content"].(string)

	// Content should be truncated
	if !strings.Contains(content, "content truncated") {
		t.Errorf("Expected truncation marker in content")
	}
}

func TestWebBrowse_InvalidScheme(t *testing.T) {
	tool := NewWebBrowseTool()
	ctx := context.Background()

	args := map[string]interface{}{
		"url": "ftp://example.com/file.txt",
	}
	result, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}

	var resp ToolResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if resp.Success {
		t.Fatalf("Expected failure for ftp:// URL, got success")
	}
	if !strings.Contains(resp.Error, "only http:// and https://") {
		t.Errorf("Expected scheme error, got: %s", resp.Error)
	}
}

func TestWebBrowse_EmptyURL(t *testing.T) {
	tool := NewWebBrowseTool()
	ctx := context.Background()

	args := map[string]interface{}{
		"url": "",
	}
	result, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}

	var resp ToolResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if resp.Success {
		t.Fatalf("Expected failure for empty URL, got success")
	}
}

func TestWebBrowse_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := NewWebBrowseTool()
	ctx := context.Background()

	args := map[string]interface{}{
		"url": server.URL,
	}
	result, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() returned error: %v", err)
	}

	var resp ToolResult
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if resp.Success {
		t.Fatalf("Expected failure for 404 response, got success")
	}
	if !strings.Contains(resp.Error, "HTTP 404") {
		t.Errorf("Expected HTTP 404 error, got: %s", resp.Error)
	}
}

func TestHtmlToText_BasicConversion(t *testing.T) {
	input := `<html><body><h1>Title</h1><p>Hello &amp; world</p><script>alert('x')</script></body></html>`
	result := htmlToText(input)

	if !strings.Contains(result, "Title") {
		t.Errorf("Expected 'Title' in result: %s", result)
	}
	if !strings.Contains(result, "Hello & world") {
		t.Errorf("Expected 'Hello & world' in result: %s", result)
	}
	if strings.Contains(result, "alert") {
		t.Errorf("Script content should be removed: %s", result)
	}
	if strings.Contains(result, "<") {
		t.Errorf("HTML tags should be removed: %s", result)
	}
}
