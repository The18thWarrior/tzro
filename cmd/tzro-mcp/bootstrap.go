package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"tzro/internal/cache"
	"tzro/internal/channel"
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
	"tzro/internal/workspace"
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

func bootstrapEngine(wsID, wsRoot string, extraPaths []string) {
	// 0. Run legacy migration (moves $TZRO_DIR/tzro.db → workspaces/default/)
	tzroDir := config.ResolvePath(".")
	if migrated, err := workspace.MigrateLegacy(tzroDir); err != nil {
		log.Printf("[tzro-mcp] Legacy migration failed: %v", err)
	} else if migrated {
		log.Println("[tzro-mcp] Migrated legacy tzro.db to workspaces/default/")
	}

	// 0b. Register workspace and point DB at workspace-specific path
	reg := workspace.NewRegistry(config.ResolvePath("workspaces"))
	if _, err := reg.Register(wsID, wsRoot); err != nil {
		log.Printf("[tzro-mcp] Failed to register workspace %s: %v", wsID, err)
	}
	_ = reg.Touch(wsID)

	// Set workspace-scoped DB path BEFORE Init()
	memory.DB.SetDBPath(reg.DBPath(wsID))
	log.Printf("[tzro-mcp] Workspace %s (root=%s) → DB: %s", wsID, wsRoot, reg.DBPath(wsID))

	// Set workspace-scoped filesystem access boundary
	allowedRoots := []string{}
	if wsRoot != "" {
		allowedRoots = append(allowedRoots, wsRoot)
	}
	allowedRoots = append(allowedRoots, extraPaths...)
	if len(allowedRoots) > 0 {
		tools.SetAllowedPathsOverride(allowedRoots)
	}

	// 1. Initialize Memory DB (SQLite WAL mode)
	if err := memory.DB.Init(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Wire durable metrics persistence (inference TPS + cache hit rate)
	inference.SetMetricsPersister(func(prompt, completion int, durationSec float64) {
		_ = memory.DB.RecordInferenceSample(prompt, completion, durationSec)
	})
	inference.SetMetricsQuerier(func(windowSeconds int64) float64 {
		return memory.DB.GetAverageTPS(windowSeconds)
	})
	cache.SetCacheEventPersister(func(hit bool) {
		_ = memory.DB.RecordCacheEvent(hit)
	})
	cache.SetCacheHitRateQuerier(func(windowSeconds int64) float64 {
		return memory.DB.GetDBCacheHitRate(windowSeconds)
	})

	// 2. Load configurations
	if err := config.Load(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	cfg := config.Get()

	// 3. Initialize Inference Backend (do NOT eagerly Start — the daemon owns the sidecar;
	//    tools lazy-start via adoption when first called, avoiding duplicate llama-server processes)
	inference.ActiveBackend = inference.NewBackend(cfg.InferenceBackend, telemetry.Default)

	// 4. Load MCP Host tools (if configured)
	configPath := config.ResolvePath("mcp_config.json")
	if _, err := os.Stat(configPath); err == nil {
		if err := mcp.GlobalRegistry.LoadConfig(configPath); err != nil {
			log.Printf("[Warning] Failed to load MCP hosts config from %s: %v", configPath, err)
		}
	}

	// 5. Initialize Tool Registry
	toolSchemasPath := config.ResolvePath("tool_schemas.json")
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

	// 8. Initialize Strategy Registry (ADR-0069)
	executor.GlobalEngine.InitRegistry()


	// 9. Register Hooks Globally
	executor.GlobalEngine.RegisterHook(&executor.McpApprovalHook{})
	executor.GlobalEngine.RegisterHook(channel.GlobalChannelToolHook) // v2: bidirectional dispatch
	executor.GlobalEngine.RegisterHook(&executor.ClientToolHook{})    // v1 fallback


	// 10. Start Proactivity AttentionScheduler
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewObserverDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewCompactorDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewReconcilerDaemon())
	_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewPrefetcherDaemon())
	_ = proactivity.GlobalScheduler.Start(context.Background())
}
