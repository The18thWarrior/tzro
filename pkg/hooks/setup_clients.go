package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func (a *agentSetup) mcpJSON(path string, extra map[string]any) {
	a.step("MCP", path, "", func() (bool, error) {
		return updateJSON(path, func(doc map[string]any) error {
			servers, err := object(doc, "mcpServers")
			if err != nil {
				return err
			}
			server := map[string]any{"command": a.opts.Binary, "args": []string{"mcp"}}
			for k, v := range extra {
				server[k] = v
			}
			return setOwned(servers, "tzro", server)
		})
	})
}
func (a *agentSetup) claude() {
	dir := configDir(a.opts, "CLAUDE_CONFIG_DIR", ".claude")
	a.skill(filepath.Join(dir, "skills"))
	a.nestedHooks(filepath.Join(dir, "settings.json"), "")
	path := filepath.Join(a.opts.Home, ".claude.json")
	if a.opts.WorkspaceOnly {
		path = filepath.Join(a.opts.Workspace, ".mcp.json")
	}
	// CLAUDE_CONFIG_DIR relocates both settings and the user MCP file.
	if !a.opts.WorkspaceOnly && dir != filepath.Join(a.opts.Home, ".claude") {
		path = filepath.Join(dir, ".claude.json")
	}
	a.mcpJSON(path, map[string]any{"type": "stdio"})
}
func (a *agentSetup) antigravity() {
	dir := filepath.Join(a.opts.Home, ".gemini", "config")
	if a.opts.WorkspaceOnly {
		dir = filepath.Join(a.opts.Workspace, ".agents")
	}
	a.skill(filepath.Join(dir, "skills"))
	a.mcpJSON(filepath.Join(dir, "mcp_config.json"), nil)
	path := filepath.Join(dir, "hooks.json")
	a.step("hooks", path, "", func() (bool, error) {
		return updateJSON(path, func(doc map[string]any) error {
			h := map[string]any{}
			for _, event := range []string{"PreToolUse", "PostToolUse"} {
				phase := "pre-tool"
				if event == "PostToolUse" {
					phase = "post-tool"
				}
				h[event] = []any{map[string]any{"matcher": ".*", "hooks": []any{map[string]any{"type": "command", "command": a.hookCommand(phase), "timeout": 10}}}}
			}
			return setOwned(doc, "tzro", h)
		})
	})
}
func (a *agentSetup) copilot() {
	dir := configDir(a.opts, "COPILOT_HOME", ".copilot")
	skillDir := filepath.Join(dir, "skills")
	hookDir := filepath.Join(dir, "hooks")
	if a.opts.WorkspaceOnly {
		skillDir = filepath.Join(a.opts.Workspace, ".github", "skills")
		hookDir = filepath.Join(a.opts.Workspace, ".github", "hooks")
	}
	a.skill(skillDir)
	// MCP registration is a Copilot CLI user setting, not a VS Code setting.
	if a.opts.WorkspaceOnly {
		a.result.Integrations = append(a.result.Integrations, IntegrationResult{Name: "MCP", Status: "unsupported", Message: "Copilot CLI MCP setup requires user scope; rerun without --workspace."})
	} else {
		a.mcpJSON(filepath.Join(dir, "mcp-config.json"), map[string]any{"type": "local", "tools": []string{"*"}})
	}
	path := filepath.Join(hookDir, "tzro.json")
	a.step("hooks", path, "", func() (bool, error) {
		h := map[string]any{}
		for _, event := range []string{"preToolUse", "postToolUse"} {
			phase := "pre-tool"
			if event == "postToolUse" {
				phase = "post-tool"
			}
			h[event] = []any{map[string]any{"type": "command", "bash": a.hookCommand(phase), "timeoutSec": 10}}
		}
		data, err := json.MarshalIndent(map[string]any{"version": 1, "hooks": h}, "", "  ")
		if err != nil {
			return false, err
		}
		return writeOwnedFile(path, append(data, '\n'), 0600)
	})
}

