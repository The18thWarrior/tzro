package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"tzro/pkg/store"
)

// HandleNativeHook translates current client wire schemas. Legacy benchmark
// adapters remain available through their existing pre-tool/post-tool events.
func HandleNativeHook(client, event string, r io.Reader, w io.Writer, s *store.Store) error {
	var input map[string]any
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return json.NewEncoder(w).Encode(map[string]any{})
	}
	switch client {
	case "claude", "codex", "copilot", "hermes", "antigravity":
	default:
		return fmt.Errorf("unknown native hook client %q", client)
	}
	if event == "pre-tool" {
		return nativePre(client, input, w, s)
	}
	if event != "post-tool" {
		return fmt.Errorf("unknown native hook event %q", event)
	}
	output := map[string]any{}
	switch client {
	case "claude":
		if value, ok := input["tool_response"]; ok {
			output["hookSpecificOutput"] = map[string]any{"hookEventName": "PostToolUse", "updatedToolOutput": compactNativeOutput(value, s)}
		}
	case "copilot":
		result, ok := input["toolResult"].(map[string]any)
		if ok {
			if text, ok := result["textResultForLlm"].(string); ok {
				result["textResultForLlm"] = CompactOrIntercept(text, "", s)
				output["modifiedResult"] = result
			}
		}
		// Codex, Hermes, and Antigravity do not expose the same result-replacement contract.
		// Preserve the original output. Their skills and MCP tools perform compaction explicitly.
	}
	return json.NewEncoder(w).Encode(output)
}

func compactNativeOutput(value any, s *store.Store) any {
	switch v := value.(type) {
	case string:
		return CompactOrIntercept(v, "", s)
	case map[string]any:
		for _, key := range []string{"stdout", "stderr"} {
			if text, ok := v[key].(string); ok {
				v[key] = CompactOrIntercept(text, "", s)
			}
		}
	}
	return value
}

func nativePre(client string, input map[string]any, w io.Writer, s *store.Store) error {
	if client == "antigravity" {
		raw, _ := json.Marshal(input)
		var out bytes.Buffer
		if err := HandlePreToolUse(bytes.NewReader(raw), &out, s); err != nil {
			return err
		}
		var policy PreToolUseOutput
		if err := json.Unmarshal(out.Bytes(), &policy); err != nil {
			return err
		}
		if policy.Decision != "deny" {
			// "allow" bypasses native permissions; "ask" respects existing grants.
			policy.Decision = "ask"
		}
		return json.NewEncoder(w).Encode(policy)
	}
	name, _ := input["tool_name"].(string)
	argValue := input["tool_input"]
	if client == "copilot" {
		name, _ = input["toolName"].(string)
		argValue = input["toolArgs"]
	}
	args, _ := argValue.(map[string]any)
	if encoded, ok := argValue.(string); ok {
		_ = json.Unmarshal([]byte(encoded), &args)
	}
	if args == nil {
		args = map[string]any{}
	}
	// Share the existing workspace-privacy check without granting tool permissions.
	normalized := map[string]any{}
	for k, v := range args {
		normalized[k] = v
	}
	if command, ok := args["command"].(string); ok {
		normalized["CommandLine"] = command
	}
	if path, ok := args["file_path"].(string); ok {
		normalized["FilePath"] = path
	}
	payload := map[string]any{"toolCall": map[string]any{"name": name, "args": normalized}}
	raw, _ := json.Marshal(payload)
	var result bytes.Buffer
	if err := HandlePreToolUse(bytes.NewReader(raw), &result, s); err != nil {
		return err
	}
	var policy PreToolUseOutput
	if err := json.Unmarshal(result.Bytes(), &policy); err != nil {
		return err
	}
	response := map[string]any{}
	if policy.Decision == "deny" {
		switch client {
		case "claude", "codex":
			response["hookSpecificOutput"] = map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": policy.Reason}
		case "copilot":
			response["permissionDecision"] = "deny"
			response["permissionDecisionReason"] = policy.Reason
		case "hermes":
			response["action"] = "block"
			response["message"] = policy.Reason
		}
	}
	return json.NewEncoder(w).Encode(response)
}
