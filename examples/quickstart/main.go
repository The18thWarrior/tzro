package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/memory"
	"tzro/internal/stream"
	"tzro/internal/tools"
)

// CustomMathTool is a custom user-defined tool that implements the tools.Tool interface.
type CustomMathTool struct{}

func (c *CustomMathTool) Name() string {
	return "custom_math_tool"
}

func (c *CustomMathTool) GetSchema() (string, error) {
	return `{
		"type": "object",
		"properties": {
			"a": {"type": "number", "description": "First number"},
			"b": {"type": "number", "description": "Second number"}
		},
		"required": ["a", "b"]
	}`, nil
}

func (c *CustomMathTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	a, ok1 := args["a"].(float64)
	b, ok2 := args["b"].(float64)
	if !ok1 || !ok2 {
		return "", fmt.Errorf("invalid arguments: a and b must be numbers")
	}

	result := a + b
	// NOTE: Always write runtime logs and debug information to os.Stderr.
	// If these tools are executed within an MCP server, printing to os.Stdout
	// will corrupt the JSON-RPC communication channel.
	fmt.Fprintf(os.Stderr, "[CustomMathTool] Calculating: %g + %g = %g\n", a, b, result)

	resp := map[string]interface{}{
		"result": result,
		"status": "success",
	}
	bytes, _ := json.Marshal(resp)
	return string(bytes), nil
}

// CustomSlackTool is a second custom tool illustrating multi-tool registries.
type CustomSlackTool struct{}

func (c *CustomSlackTool) Name() string {
	return "slack_message"
}

func (c *CustomSlackTool) GetSchema() (string, error) {
	return `{
		"type": "object",
		"properties": {
			"channel": {"type": "string", "description": "Target channel name"},
			"text": {"type": "string", "description": "Message text"}
		},
		"required": ["channel", "text"]
	}`, nil
}

func (c *CustomSlackTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	channel, _ := args["channel"].(string)
	text, _ := args["text"].(string)

	// NOTE: Direct tool output logs to os.Stderr to safeguard the MCP stdio stream.
	fmt.Fprintf(os.Stderr, "[CustomSlackTool] Posting message to channel #%s: %s\n", channel, text)

	resp := map[string]interface{}{
		"status":    "delivered",
		"channel":   channel,
		"timestamp": time.Now().Unix(),
	}
	bytes, _ := json.Marshal(resp)
	return string(bytes), nil
}

// RunQuickstart runs a full local quickstart workflow end-to-end.
func RunQuickstart(ctx context.Context, dbPath string) error {
	fmt.Println("--- Initializing tzro Quickstart ---")

	// 1. Configure local SQLite database
	memory.DB.SetDBPathForTesting(dbPath)
	if err := memory.DB.Init(); err != nil {
		return fmt.Errorf("failed to init DB: %w", err)
	}
	defer memory.DB.Close()

	// 2. Register custom tools
	mathTool := &CustomMathTool{}
	tools.Register(mathTool)
	defer tools.Unregister(mathTool.Name())

	slackTool := &CustomSlackTool{}
	tools.Register(slackTool)
	defer tools.Unregister(slackTool.Name())

	// 3. Define a Directed Acyclic Graph (DAG) program
	// This graph will first run custom_math_tool, then log/slack the result.
	graph := &compiler.ExecutionGraph{
		TaskID:    "t_quickstart_demo",
		CreatedAt: time.Now().Unix(),
		Nodes: []compiler.GraphNode{
			{
				ID:           "node_01",
				Type:         "action",
				Action:       "custom_math_tool",
				Instructions: "Calculate the sum of 15 and 35.",
				AllowedTools: []string{"custom_math_tool"},
				Status:       "pending",
			},
			{
				ID:           "node_02",
				Type:         "action",
				Action:       "slack_message",
				Instructions: "Confirm the sum is {{nodes.node_01.output.result}}.",
				AllowedTools: []string{"slack_message"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "node_01", TargetID: "node_02"},
		},
	}

	// 4. Subscribe to the Global SSE StreamBus to monitor progress
	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		// Filter events relating to our task
		return chunk.TaskID == graph.TaskID
	})
	defer sub.Unsubscribe()

	// Launch background monitor goroutine to display real-time events
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-sub.Ch:
				if !ok {
					return
				}
				fmt.Printf("[SSE Stream Event] Source: %s | Node: %s | Type: %s | Info: %s\n",
					chunk.Source, chunk.NodeID, chunk.Type, chunk.Content)
			}
		}
	}()

	// 5. Compile and Sort DAG via Kahn's algorithm
	levels, err := compiler.CompileAndSort(graph)
	if err != nil {
		return fmt.Errorf("failed to compile and sort DAG: %w", err)
	}

	fmt.Printf("[Compiler] Kahn topological levels: %v\n", levels)

	// 6. Execute the workflow graph
	err = executor.GlobalEngine.ExecuteGraph(ctx, graph, levels)
	if err != nil {
		return fmt.Errorf("failed to execute workflow graph: %w", err)
	}

	fmt.Println("--- tzro Quickstart Completed Successfully ---")
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPath := "tzro_quickstart.db"
	defer os.Remove(dbPath)

	if err := RunQuickstart(ctx, dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "Quickstart failed: %v\n", err)
		os.Exit(1)
	}
}
