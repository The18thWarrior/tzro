package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/config"
	"tzro/internal/workspace"
)

var version = "1.0.0"

// mcpServer holds the *mcp.Server reference for use by tool handlers
// that need to create SubagentChannels (e.g., handleTzroRun, handleTzroWorkflow).
var mcpServer *mcp.Server

// engineReady is closed once bootstrapEngine() completes in the background goroutine.
// Tool handlers that need engine subsystems should call <-engineReady before proceeding.
var engineReady = make(chan struct{})

func isDaemonRunning() bool {
	if os.Getenv("TZRO_TESTING") == "true" {
		return false
	}

	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	// Try the port file URL first (most accurate when available)
	daemonURL := config.GetDaemonURL()
	if probeDaemon(client, daemonURL) {
		return true
	}

	// Fall back to the default port — the surviving daemon may be on :8080
	// while the port file is stale from a previous session.
	defaultURL := "http://127.0.0.1:8080"
	if defaultURL != daemonURL && probeDaemon(client, defaultURL) {
		// Update the port file so GetDaemonURL returns the correct URL
		_ = config.WriteDaemonPort(8080)
		log.Printf("[tzro-mcp] Found existing daemon at %s (port file was stale)\n", defaultURL)
		return true
	}

	return false
}

// probeDaemon sends a health check to the given daemon URL.
func probeDaemon(client *http.Client, baseURL string) bool {
	// Try /health first (lightweight, always 200 when healthy)
	resp, err := client.Get(fmt.Sprintf("%s/health", baseURL))
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}
	// Fall back to /api/config for older daemons that might not have /health
	resp, err = client.Get(fmt.Sprintf("%s/api/config", baseURL))
	if err == nil {
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	return false
}

func startDaemon() {
	if os.Getenv("TZRO_TESTING") == "true" {
		return
	}

	tzrodPath := config.FindBinary("tzrod")

	if tzrodPath == "" {
		log.Println("[tzro-mcp] Could not find tzrod executable to start daemon")
		return
	}

	log.Printf("[tzro-mcp] Starting daemon automatically: %s\n", tzrodPath)
	cmd := exec.Command(tzrodPath)
	cmd.Env = os.Environ()
	if envDir := os.Getenv("TZRO_DIR"); envDir != "" {
		cmd.Dir = envDir
	}

	// Redirect stdout/stderr to daemon.log
	logDir := config.ResolvePath(".")
	_ = os.MkdirAll(logDir, 0755)
	logFilePath := filepath.Join(logDir, "daemon.log")
	if daemonLog, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		cmd.Stdout = daemonLog
		cmd.Stderr = daemonLog
	}

	// Start detached from the current process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[tzro-mcp] Failed to start daemon: %v\n", err)
		return
	}
	log.Printf("[tzro-mcp] Daemon started successfully with PID %d\n", cmd.Process.Pid)
}

