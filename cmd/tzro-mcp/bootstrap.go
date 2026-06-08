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
	"tzro/internal/proactivity"
	"tzro/internal/sentinel"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
)

// TelemetryLLMAdapter routes Observer reflection prompts using the ActiveBackend.
type TelemetryLLMAdapter struct {
	backend inference.InferenceBackend
}

// CallModel delegates the inference call to the ActiveBackend.
func (a *TelemetryLLMAdapter) CallModel(ctx context.Context, systemPrompt, userPrompt string, jsonSchema string) (string, error) {
	if a.backend == nil {
		return "", fmt.Errorf("no active inference backend configured for observer")
	}
	res, err := a.backend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, jsonSchema)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

func bootstrapEngine() {
	// 1. Initialize Memory DB (SQLite WAL mode)
	if err := memory.DB.Init(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// 2. Load configurations
	if err := config.Load(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	cfg := config.Get()

	// 3. Initialize Inference Backend
	inference.ActiveBackend = inference.NewBackend(cfg.InferenceBackend, telemetry.Default)
	if err := inference.ActiveBackend.Start(context.Background()); err != nil {
		log.Printf("[Warning] Failed to start inference backend: %v", err)
	}

	// 4. Load MCP Host tools (if configured)
	configPath := config.ResolvePath(filepath.Join(".tzro", "mcp_config.json"))
	if _, err := os.Stat(configPath); err == nil {
		if err := mcp.GlobalRegistry.LoadConfig(configPath); err != nil {
			log.Printf("[Warning] Failed to load MCP hosts config from %s: %v", configPath, err)
		}
	}

	// 5. Initialize Tool Registry
	toolSchemasPath := config.ResolvePath(filepath.Join(".tzro", "tool_schemas.json"))
	if err := tools.Init(toolSchemasPath); err != nil {
		log.Printf("[Warning] Failed to initialize dynamic tool registry: %v", err)
	}

	// 6. Conditionally Start Observer
	if cfg.IsObserverEnabled() {
		observer.SetLLMClient(&TelemetryLLMAdapter{
			backend: inference.ActiveBackend,
		})
		observer.Start()
	}

	// 7. Conditionally Start Sentinel (ADR-0023)
	if cfg.IsSentinelEnabled() {
		sentinel.SetLLMClient(&TelemetryLLMAdapter{
			backend: inference.ActiveBackend,
		})
		sentinel.Start()
	}

	// 8. Register Hooks Globally
	executor.GlobalEngine.RegisterHook(&executor.McpApprovalHook{})
	executor.GlobalEngine.RegisterHook(&executor.ClientToolHook{})

	// 9. Start Proactivity AttentionScheduler
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewObserverDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewCompactorDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewReconcilerDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewPrefetcherDaemon())
	_ = proactivity.GlobalScheduler.Start(context.Background())
}
