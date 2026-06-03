package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/observer"
	"tzro/internal/server"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
	"tzro/internal/workflow"
)

func main() {
	fmt.Println("==========================================================")
	fmt.Println("          tzro - Durable Agentic Execution Engine         ")
	fmt.Println("==========================================================")

	// 1. Initialize Relational/Document SQLite-simulated JSON DB
	fmt.Println("[Init] Initializing local JSON Graph Database...")
	err := memory.DB.Init()
	if err != nil {
		log.Fatalf("[Init Error] Failed to start database: %v\n", err)
	}

	// 2. Load persistent global configurations
	fmt.Println("[Init] Loading global persistent settings...")
	if err := config.Load(); err != nil {
		log.Fatalf("[Init Error] Failed to load settings: %v\n", err)
	}

	// 3. Initialize and pre-warm the pluggable inference backend if enabled
	cfg := config.Get()
	inference.ActiveBackend = inference.NewBackend(cfg.InferenceBackend, telemetry.Default)
	if cfg.SidecarEnabled || cfg.InferenceBackend.Type != "" {
		fmt.Println("[Init] Pre-warming active inference backend in background thread...")
		go func() {
			_ = inference.ActiveBackend.Start(context.Background())
		}()
	}

	// 4. Load MCP server configuration setting stdio hosts
	configPath := filepath.Join(".tzro", "mcp_config.json")
	fmt.Printf("[Init] Loading MCP configuration setting from %s...\n", configPath)
	err = mcp.GlobalRegistry.LoadConfig(configPath)
	if err != nil {
		fmt.Printf("[Init Warning] Failed to load MCP configuration from %s: %v. MCP tools will not be available.\n", configPath, err)
	}

	// 4.5. Initialize Dynamic Tool Registry
	toolSchemasPath := filepath.Join(".tzro", "tool_schemas.json")
	fmt.Printf("[Init] Initializing dynamic Tool Registry from %s...\n", toolSchemasPath)
	if err := tools.Init(toolSchemasPath); err != nil {
		fmt.Printf("[Init Warning] Failed to initialize Tool Registry: %v\n", err)
	}

	// 5. Spawn background debounced event monitor Observer (if enabled)
	if cfg.IsObserverEnabled() {
		fmt.Println("[Init] Injecting LLM client adapter into Telemetry Observer...")
		observer.SetLLMClient(&TelemetryLLMAdapter{
			manager: inference.GlobalLocalModel,
		})

		fmt.Println("[Init] Spawning background debouncer Observer...")
		observer.Start()
	} else {
		fmt.Println("[Init] Observer Agent is disabled per configuration settings.")
	}

	// 5.25. Register Hooks Globally
	fmt.Println("[Init] Registering global hooks...")
	executor.GlobalEngine.RegisterHook(&executor.McpApprovalHook{})
	executor.GlobalEngine.RegisterHook(&executor.ClientToolHook{})

	// 5.5. Initialize background cron scheduler and run Boot Recovery
	fmt.Println("[Init] Starting background cron scheduler & recovering interrupted workflows...")
	workflow.Scheduler.Start(context.Background())
	workflow.RecoverInterruptedWorkflows(context.Background())

	// 6. Start Unified REST Endpoint API & serve GUI Dashboard static assets
	port := ":8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = ":" + envPort
	}

	fmt.Printf("[Init] Ready! Open your browser to http://localhost%s/ to view the GUI\n", port)
	err = server.StartServer(port)
	if err != nil {
		log.Fatalf("[Server Error] Failed to run HTTP server: %v\n", err)
	}
}

// TelemetryLLMAdapter routes Observer reflection prompts using preemption-supported local/cloud routing.
type TelemetryLLMAdapter struct {
	manager *inference.LocalModelManager
}

func (a *TelemetryLLMAdapter) CallModel(ctx context.Context, systemPrompt, userPrompt string, jsonSchema string) (string, error) {
	if inference.ActiveBackend != nil {
		res, err := inference.ActiveBackend.CallModel(ctx, systemPrompt, userPrompt, jsonSchema)
		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	return a.manager.ExecuteStructured(ctx, inference.StructuredInferenceRequest{
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		JSONSchema:   jsonSchema,
	})
}
