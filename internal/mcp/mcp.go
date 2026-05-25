package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var execCommandContext = exec.CommandContext

type MCPServerConfig struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	UseDocker   bool              `json:"useDocker,omitempty"`
	DockerImage string            `json:"dockerImage,omitempty"`
	DockerOpts  []string          `json:"dockerOpts,omitempty"`
}

type MCPConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

type MCPDaemon struct {
	Name        string
	Command     string
	Args        []string
	Env         map[string]string
	UseDocker   bool
	DockerImage string
	DockerOpts  []string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	scanner     *bufio.Scanner
	mutex       sync.Mutex
	Active      bool
	ctx         context.Context
}

type MCPRegistry struct {
	daemons map[string]*MCPDaemon
	mutex   sync.RWMutex
}

var GlobalRegistry = &MCPRegistry{
	daemons: make(map[string]*MCPDaemon),
}

func NewMCPDaemon(name string, config MCPServerConfig) *MCPDaemon {
	return &MCPDaemon{
		Name:        name,
		Command:     config.Command,
		Args:        config.Args,
		Env:         config.Env,
		UseDocker:   config.UseDocker,
		DockerImage: config.DockerImage,
		DockerOpts:  config.DockerOpts,
	}
}

// IsActive returns whether the daemon is currently active thread-safely
func (d *MCPDaemon) IsActive() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.Active
}

// startNoLock spawns the persistent daemon process and hooks pipes without locking
func (d *MCPDaemon) startNoLock(ctx context.Context) error {
	d.ctx = ctx

	if d.UseDocker {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			homeDir = "."
		}
		mountArg := fmt.Sprintf("%s/.tzro:/root/.tzro", homeDir)

		dockerArgs := []string{"run", "-i", "--rm"}
		dockerArgs = append(dockerArgs, d.DockerOpts...)
		dockerArgs = append(dockerArgs, "-v", mountArg)

		// Inject strictly declared resolved env variables via -e
		for k, v := range d.Env {
			val := v
			if strings.HasPrefix(v, "$") {
				envVarName := strings.TrimPrefix(v, "$")
				val = os.Getenv(envVarName)
			}
			dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, val))
		}

		dockerArgs = append(dockerArgs, d.DockerImage, d.Command)
		dockerArgs = append(dockerArgs, d.Args...)

		d.cmd = execCommandContext(ctx, "docker", dockerArgs...)
	} else {
		d.cmd = execCommandContext(ctx, d.Command, d.Args...)

		// Inherit system environment variables
		envList := os.Environ()
		for k, v := range d.Env {
			// Resolve $VAR references
			val := v
			if strings.HasPrefix(v, "$") {
				envVarName := strings.TrimPrefix(v, "$")
				val = os.Getenv(envVarName)
			}
			envList = append(envList, fmt.Sprintf("%s=%s", k, val))
		}
		d.cmd.Env = envList
	}

	var err error
	d.stdin, err = d.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe error: %w", err)
	}

	d.stdout, err = d.cmd.StdoutPipe()
	if err != nil {
		_ = d.stdin.Close()
		d.stdin = nil
		return fmt.Errorf("stdout pipe error: %w", err)
	}

	d.stderr, err = d.cmd.StderrPipe()
	if err == nil {
		// Log stderr in goroutine so it doesn't block stdout reading
		go func() {
			scanner := bufio.NewScanner(d.stderr)
			for scanner.Scan() {
				// Simply drop or print internally
				// fmt.Printf("[%s STDERR] %s\n", d.Name, scanner.Text())
			}
		}()
	}

	d.scanner = bufio.NewScanner(d.stdout)

	if err := d.cmd.Start(); err != nil {
		_ = d.stdin.Close()
		_ = d.stdout.Close()
		if d.stderr != nil {
			_ = d.stderr.Close()
		}
		d.stdin = nil
		d.stdout = nil
		d.stderr = nil
		d.scanner = nil
		return fmt.Errorf("process start error: %w", err)
	}

	d.Active = true
	return nil
}

// stopNoLock terminates the running process and cleans up resources without locking
func (d *MCPDaemon) stopNoLock() error {
	d.Active = false
	if d.stdin != nil {
		_ = d.stdin.Close()
		d.stdin = nil
	}
	if d.stdout != nil {
		_ = d.stdout.Close()
		d.stdout = nil
	}
	if d.stderr != nil {
		_ = d.stderr.Close()
		d.stderr = nil
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
		_ = d.cmd.Wait()
		d.cmd = nil
	}
	d.scanner = nil
	return nil
}

// restartNoLock terminates any existing dead process, flushes resources, and re-starts a new process without locking
func (d *MCPDaemon) restartNoLock(ctx context.Context) error {
	_ = d.stopNoLock()
	return d.startNoLock(ctx)
}

// Start spawns the persistent daemon process and hooks pipes thread-safely
func (d *MCPDaemon) Start(ctx context.Context) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if d.Active {
		return nil
	}

	return d.startNoLock(ctx)
}

