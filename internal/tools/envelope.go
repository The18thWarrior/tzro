package tools

import (
	"context"
	"encoding/json"
	"time"

	"tzro/internal/content"
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

	// Extracted carries rich content (text + images) from format-specific extractors.
	// Side-channel for the probe loop — never serialized to JSON. When set, the probe
	// uses this instead of the raw Data field to construct step messages.
	Extracted *content.ExtractedContent `json:"-"`
}

type ExecuteFn func(ctx context.Context, input json.RawMessage) (*ToolResult, error)

type ResultOption func(*ToolResult)

func WithHint(hint string) ResultOption {
	return func(r *ToolResult) {
		r.Hint = hint
	}
}

func WithRelatedTools(tools ...string) ResultOption {
	return func(r *ToolResult) {
		r.RelatedTools = tools
	}
}

// ToolError creates a standardised error result with navigational hints.
func ToolError(msg string, opts ...ResultOption) *ToolResult {
	r := &ToolResult{Success: false, Error: msg}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ToolSuccess creates a standardised success result with optional hints.
func ToolSuccess(data interface{}, opts ...ResultOption) *ToolResult {
	r := &ToolResult{Success: true, Data: data}
	for _, opt := range opts {
		opt(r)
	}
	return r
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

// --- Extracted Content Side-Channel ---
//
// The Extracted field on ToolResult is json:"-", so it gets dropped during
// serialization in BaseAgentTool.Call(). This context-based side-channel
// lets the probe loop retrieve the Extracted content after Call returns.

type extractedCtxKey struct{}

// ExtractedHolder is a mutable container placed on the context.
// BaseAgentTool.Call sets it; the probe loop reads it.
type ExtractedHolder struct {
	Content *content.ExtractedContent
}

// NewExtractedCtx returns a context with an empty ExtractedHolder.
// Call this before tools.Call() to enable the side-channel.
func NewExtractedCtx(ctx context.Context) (context.Context, *ExtractedHolder) {
	holder := &ExtractedHolder{}
	return context.WithValue(ctx, extractedCtxKey{}, holder), holder
}

// ExtractedFromCtx retrieves the ExtractedHolder from a context, or nil.
func ExtractedFromCtx(ctx context.Context) *ExtractedHolder {
	return extractedHolderFromCtx(ctx)
}

// extractedHolderFromCtx is the internal accessor used by BaseAgentTool.Call.
func extractedHolderFromCtx(ctx context.Context) *ExtractedHolder {
	if holder, ok := ctx.Value(extractedCtxKey{}).(*ExtractedHolder); ok {
		return holder
	}
	return nil
}

