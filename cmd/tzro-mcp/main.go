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
)

var version = "1.0.0"

func isDaemonRunning() bool {
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}
	url := fmt.Sprintf("http://localhost:%s/api/config", port)
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
	}
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	return false
}

func startDaemon() {
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
	logDir := config.ResolvePath(".tzro")
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
	logDir := config.ResolvePath(".tzro")

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

	// Check if daemon is running, if not start it
	if !isDaemonRunning() {
		startDaemon()
	}

	// Bootstrap engine subsystems (config, memory DB, inference, tools, observer)
	bootstrapEngine()

	// Initialize MCP server implementation
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tzro",
		Version: version,
	}, getResourcesServerOptions())

	// Register tzro-specific Phase 1 tools
	registerTools(server)

	// Register dynamic MCP resources
	registerResources(server)

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
