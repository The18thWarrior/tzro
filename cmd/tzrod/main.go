package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"tzro/internal/cache"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/observer"
	"tzro/internal/packagemanager"
	"tzro/internal/pidlock"
	"tzro/internal/proactivity"
	"tzro/internal/sentinel"
	"tzro/internal/server"
	"tzro/internal/services"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
	"tzro/internal/workflow"
)

func main() {
	// Singleton guard: ensure only one tzrod per workspace
	lockDir := config.ResolvePath(".")
	_ = os.MkdirAll(lockDir, 0755)
	lockPath := filepath.Join(lockDir, "daemon.lock")
	unlock, lockErr := pidlock.Acquire(lockPath)
	if lockErr != nil {
		var alreadyRunning *pidlock.ErrAlreadyRunning
		if errors.As(lockErr, &alreadyRunning) {
			fmt.Printf("[tzrod] Another daemon is already running (PID %d). Exiting.\n", alreadyRunning.HolderPID)
			os.Exit(0)
		}
		log.Fatalf("[tzrod] Failed to acquire daemon lockfile: %v", lockErr)
	}
	defer unlock()

	fmt.Println("==========================================================")
	fmt.Println("          tzro - Durable Agentic Execution Engine         ")
	fmt.Println("==========================================================")

	// 1. Initialize Relational/Document SQLite-simulated JSON DB
	fmt.Println("[Init] Initializing local JSON Graph Database...")
	err := memory.DB.Init()
	if err != nil {
		log.Fatalf("[Init Error] Failed to start database: %v\n", err)
	}

	// Wire durable metrics persistence — inference TPS and cache hit rate
	// now survive daemon restarts via SQLite instead of ephemeral in-memory atomics.
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
	fmt.Println("[Init] Durable metrics persistence wired (inference TPS + cache hit rate).")

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
			// StartActive starts both the worker (or ActiveBackend) and the
			// router sidecar in parallel. Previously only ActiveBackend.Start()
			// was called here, which is a no-op for remote backends, leaving
			// the router sidecar dead and collapsing dual-sidecar routing.
			if err := inference.StartActive(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "[Init] Inference backend pre-warm failed: %v\n", err)
			}
		}()
	}

	// 4. Load MCP server configuration setting stdio hosts
	configPath := config.ResolvePath("mcp_config.json")
	fmt.Printf("[Init] Loading MCP configuration setting from %s...\n", configPath)
	err = mcp.GlobalRegistry.LoadConfig(configPath)
	if err != nil {
		fmt.Printf("[Init Warning] Failed to load MCP configuration from %s: %v. MCP tools will not be available.\n", configPath, err)
	}

	// 4.5. Initialize Dynamic Tool Registry
	toolSchemasPath := config.ResolvePath("tool_schemas.json")
	fmt.Printf("[Init] Initializing dynamic Tool Registry from %s...\n", toolSchemasPath)
	if err := tools.Init(toolSchemasPath); err != nil {
		fmt.Printf("[Init Warning] Failed to initialize Tool Registry: %v\n", err)
	}

	// 5. Register background services via declarative ServiceRegistry
	fmt.Println("[Init] Registering background services...")
	svcRegistry := services.NewRegistry()

	// Observer Agent
	svcRegistry.Register(services.ServiceDef{
		Name:    "observer",
		Type:    "background_agent",
		Enabled: cfg.IsObserverEnabled(),
		Start: func() error {
			fmt.Println("[Init] Injecting LLM client adapter into Telemetry Observer...")
			observer.SetLLMClient(&TelemetryLLMAdapter{
				manager: inference.GlobalLocalModel,
			})
			fmt.Println("[Init] Spawning background debouncer Observer...")
			observer.Start()
			return nil
		},
		Stop: func() error { return nil },
	})

	// Sentinel Agent (ADR-0023)
	svcRegistry.Register(services.ServiceDef{
		Name:    "sentinel",
		Type:    "background_agent",
		Enabled: cfg.IsSentinelEnabled(),
		Start: func() error {
			fmt.Println("[Init] Injecting LLM client adapter into Sentinel Agent...")
			sentinel.SetLLMClient(&TelemetryLLMAdapter{
				manager: inference.GlobalLocalModel,
			})
			fmt.Println("[Init] Spawning background Sentinel heartbeat...")
			sentinel.Start()
			return nil
		},
		Stop: func() error { return nil },
	})

	// Attention Scheduler (Proactivity subsystem)
	svcRegistry.Register(services.ServiceDef{
		Name:    "attention_scheduler",
		Type:    "scheduler",
		Enabled: true,
		Start: func() error {
			fmt.Println("[Init] Starting Proactivity AttentionScheduler...")
			_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewObserverDaemon())
			_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewCompactorDaemon())
			_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewReconcilerDaemon())
			_ = proactivity.GlobalScheduler.RegisterDaemon(proactivity.NewPrefetcherDaemon())
			_ = proactivity.GlobalScheduler.Start(context.Background())
			return nil
		},
		Stop: func() error { return nil },
	})

	// 5.25. Register Hooks Globally
	fmt.Println("[Init] Registering global hooks...")
	executor.GlobalEngine.RegisterHook(&executor.McpApprovalHook{})
	executor.GlobalEngine.RegisterHook(&executor.ClientToolHook{})

	// 5.5. Initialize background cron scheduler and run Boot Recovery
	fmt.Println("[Init] Starting background cron scheduler & recovering interrupted workflows...")

	// Register system_dashboard workflow on boot if not present
	fmt.Println("[Init] Registering system dashboard workflow...")
	systemWF := memory.WorkflowDefinition{
		ID:                "system_dashboard",
		Name:              "System Dashboard Spec Generator",
		Description:       "Generates the system dashboard layout specification JSON dynamically.",
		TriggerType:       "cron",
		TriggerConfig:     "0 */4 * * *",
		Status:            "active",
		OrchestrationMode: "static",
		CreatedAt:         time.Now().Unix(),
		UpdatedAt:         time.Now().Unix(),
	}
	systemWFTasks := []memory.WorkflowTask{
		{
			WorkflowID:     "system_dashboard",
			TaskTemplateID: "generate_spec",
			Name:           "Generate Dashboard Spec",
			Instructions:   "Generate system dashboard spec",
			Dependencies:   "",
		},
	}
	// Check if already registered first
	wfs, err := memory.DB.GetWorkflows()
	exists := false
	if err == nil {
		for _, w := range wfs {
			if w.ID == "system_dashboard" {
				exists = true
				break
			}
		}
	}
	if !exists {
		err = memory.DB.SaveWorkflow(systemWF, systemWFTasks)
		if err != nil {
			fmt.Printf("[Init Warning] Failed to register system_dashboard workflow: %v\n", err)
		} else {
			fmt.Println("[Init] Registered system_dashboard workflow successfully.")
		}
	}

	workflow.Scheduler.Start(context.Background())
	workflow.RecoverInterruptedWorkflows(context.Background())

	// Start all enabled background services
	fmt.Println("[Init] Starting all enabled background services...")
	svcRegistry.StartAll()
	for _, s := range svcRegistry.List() {
		if !s.Enabled {
			fmt.Printf("[Init] Service '%s' is disabled per configuration settings.\n", s.Name)
		} else {
			fmt.Printf("[Init] Service '%s' → %s\n", s.Name, s.Status)
		}
	}

	// 6. Start Unified REST Endpoint API & serve GUI Dashboard static assets
	port := ":8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = ":" + envPort
	}

	// Load active apps on boot
	fmt.Println("[Init] Loading installed Agent Apps...")
	db := memory.DB.RawDB()
	if db != nil {
		appsDir := config.ResolvePath("apps")
		mgr := packagemanager.NewManager(db, mcp.GlobalRegistry, appsDir)
		if err := mgr.LoadInstalledApps(); err != nil {
			fmt.Printf("[Init Warning] Failed to load installed apps: %v\n", err)
		}
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
		res, err := inference.ActiveBackend.CallModel(ctx, []inference.InferenceMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}}, jsonSchema)
		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	return a.manager.ExecuteStructured(ctx, inference.NewSimpleRequest(systemPrompt, userPrompt, jsonSchema))
}
