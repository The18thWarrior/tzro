package kvlock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// OpenAIMessage represents a standard chat completion message.
type OpenAIMessage struct {
	Role       string `json:"role"`
	Content    any                `json:"content"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage    `json:"tool_calls,omitempty"`
}

// OpenAIPayload represents an incoming OpenAI request body.
type OpenAIPayload struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Tools       []any           `json:"tools,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

// AnthropicMessage represents an Anthropic message.
type AnthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// AnthropicPayload represents an incoming Anthropic request body.
type AnthropicPayload struct {
	Model     string             `json:"model"`
	System    any                `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
	Tools     []any              `json:"tools,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
	MaxTokens int                `json:"max_tokens,omitempty"`
}

// LockGuard normalizes payloads across turns to lock the KV-cache prefix.
type LockGuard struct{}

// NewLockGuard initializes a new KV-Cache Lock Guard.
func NewLockGuard() *LockGuard {
	return &LockGuard{}
}

// SortToolsDeterministically ensures tool array is ordered deterministically by name.
func SortToolsDeterministically(tools []any) {
	if len(tools) <= 1 {
		return
	}

	getToolName := func(t any) string {
		m, ok := t.(map[string]any)
		if !ok {
			return ""
		}
		if name, ok := m["name"].(string); ok {
			return name
		}
		if function, ok := m["function"].(map[string]any); ok {
			if name, ok := function["name"].(string); ok {
				return name
			}
		}
		return ""
	}

	sort.SliceStable(tools, func(i, j int) bool {
		return getToolName(tools[i]) < getToolName(tools[j])
	})
}

// NormalizeOpenAI locks message order and tool definitions for OpenAI payloads.
func (g *LockGuard) NormalizeOpenAI(raw []byte) ([]byte, string, error) {
	var payload OpenAIPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, "", err
	}

	if len(payload.Tools) > 0 {
		SortToolsDeterministically(payload.Tools)
	}

	// Calculate Prefix Hash on the first message (system prompt) and tools
	prefixData := fmt.Sprintf("%v:%v", payload.Tools, len(payload.Messages))
	if len(payload.Messages) > 0 {
		prefixData = fmt.Sprintf("%v:%v", payload.Messages[0], payload.Tools)
	}
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}

// NormalizeAnthropic locks system prompts and tool schemas for Anthropic payloads.
func (g *LockGuard) NormalizeAnthropic(raw []byte) ([]byte, string, error) {
	var payload AnthropicPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, "", err
	}

	if len(payload.Tools) > 0 {
		SortToolsDeterministically(payload.Tools)
	}

	// Format static system prompt deterministically
	systemStr := ""
	if payload.System != nil {
		if s, ok := payload.System.(string); ok {
			systemStr = strings.TrimSpace(s)
			payload.System = systemStr
		}
	}

	prefixData := fmt.Sprintf("%s:%v", systemStr, payload.Tools)
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}
