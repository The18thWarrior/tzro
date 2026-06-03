package main

import (
	"context"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "1.0.0"

func main() {
	// Save the original stdout file descriptor/pointer
	realStdout := os.Stdout

	// Redirect os.Stdout to os.Stderr so all initialization print statements from internal
	// and third-party libraries go to stderr instead of corrupting the JSON-RPC stdio transport.
	os.Stdout = os.Stderr

	// Divert all standard logging to stderr
	log.SetOutput(os.Stderr)

	// Bootstrap engine subsystems (config, memory DB, inference, tools, observer)
	bootstrapEngine()

	// Restore real stdout for the MCP server's JSON-RPC stdio transport
	os.Stdout = realStdout

	// Initialize MCP server implementation
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tzro",
		Version: version,
	}, nil)

	// Register tzro-specific Phase 1 tools
	registerTools(server)

	// Run standard stdio transport (blocks until stdin EOF)
	log.Println("[tzro-mcp] Server is listening on stdin/stdout...")
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("[tzro-mcp] Server run error: %v", err)
	}
}
