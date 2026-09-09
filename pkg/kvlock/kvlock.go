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
	Role       string          `json:"role"`
	Content    any             `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
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

// NormalizeOpenAI locks message order and tool definitions for OpenAI payloads while preserving all arbitrary and unknown fields.
func (g *LockGuard) NormalizeOpenAI(raw []byte) ([]byte, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Non-fatal normalization error fails open
		return raw, "", err
	}

	// Sort tools if present
	if toolsRaw, ok := payload["tools"]; ok {
		if toolsList, ok := toolsRaw.([]any); ok && len(toolsList) > 0 {
			SortToolsDeterministically(toolsList)
			payload["tools"] = toolsList
		}
	}

	// Calculate Prefix Hash on the first message (system prompt / first turn) and tools
	messages, _ := payload["messages"].([]any)
	tools, _ := payload["tools"].([]any)

	prefixData := fmt.Sprintf("%v:%v", tools, len(messages))
	if len(messages) > 0 {
		prefixData = fmt.Sprintf("%v:%v", messages[0], tools)
	}
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		// Fail open on marshaling failure
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}

// NormalizeAnthropic locks system prompts and tool schemas for Anthropic payloads while preserving all arbitrary and unknown fields.
func (g *LockGuard) NormalizeAnthropic(raw []byte) ([]byte, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		// Non-fatal normalization error fails open
		return raw, "", err
	}

	// Sort tools if present
	if toolsRaw, ok := payload["tools"]; ok {
		if toolsList, ok := toolsRaw.([]any); ok && len(toolsList) > 0 {
			SortToolsDeterministically(toolsList)
			payload["tools"] = toolsList
		}
	}

	// Format static system prompt deterministically
	systemStr := ""
	if sys, ok := payload["system"]; ok && sys != nil {
		if s, ok := sys.(string); ok {
			systemStr = strings.TrimSpace(s)
			payload["system"] = systemStr
		}
	}

	tools, _ := payload["tools"].([]any)
	prefixData := fmt.Sprintf("%s:%v", systemStr, tools)
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		// Fail open on marshaling failure
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}

// NormalizeResponses locks input instructions and tools for OpenAI Responses API payloads (/v1/responses).
func (g *LockGuard) NormalizeResponses(raw []byte) ([]byte, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, "", err
	}

	// Sort tools if present
	if toolsRaw, ok := payload["tools"]; ok {
		if toolsList, ok := toolsRaw.([]any); ok && len(toolsList) > 0 {
			SortToolsDeterministically(toolsList)
			payload["tools"] = toolsList
		}
	}

	instructions, _ := payload["instructions"].(string)
	tools, _ := payload["tools"].([]any)
	prefixData := fmt.Sprintf("%s:%v", strings.TrimSpace(instructions), tools)
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}

// NormalizeGemini locks system instructions and tools for Gemini-native payloads (/v1beta/models/...).
func (g *LockGuard) NormalizeGemini(raw []byte) ([]byte, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, "", err
	}

	// Sort function declarations inside tools if present
	if toolsRaw, ok := payload["tools"]; ok {
		if toolsList, ok := toolsRaw.([]any); ok && len(toolsList) > 0 {
			for _, t := range toolsList {
				if toolMap, ok := t.(map[string]any); ok {
					if fnDecls, ok := toolMap["function_declarations"].([]any); ok && len(fnDecls) > 1 {
						sort.SliceStable(fnDecls, func(i, j int) bool {
							m1, _ := fnDecls[i].(map[string]any)
							m2, _ := fnDecls[j].(map[string]any)
							n1, _ := m1["name"].(string)
							n2, _ := m2["name"].(string)
							return n1 < n2
						})
					}
				}
			}
		}
	}

	systemInstruction, _ := payload["system_instruction"]
	tools, _ := payload["tools"]
	prefixData := fmt.Sprintf("%v:%v", systemInstruction, tools)
	hash := sha256.Sum256([]byte(prefixData))
	prefixHash := hex.EncodeToString(hash[:])[:12]

	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw, prefixHash, err
	}

	return normalized, prefixHash, nil
}