func yamlObject(node *yaml.Node, key string) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected YAML mapping for %s", key)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			if node.Content[i+1].Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%s must be a mapping", key)
			}
			return node.Content[i+1], nil
		}
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child, nil
}
func yamlOwned(node *yaml.Node, key string, value any) error {
	var desired yaml.Node
	if err := desired.Encode(value); err != nil {
		return err
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			var old any
			if err := node.Content[i+1].Decode(&old); err != nil {
				return err
			}
			if !jsonEqual(old, value) {
				return fmt.Errorf("existing %s differs; preserved for manual review", key)
			}
			return nil
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &desired)
	return nil
}
func (a *agentSetup) hermes() {
	dir := configDir(a.opts, "HERMES_HOME", ".hermes")
	a.skill(filepath.Join(dir, "skills"))
	path := filepath.Join(dir, "config.yaml")
	// Both integrations share one document edit, preserving YAML comments and unrelated nodes.
	changed, err := updateFile(path, 0600, func(old []byte) ([]byte, error) {
		doc := yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		if len(bytes.TrimSpace(old)) > 0 {
			decoder := yaml.NewDecoder(bytes.NewReader(old))
			if err := decoder.Decode(&doc); err != nil {
				return nil, err
			}
			var extra yaml.Node
			if err := decoder.Decode(&extra); err != io.EOF {
				return nil, fmt.Errorf("expected exactly one YAML document")
			}
		}
		if len(doc.Content) != 1 {
			return nil, fmt.Errorf("expected one YAML document")
		}
		// Decode once to reject duplicate keys, rather than silently replacing ambiguous config.
		var check map[string]any
		if err := doc.Decode(&check); err != nil {
			return nil, err
		}
		root := doc.Content[0]
		servers, err := yamlObject(root, "mcp_servers")
		if err != nil {
			return nil, err
		}
		if err := yamlOwned(servers, "tzro", map[string]any{"command": a.opts.Binary, "args": []string{"mcp"}}); err != nil {
			return nil, err
		}
		h, err := yamlObject(root, "hooks")
		if err != nil {
			return nil, err
		}
		for _, event := range []string{"pre_tool_call", "post_tool_call"} {
			phase := "pre-tool"
			if event == "post_tool_call" {
				phase = "post-tool"
			}
			desired := map[string]any{"matcher": ".*", "command": a.hookCommand(phase), "timeout": 10}
			var list *yaml.Node
			for i := 0; i < len(h.Content); i += 2 {
				if h.Content[i].Value == event {
					list = h.Content[i+1]
				}
			}
			if list == nil {
				list = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				h.Content = append(h.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: event}, list)
			}
			if list.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("%s must be a list", event)
			}
			found := false
			for _, item := range list.Content {
				var value any
				if err := item.Decode(&value); err != nil {
					return nil, err
				}
				if jsonEqual(value, desired) {
					found = true
				} else if encoded, _ := json.Marshal(value); strings.Contains(string(encoded), " hook hermes native-") {
					return nil, fmt.Errorf("modified tzro %s hook preserved; review before reinitializing", event)
				}
			}
			if !found {
				var item yaml.Node
				if err := item.Encode(desired); err != nil {
					return nil, err
				}
				list.Content = append(list.Content, &item)
			}
		}
		return yaml.Marshal(&doc)
	})
	a.step("MCP", path, "", func() (bool, error) { return changed, err })
	pending := "Restart Hermes and accept its native hook prompt; activation is not verified."
	if a.opts.WorkspaceOnly {
		pending = "Select this Hermes profile with HERMES_HOME=" + shellQuote(dir) + ". " + pending
	}
	a.step("hooks", path, pending, func() (bool, error) { return changed, err })
}
func (a *agentSetup) piCoder() {
	dir := configDir(a.opts, "PI_CODING_AGENT_DIR", ".pi/agent")
	if a.opts.WorkspaceOnly {
		dir = filepath.Join(a.opts.Workspace, ".pi")
	}
	a.skill(filepath.Join(dir, "skills"))
	path := filepath.Join(dir, "extensions", "tzro-hook.ts")
	binary, _ := json.Marshal(a.opts.Binary)
	extension := fmt.Sprintf(`// Generated by tzro init. Local output compaction; errors leave the original result intact.
import { execFileSync } from "node:child_process";
export default function (pi: any) {
  pi.on("tool_result", async (event: any) => {
    if (!Array.isArray(event.content)) return;
    try {
      const content = event.content.map((item: any) => {
        if (item.type !== "text" || typeof item.text !== "string") return item;
        const result = JSON.parse(execFileSync(%s, ["hook", "pi-coder", "post-tool"], {
          input: JSON.stringify({tool_name: event.toolName, tool_output: item.text}),
          encoding: "utf8", timeout: 10000, maxBuffer: 32 * 1024 * 1024,
        }));
        return typeof result.tool_output === "string" ? {...item, text: result.tool_output} : item;
      });
      return {content};
    } catch { return; }
  });
}
`, string(binary))
	a.step("hooks", path, "", func() (bool, error) { return writeOwnedFile(path, []byte(extension), 0644) })
	a.result.Integrations = append(a.result.Integrations, IntegrationResult{Name: "MCP", Status: "unsupported", Message: "No native MCP registration verified for this Pi version; the skill uses the tzro CLI."})
}
