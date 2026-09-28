package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
)

func discoverSkill(ctx context.Context, cfg Config, p *prepared) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := command(ctx, p.env, p.result.Workspace, cfg.PiBinary, "--mode", "rpc", "--no-session", "--offline", "--provider", "tzro-workflow", "--model", cfg.Model)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	var stderr outputBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return false, err
	}
	defer func() { cancel(); _ = stdin.Close(); _ = cmd.Wait() }()
	if err := json.NewEncoder(stdin).Encode(map[string]string{"type": "get_commands"}); err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		var event struct {
			Type, Command string
			Success       bool
			Data          struct {
				Commands []struct{ Name, Source string }
			}
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "response" || event.Command != "get_commands" {
			continue
		}
		if !event.Success {
			return false, fmt.Errorf("Pi resource discovery failed")
		}
		found := false
		for _, resource := range event.Data.Commands {
			if resource.Source == "skill" && resource.Name == "skill:tzro" {
				found = true
			} else {
				return false, fmt.Errorf("unexpected native resource %q in isolated profile", resource.Name)
			}
		}
		return found, nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, fmt.Errorf("Pi did not report resources")
}
