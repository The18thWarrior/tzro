package turnreduction

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Preserve the ordinary installation and bind its server to the same task deadline.
func bindMCPDeadline(home, workspace string, deadline time.Time) error {
	path := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	var servers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(config["mcpServers"], &servers); err != nil {
		return err
	}
	server, ok := servers["tzro"]
	if !ok {
		return fmt.Errorf("installed Tzro MCP server missing")
	}
	var args []string
	if err := json.Unmarshal(server["args"], &args); err != nil {
		return err
	}
	server["args"], err = json.Marshal(append(args, "--workspace", workspace, "--deadline", deadline.UTC().Format(time.RFC3339Nano)))
	if err != nil {
		return err
	}
	config["mcpServers"], err = json.Marshal(servers)
	if err != nil {
		return err
	}
	return writeJSON(path, config)
}