// Call routes dynamic JSON-RPC 2.0 requests over the open stdio pipes with auto-recovery and thread safety
func (d *MCPDaemon) Call(method string, params map[string]interface{}) (string, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	ctx := d.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if !d.Active {
		if err := d.startNoLock(ctx); err != nil {
			return "", fmt.Errorf("daemon %s is not running and failed to start: %w", d.Name, err)
		}
	}

	rpcBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  method,
		"params":  rpcParams(method, params),
	}
	bodyBytes, err := json.Marshal(rpcBody)
	if err != nil {
		return "", err
	}

	for attempt := 1; attempt <= 2; attempt++ {
		if d.cmd == nil || d.cmd.Process == nil || d.stdin == nil || d.scanner == nil {
			if attempt == 2 {
				return "", fmt.Errorf("MCP daemon %s process is not running", d.Name)
			}
			if err := d.restartNoLock(ctx); err != nil {
				return "", fmt.Errorf("failed to auto-recover daemon: %w", err)
			}
			continue
		}

		// Send message over stdin
		if _, err := fmt.Fprintln(d.stdin, string(bodyBytes)); err != nil {
			if attempt == 1 {
				if err := d.restartNoLock(ctx); err == nil {
					continue
				}
			}
			return "", fmt.Errorf("failed to send RPC call over stdin: %w", err)
		}

		// Read response over stdout
		if d.scanner.Scan() {
			return d.scanner.Text(), nil
		}

		scanErr := d.scanner.Err()
		if attempt == 1 {
			if err := d.restartNoLock(ctx); err == nil {
				continue
			}
		}

		if scanErr != nil {
			return "", fmt.Errorf("scanner error during RPC call: %w", scanErr)
		}
		return "", io.EOF
	}

	return "", io.EOF
}

func rpcParams(method string, params map[string]interface{}) map[string]interface{} {
	if method == "tools/call" {
		return params
	}
	return map[string]interface{}{}
}

// Stop terminates the running child process safely
func (d *MCPDaemon) Stop() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	return d.stopNoLock()
}

// LoadConfig parses a local config JSON file and initializes daemons
func (r *MCPRegistry) LoadConfig(configPath string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Shut down any existing daemons first
	for _, d := range r.daemons {
		_ = d.Stop()
	}
	r.daemons = make(map[string]*MCPDaemon)

	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("MCP configuration file '%s' does not exist; please create a valid configuration", configPath)
		}
		return err
	}
	defer file.Close()

	var cfg MCPConfig
	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return err
	}

	for name, item := range cfg.MCPServers {
		r.daemons[name] = NewMCPDaemon(name, item)
	}

	return nil
}

func (r *MCPRegistry) GetDaemon(name string) (*MCPDaemon, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	d, ok := r.daemons[name]
	return d, ok
}

func (r *MCPRegistry) GetList() map[string]MCPServerConfig {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	res := make(map[string]MCPServerConfig)
	for name, d := range r.daemons {
		res[name] = MCPServerConfig{
			Command: d.Command,
			Args:    d.Args,
			Env:     d.Env,
		}
	}
	return res
}

// ToolInfo represents the metadata of an MCP tool as returned by tools/list
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type ToolsListResult struct {
	Tools []ToolInfo `json:"tools"`
}

type ToolsListResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  ToolsListResult `json:"result"`
}

// ListTools queries the MCP daemon's tools/list method
func (d *MCPDaemon) ListTools(ctx context.Context) ([]ToolInfo, error) {
	respStr, err := d.Call("tools/list", nil)
	if err != nil {
		return nil, err
	}

	var resp ToolsListResponse
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list response: %w (raw response: %s)", err, respStr)
	}

	return resp.Result.Tools, nil
}

// DiscoverTools queries all daemons (starting them if necessary) and gathers all dynamic tools
func (r *MCPRegistry) DiscoverTools(ctx context.Context) (map[string]ToolInfo, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	toolsMap := make(map[string]ToolInfo)
	for _, d := range r.daemons {
		if !d.IsActive() {
			if err := d.Start(ctx); err != nil {
				fmt.Printf("[MCP Discovery Warning] Failed to start daemon %s: %v\n", d.Name, err)
				continue
			}
		}

		tools, err := d.ListTools(ctx)
		if err != nil {
			fmt.Printf("[MCP Discovery Warning] Failed to list tools for daemon %s: %v\n", d.Name, err)
			continue
		}

		for _, t := range tools {
			toolsMap[t.Name] = t
		}
	}

	return toolsMap, nil
}

// FindDaemonForTool finds and returns the daemon that hosts the given tool name, spawning it if inactive.
func (r *MCPRegistry) FindDaemonForTool(ctx context.Context, toolName string) (*MCPDaemon, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// 1. First, check already active daemons
	for _, d := range r.daemons {
		if d.IsActive() {
			tools, err := d.ListTools(ctx)
			if err == nil {
				for _, t := range tools {
					if t.Name == toolName {
						return d, true
					}
				}
			}
		}
	}

	// 2. If not found in active daemons, check inactive ones
	for _, d := range r.daemons {
		if !d.IsActive() {
			if err := d.Start(ctx); err == nil {
				tools, err := d.ListTools(ctx)
				if err == nil {
					for _, t := range tools {
						if t.Name == toolName {
							return d, true
						}
					}
				}
			}
		}
	}

	return nil, false
}

// GetGBNFSchema wraps raw tool parameters in a GBNF-compatible JSON schema structure
func GetGBNFSchema(inputSchema map[string]interface{}) (string, error) {
	wrapped := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_arguments": inputSchema,
		},
		"required": []string{"tool_arguments"},
	}
	bytes, err := json.Marshal(wrapped)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