func main() {
	// Duplicate the original standard input (fd 0) and standard output (fd 1)
	// to new file descriptors to fully isolate the MCP JSON-RPC transport.
	realStdinFd, err := syscall.Dup(0)
	if err != nil {
		log.Fatalf("failed to dup stdin: %v", err)
	}
	realStdoutFd, err := syscall.Dup(1)
	if err != nil {
		log.Fatalf("failed to dup stdout: %v", err)
	}
	realStdin := os.NewFile(uintptr(realStdinFd), "/dev/stdin")
	realStdout := os.NewFile(uintptr(realStdoutFd), "/dev/stdout")

	// Determine log directory
	logDir := config.ResolvePath(".")

	// Redirect stdout (fd 1) and stderr (fd 2) at the OS level to a log file
	_ = os.MkdirAll(logDir, 0755)
	if logFile, err := os.OpenFile(filepath.Join(logDir, "mcp.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		_ = syscall.Dup2(int(logFile.Fd()), 1)
		_ = syscall.Dup2(int(logFile.Fd()), 2)
		log.SetOutput(logFile)
	} else {
		// Fallback to devNull if log file cannot be opened
		if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
			_ = syscall.Dup2(int(devNull.Fd()), 1)
			_ = syscall.Dup2(int(devNull.Fd()), 2)
		}
	}

	// NOTE: No singleton guard here — each IDE connection spawns its own
	// stdio transport process. The daemon itself is already singleton-guarded
	// via daemon.lock, so multiple MCP bridges are safe to coexist.

	// Workspace resolution: the InitializedHandler fires after the MCP
	// handshake completes, at which point we can call ListRoots() to discover
	// the IDE's workspace. The resolved workspace identity flows into
	// bootstrapEngine() to scope the DB and allowedPaths.
	type workspaceInfo struct {
		rootPath   string
		extraPaths []string
	}
	workspaceResolved := make(chan workspaceInfo, 1)

	// Initialize MCP server and register tools FIRST so the stdio transport
	// can start accepting the IDE's initialize handshake immediately.
	// Heavy subsystem bootstrap (daemon discovery, inference, observer, etc.)
	// runs in a background goroutine to avoid exceeding the IDE's connection timeout.
	serverOpts := getResourcesServerOptions()
	serverOpts.InitializedHandler = func(ctx context.Context, req *mcp.InitializedRequest) {
		// In tests, skip ListRoots to prevent unexpected roots/list requests on stdout
		if os.Getenv("TZRO_TESTING") == "true" {
			workspaceResolved <- workspaceInfo{}
			return
		}
		// Called once after the IDE sends notifications/initialized
		rootsResult, err := req.Session.ListRoots(ctx, nil)
		if err != nil {
			log.Printf("[tzro-mcp] ListRoots failed (client may not support roots): %v", err)
			workspaceResolved <- workspaceInfo{}
			return
		}
		rootPath, extras := workspace.ResolveFromRoots(rootsResult.Roots)
		log.Printf("[tzro-mcp] MCP roots resolved: root=%q, extras=%v", rootPath, extras)
		workspaceResolved <- workspaceInfo{rootPath: rootPath, extraPaths: extras}
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tzro",
		Version: version,
	}, serverOpts)
	mcpServer = server

	// Register tzro-specific tools (handlers will block on engineReady if needed)
	registerTools(server)

	// Register dynamic MCP resources
	registerResources(server)

	// Background: daemon discovery + workspace resolution + engine bootstrap
	go func() {
		if !isDaemonRunning() {
			startDaemon()
			if os.Getenv("TZRO_TESTING") != "true" {
				// Wait for the new daemon to become responsive (up to 5 seconds)
				for i := 0; i < 10; i++ {
					time.Sleep(500 * time.Millisecond)
					if isDaemonRunning() {
						log.Println("[tzro-mcp] Daemon is ready")
						break
					}
				}
			}
		} else {
			log.Println("[tzro-mcp] Reusing existing daemon at", config.GetDaemonURL())
		}

		// Wait for workspace identity resolution (from MCP roots or timeout)
		var wsRoot string
		var extraPaths []string
		select {
		case info := <-workspaceResolved:
			wsRoot = info.rootPath
			extraPaths = info.extraPaths
		case <-time.After(2 * time.Second):
			log.Println("[tzro-mcp] Workspace resolution timed out, checking env fallback")
		}

		// Fallback cascade: MCP roots → TZRO_WORKSPACE env → "default"
		if wsRoot == "" {
			wsRoot = workspace.ResolveFromEnv()
		}
		wsID := workspace.ID(wsRoot) // returns DefaultID for empty wsRoot
		log.Printf("[tzro-mcp] Using workspace %s (root=%q)", wsID, wsRoot)

		// Bootstrap engine subsystems (config, memory DB, inference, tools, observer)
		bootstrapEngine(wsID, wsRoot, extraPaths)

		// Signal that the engine is fully initialized
		close(engineReady)
		log.Println("[tzro-mcp] Engine bootstrap complete")
	}()

	// Run standard stdio transport (blocks until stdin EOF)
	log.Println("[tzro-mcp] Server is listening on stdin/stdout...")
	transport := &mcp.IOTransport{
		Reader: realStdin,
		Writer: realStdout,
	}
	if err := server.Run(context.Background(), transport); err != nil {
		log.Fatalf("[tzro-mcp] Server run error: %v", err)
	}
}
