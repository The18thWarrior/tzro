package tools

import (
	"context"
	"encoding/json"
	"time"
)

// ToolResultMeta holds execution metadata appended to every tool result.
type ToolResultMeta struct {
	Tool        string `json:"tool"`                  // tool name (e.g. "web_search")
	DurationMs  int64  `json:"durationMs"`            // execution time in milliseconds
	Timestamp   string `json:"timestamp"`             // ISO 8601 timestamp
	RecordCount *int   `json:"recordCount,omitempty"` // heuristic count of returned records
}

// ToolResult is the standardised response envelope for all tools.
type ToolResult struct {
	Success      bool            `json:"success"`
	Data         interface{}     `json:"data,omitempty"`
	Error        string          `json:"error,omitempty"`
	Hint         string          `json:"hint,omitempty"`         // what the agent should try next
	RelatedTools []string        `json:"relatedTools,omitempty"` // alternative tools to try
	Meta         *ToolResultMeta `json:"_meta,omitempty"`
}

type ExecuteFn func(ctx context.Context, input json.RawMessage) (*ToolResult, error)

type ErrorOption func(*ToolResult)

func WithHint(hint string) ErrorOption {
	return func(r *ToolResult) {
		r.Hint = hint
	}
}

func WithRelatedTools(tools ...string) ErrorOption {
	return func(r *ToolResult) {
		r.RelatedTools = tools
	}
}

// ToolError creates a standardised error result with navigational hints.
func ToolError(msg string, opts ...ErrorOption) *ToolResult {
	r := &ToolResult{Success: false, Error: msg}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ToolSuccess creates a standardised success result.
func ToolSuccess(data interface{}) *ToolResult {
	return &ToolResult{Success: true, Data: data}
}

// WithToolMeta wraps an Execute function to inject timing metadata.
func WithToolMeta(toolName string, fn ExecuteFn) ExecuteFn {
	return func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
		start := time.Now()
		result, err := fn(ctx, input)
		if err != nil {
			return nil, err
		}
		if result.Meta == nil {
			result.Meta = &ToolResultMeta{}
		}
		result.Meta.Tool = toolName
		result.Meta.DurationMs = time.Since(start).Milliseconds()
		result.Meta.Timestamp = time.Now().UTC().Format(time.RFC3339)

		// Heuristic record count from Data
		if result.Data != nil {
			if slice, ok := result.Data.([]interface{}); ok {
				n := len(slice)
				result.Meta.RecordCount = &n
			}
		}
		return result, nil
	}
}
