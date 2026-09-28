package hooks

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// updateFile validates before writing and replaces one file atomically.
func updateFile(path string, mode os.FileMode, edit func([]byte) ([]byte, error)) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	data, err := edit(old)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(old, data) {
		return false, nil
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("refusing to replace non-regular configuration: %s", path)
		}
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tzro-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	closeErr := tmp.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}

func writeOwnedFile(path string, data []byte, mode os.FileMode) (bool, error) {
	changed, err := updateFile(path, mode, func(old []byte) ([]byte, error) {
		if len(old) > 0 && !bytes.Equal(old, data) {
			previous, err := os.ReadFile(path + ".tzro.sha256")
			if err != nil || strings.TrimSpace(string(previous)) != fmt.Sprintf("%x", sha256.Sum256(old)) {
				return nil, fmt.Errorf("modified file preserved; move it aside before reinitializing")
			}
		}
		return data, nil
	})
	if err != nil {
		return false, err
	}
	_, err = updateFile(path+".tzro.sha256", 0600, func([]byte) ([]byte, error) { return []byte(fmt.Sprintf("%x\n", sha256.Sum256(data))), nil })
	return changed, err
}

func updateJSON(path string, edit func(map[string]any) error) (bool, error) {
	return updateFile(path, 0600, func(old []byte) ([]byte, error) {
		doc := map[string]any{}
		if len(bytes.TrimSpace(old)) > 0 {
			if err := json.Unmarshal(old, &doc); err != nil {
				return nil, err
			}
			if doc == nil {
				return nil, fmt.Errorf("expected a JSON object")
			}
		}
		if err := edit(doc); err != nil {
			return nil, err
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		return append(data, '\n'), err
	})
}

func object(parent map[string]any, key string) (map[string]any, error) {
	if value, ok := parent[key]; ok {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", key)
		}
		return m, nil
	}
	m := map[string]any{}
	parent[key] = m
	return m, nil
}
func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func setOwned(parent map[string]any, key string, value any) error {
	if old, ok := parent[key]; ok && !jsonEqual(old, value) {
		return fmt.Errorf("existing %s entry differs; preserved for manual review", key)
	}
	parent[key] = value
	return nil
}
func mergeHook(parent map[string]any, event string, entry any, client string) error {
	list := []any{}
	if old, ok := parent[event]; ok {
		var valid bool
		list, valid = old.([]any)
		if !valid {
			return fmt.Errorf("%s must be an array", event)
		}
	}
	kept := make([]any, 0, len(list)+1)
	phase := "pre-tool"
	if event == "PostToolUse" {
		phase = "post-tool"
	}
	legacy := map[string]any{"matcher": ".*", "command": "tzro hook " + client + " " + phase}
	found := false
	for _, old := range list {
		// Migrate only the exact entry emitted by the former installer.
		if jsonEqual(old, legacy) {
			continue
		}
		if jsonEqual(old, entry) {
			if !found {
				kept = append(kept, old)
			}
			found = true
			continue
		}
		encoded, _ := json.Marshal(old)
		if strings.Contains(string(encoded), " hook "+client+" native-") {
			return fmt.Errorf("modified tzro %s hook preserved; review before reinitializing", event)
		}
		kept = append(kept, old)
	}
	if !found {
		kept = append(kept, entry)
	}
	parent[event] = kept
	return nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func addTOMLServer(path, binary string) (bool, error) {
	return updateFile(path, 0600, func(old []byte) ([]byte, error) {
		doc := map[string]any{}
		if err := toml.Unmarshal(old, &doc); err != nil {
			return nil, err
		}
		servers, err := object(doc, "mcp_servers")
		if err != nil {
			return nil, err
		}
		wanted := map[string]any{"command": binary, "args": []any{"mcp"}}
		if value, ok := servers["tzro"]; ok {
			if !reflect.DeepEqual(value, wanted) {
				return nil, fmt.Errorf("existing tzro MCP entry differs; preserved for manual review")
			}
			return old, nil
		}
		block, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"tzro": wanted}})
		if err != nil {
			return nil, err
		}
		// Retain comments, ordering, credentials, and all unrelated TOML text.
		combined := append(append(append([]byte{}, old...), '\n'), block...)
		var check map[string]any
		if err := toml.Unmarshal(combined, &check); err != nil {
			return nil, fmt.Errorf("cannot append tzro MCP table safely; existing configuration preserved: %w", err)
		}
		return combined, nil
	})
}
