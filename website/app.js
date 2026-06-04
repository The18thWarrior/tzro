/* =========================================================================
   TZRO WEBSITE PROTOTYPE: CORE CLIENT-SIDE ENGINE
   ========================================================================= */

document.addEventListener("DOMContentLoaded", () => {
  // =========================================================================
  // Mobile Navigation Drawer Toggle
  // =========================================================================
  const mobileMenuToggle = document.getElementById("mobile-menu-toggle");
  const mobileNavDrawer = document.getElementById("mobile-nav-drawer");
  const mobileNavLinks = document.querySelectorAll(".mobile-nav-link");

  if (mobileMenuToggle && mobileNavDrawer) {
    mobileMenuToggle.addEventListener("click", () => {
      const isOpen = mobileNavDrawer.classList.toggle("open");
      mobileMenuToggle.classList.toggle("active", isOpen);
      mobileMenuToggle.setAttribute("aria-expanded", isOpen);
    });

    // Close mobile menu when a link is clicked
    mobileNavLinks.forEach((link) => {
      link.addEventListener("click", () => {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      });
    });

    // Close when clicking outside of the drawer
    document.addEventListener("click", (e) => {
      if (
        mobileNavDrawer.classList.contains("open") &&
        !mobileNavDrawer.contains(e.target) &&
        !mobileMenuToggle.contains(e.target)
      ) {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      }
    });
  }

  // =========================================================================
  // 1. INITIALIZE ALL PLAYGROUNDS
  // =========================================================================
  let vASimIntervals = [];
  let vBSimIntervals = [];
  let vCSimIntervals = [];

  // =========================================================================
  // 2. VARIANT A: KAHN PARALLEL EXECUTION SIMULATOR
  // =========================================================================
  const vAStartBtn = document.getElementById("vA-start-btn");
  const vAResetBtn = document.getElementById("vA-reset-btn");
  const vAConsole = document.getElementById("vA-console");
  const vAHorizonPulse = document.getElementById("vA-horizon-pulse");

  const nodePlanner = document.getElementById("node-planner");
  const node01 = document.getElementById("node-01");
  const node02 = document.getElementById("node-02");
  const node03 = document.getElementById("node-03");

  function appendConsoleLine(text, cssClass = "") {
    const line = document.createElement("div");
    line.className = `log-line ${cssClass}`;
    line.textContent = `[${new Date().toLocaleTimeString()}] ${text}`;
    vAConsole.appendChild(line);
    vAConsole.scrollTop = vAConsole.scrollHeight;
  }

  function resetVariantASim() {
    vASimIntervals.forEach(clearTimeout);
    vASimIntervals = [];

    if (vAHorizonPulse) {
      vAHorizonPulse.classList.remove("pulsing");
      vAHorizonPulse.style.transform = "scaleX(0)";
      vAHorizonPulse.style.opacity = "0";
    }

    // Reset nodes to default visual state
    [nodePlanner, node01, node02, node03].forEach((n) => {
      if (n) {
        n.className = "sim-node pending";
      }
    });
    if (nodePlanner) nodePlanner.className = "sim-node planner";

    vAConsole.innerHTML =
      '<div class="log-line text-muted">Awaiting topological compilation triggers...</div>';
    vAStartBtn.disabled = false;
    vAStartBtn.textContent = "Compile & Run DAG";
  }

  function runVariantASim() {
    resetVariantASim();
    vAStartBtn.disabled = true;
    vAStartBtn.textContent = "Running Sim...";

    if (vAHorizonPulse) {
      vAHorizonPulse.classList.add("pulsing");
      vAHorizonPulse.style.transform = "scaleX(1)";
      vAHorizonPulse.style.opacity = "1";
    }

    appendConsoleLine(
      "Compiling abstract graph definition with Kahn Sorter...",
      "cyan",
    );

    // Phase 1: Planning / Strategist runs
    vASimIntervals.push(
      setTimeout(() => {
        nodePlanner.className = "sim-node running";
        appendConsoleLine(
          "Strategist (Gemini Cloud) called ONCE. Compiling Abstract execution blueprint...",
          "log-line",
        );
      }, 1000),
    );

    vASimIntervals.push(
      setTimeout(() => {
        nodePlanner.className = "sim-node completed";
        appendConsoleLine(
          "Strategic compilation complete. Sorted levels constructed programmatically.",
          "success",
        );
        appendConsoleLine(
          "Level sequence mapping: Level 0 [node_01, node_02] (Parallel) -> Level 1 [node_03] (Dependent)",
          "cyan",
        );
      }, 2500),
    );

    // Phase 2: Level 0 executes (Parallel Goroutines)
    vASimIntervals.push(
      setTimeout(() => {
        node01.className = "sim-node running";
        node02.className = "sim-node running";
        appendConsoleLine(
          "Level 0 goroutines spawned. Dispatching concurrent tasks in parallel.",
          "cyan",
        );
        appendConsoleLine(
          "Executing node_01 (archive_files) with GBNF grammar constraints...",
          "log-line",
        );
        appendConsoleLine(
          "Executing node_02 (fetch_user_details) as WASM sandboxed micro-skill...",
          "log-line",
        );
      }, 4500),
    );

    vASimIntervals.push(
      setTimeout(() => {
        node01.className = "sim-node completed";
        appendConsoleLine(
          "[Level 0: Goroutine 1] node_01 completed. Output: archive_report.zip generated.",
          "success",
        );
      }, 6500),
    );

    vASimIntervals.push(
      setTimeout(() => {
        node02.className = "sim-node completed";
        appendConsoleLine(
          "[Level 0: Goroutine 2] node_02 completed. Output: user profiles compiled (Size: 4.8KB).",
          "success",
        );
      }, 7200),
    );

    // Phase 3: Level 1 executes
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "All Level 0 dependency edges satisfied. Unblocking Level 1.",
          "cyan",
        );
        node03.className = "sim-node running";
        appendConsoleLine(
          "Executing node_03 (send_team_alert) using Stdio MCP gateway. Mapping parameters forwarded from node_01/node_02...",
          "log-line",
        );
      }, 8500),
    );

    vASimIntervals.push(
      setTimeout(() => {
        node03.className = "sim-node completed";
        appendConsoleLine(
          "[Level 1] node_03 completed. Notification pushed via Stdio Slack MCP server.",
          "success",
        );
      }, 10500),
    );

    // Terminal synthesis node
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "Terminal synthesis node initialized. Compiling summary block...",
          "cyan",
        );
      }, 11500),
    );

    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "TASK DEMO EXECUTION COMPLETE. Status: success. Cloud API calls: 1. Local tool dispatches: 3. Telemetry Stream: closed.",
          "success",
        );
        if (vAHorizonPulse) {
          vAHorizonPulse.classList.remove("pulsing");
          vAHorizonPulse.style.transform = "scaleX(0)";
          vAHorizonPulse.style.opacity = "0";
        }
        vAStartBtn.disabled = false;
        vAStartBtn.textContent = "Re-run DAG Task";
      }, 12800),
    );
  }

  if (vAStartBtn) vAStartBtn.addEventListener("click", runVariantASim);
  if (vAResetBtn) vAResetBtn.addEventListener("click", resetVariantASim);

  // =========================================================================
  // 3. VARIANT B: 5-LAYER CONTEXT COMPACTION PLAYGROUND
  // =========================================================================
  const vBCompressBtn = document.getElementById("vB-compress-btn");
  const rawPayloadDisplay = document.getElementById("raw-payload-display");
  const compactedPayloadDisplay = document.getElementById(
    "compacted-payload-display",
  );
  const diskCacheBanner = document.getElementById("disk-cache-banner");
  const compactorMetrics = document.getElementById("compactor-metrics");

  const metricSaving = document.getElementById("metric-saving");
  const metricInitial = document.getElementById("metric-initial");
  const metricFinal = document.getElementById("metric-final");
  const metricRatio = document.getElementById("metric-ratio");
  const rawSizeLabel = document.getElementById("raw-size");
  const compactedSizeLabel = document.getElementById("compacted-size");
  const rawSizeTab = document.getElementById("raw-size-tab");
  const compactedSizeTab = document.getElementById("compacted-size-tab");
  const compactorTabBtns = document.querySelectorAll(".compactor-tab-btn");
  const compactorRawPanel = document.getElementById("compactor-raw-panel");
  const compactorCompactedPanel = document.getElementById(
    "compactor-compacted-panel",
  );

  if (compactorTabBtns.length > 0) {
    compactorTabBtns.forEach((btn) => {
      btn.addEventListener("click", () => {
        compactorTabBtns.forEach((b) => b.classList.remove("active"));
        btn.classList.add("active");
        const target = btn.getAttribute("data-target");
        if (target === "raw") {
          if (compactorRawPanel) compactorRawPanel.classList.add("active");
          if (compactorCompactedPanel)
            compactorCompactedPanel.classList.remove("active");
        } else {
          if (compactorRawPanel) compactorRawPanel.classList.remove("active");
          if (compactorCompactedPanel)
            compactorCompactedPanel.classList.add("active");
        }
      });
    });
  }

  const presetBtns = document.querySelectorAll(".preset-btn");
  const pipelineSteps = [
    document.getElementById("step-0"),
    document.getElementById("step-1"),
    document.getElementById("step-2"),
    document.getElementById("step-3"),
    document.getElementById("step-4"),
  ];

  // Raw Presets Data
  const PAYLOAD_PRESETS = {
    crm: {
      size: "22.4",
      raw: `[\n  {\n    "id": 81092,\n    "accountName": "Vortex Enterprise Solutions Ltd",\n    "domain": "vortex-solutions.io",\n    "contact": {\n      "fullName": "Elizabeth Sterling",\n      "title": "VP of Technology Engineering",\n      "phone": "+1-415-555-0193",\n      "email": "e.sterling@vortex-solutions.io"\n    },\n    "billing": {\n      "plan": "Enterprise Select",\n      "monthlyCost": 4500.00,\n      "outstandingBalance": 0.00,\n      "address": "400 Mission St, San Francisco, CA 94105"\n    },\n    "activeIntegrations": ["salesforce", "github", "jira", "slack"],\n    "usageMetrics": {\n      "activeSeats": 240,\n      "monthlyTokenUsage": 45109228,\n      "latencyAveragesMs": 284\n    }\n  },\n  {\n    "id": 81093,\n    "accountName": "Nova Grid Laboratories Inc",\n    "domain": "novagrid.labs",\n    "contact": {\n      "fullName": "Dr. Aris Thorne",\n      "title": "Director of Decentralized Systems",\n      "phone": "+1-512-555-9011",\n      "email": "aris.thorne@novagrid.labs"\n    },\n    "billing": {\n      "plan": "Scale Pro Tier",\n      "monthlyCost": 1850.00,\n      "outstandingBalance": 1850.00,\n      "address": "901 Congress Ave, Austin, TX 78701"\n    },\n    "activeIntegrations": ["github", "aws"],\n    "usageMetrics": {\n      "activeSeats": 85,\n      "monthlyTokenUsage": 12891040,\n      "latencyAveragesMs": 195\n    }\n  }\n]`,
      compacted: `TSV: id\taccountName\tdomain\tcontact.fullName\tcontact.title\tbilling.plan\tbilling.monthlyCost\tusageMetrics.activeSeats\tactiveIntegrations\n81092\tVortex Enterprise Solutions Ltd\tvortex-solutions.io\tElizabeth Sterling\tVP of Technology Engineering\tEnterprise Select\t4500.00\t240\tsalesforce,github,jira,slack\n81093\tNova Grid Laboratories Inc\tnovagrid.labs\tDr. Aris Thorne\tDirector of Decentralized Systems\tScale Pro Tier\t1850.00\t85\tgithub,aws`,
      saving: "87.5%",
      finalSize: "2.8",
      ratio: "8:1 Reduction",
    },
    logs: {
      size: "48.1",
      raw: `{\n  "timestamp": "2026-06-02T13:44:09.102Z",\n  "level": "FATAL",\n  "sourceService": "tzro-compiler-go",\n  "threadId": 1408392189,\n  "exception": {\n    "message": "panic: runtime error: invalid memory address or nil pointer dereference",\n    "code": "SIGSEGV",\n    "stackTrace": [\n      "goroutine 81 [running]:",\n      "tzro/internal/compiler/sorter.go:192 - KahnSorter.DetectCycles()",\n      "tzro/internal/compiler/sorter.go:64 - KahnSorter.CompileAndSort(0xc00028e920)",\n      "tzro/internal/executor/executor.go:128 - ExecutionEngine.ExecuteGraph(0xc00010c2f0)",\n      "tzro/cmd/tzrod/main.go:88 - main.RunDaemon()",\n      "runtime/asm_amd64.s:1598 - goexit()"\n    ]\n  },\n  "environment": {\n    "os": "macOS 14.5",\n    "runtime": "go1.22.4",\n    "activeConnections": 12,\n    "systemLoad": 0.82\n  }\n}`,
      compacted: `FATAL EXCEPTION in tzro-compiler-go\npanic: runtime error: invalid memory address or nil pointer dereference\nTrace:\n - sorter.go:192 -> KahnSorter.DetectCycles()\n - sorter.go:64 -> KahnSorter.CompileAndSort()\n - executor.go:128 -> ExecutionEngine.ExecuteGraph()\n - main.go:88 -> main.RunDaemon()\nEnv: macOS 14.5 | go1.22.4 | SystemLoad: 0.82`,
      saving: "91.8%",
      finalSize: "3.9",
      ratio: "12:1 Reduction",
    },
    scraped: {
      size: "80.6",
      raw: `<!DOCTYPE html>\n<html>\n<head>\n  <title>TZRO Github Repository Documentation Page</title>\n</head>\n<body>\n  <div id="wrapper">\n    <header class="repo-header">\n      <h1 class="repo-title"><a href="https://github.com/The18thWarrior/tzro">The18thWarrior/tzro</a></h1>\n      <span class="star-count">Stars: 1420</span>\n    </header>\n    \n    <main class="content-body">\n      <div class="readme-preview">\n        <h2>Overview</h2>\n        <p class="summary-p">tzro is a durable, local-first agentic execution engine designed to coordinate complex multi-system automations securely on resource-constrained hardware.</p>\n        <p class="detail-p">By implementing a strategy-vs-tactics planner routing and topological sorted goroutine execution grids, tzro allows local lightweight GGUF models to call tools without arguments hallucination.</p>\n      </div>\n      \n      <aside class="sidebar-info">\n        <h3>Build Status</h3>\n        <div class="status-indicator">passing</div>\n        <h3>License</h3>\n        <span>Apache-2.0</span>\n      </aside>\n    </main>\n  </div>\n</body>\n</html>`,
      compacted: `URL: https://github.com/The18thWarrior/tzro\nStars: 1420 | Status: passing | License: Apache-2.0\nTitle: TZRO Github Repository Documentation Page\nContent:\n- tzro: durable, local-first agentic execution engine coordinating complex multi-system automations securely on resource-constrained hardware.\n- System uses strategy-vs-tactics planner and topological sorted goroutines to prevent local tool argument hallucinations.`,
      saving: "93.4%",
      finalSize: "5.3",
      ratio: "15:1 Reduction",
    },
  };

  let activePreset = "crm";

  function resetVariantBSim() {
    vBSimIntervals.forEach(clearTimeout);
    vBSimIntervals = [];

    compactedPayloadDisplay.textContent = "";
    compactedPayloadDisplay.style.display = "block";
    diskCacheBanner.style.display = "none";
    compactorMetrics.style.display = "none";

    pipelineSteps.forEach((step) => {
      if (step) {
        step.className = "pipeline-step-item";
        step.querySelector(".status").textContent = "Idle";
      }
    });

    // Reset displays
    const preset = PAYLOAD_PRESETS[activePreset];
    rawSizeLabel.textContent = preset.size;
    compactedSizeLabel.textContent = "0.0";
    rawPayloadDisplay.value = preset.raw;

    // Reset mobile tab elements
    if (rawSizeTab) rawSizeTab.textContent = preset.size;
    if (compactedSizeTab) compactedSizeTab.textContent = "0.0";

    // Switch mobile back to raw panel on reset/preset selection
    if (compactorRawPanel && compactorCompactedPanel) {
      compactorRawPanel.classList.add("active");
      compactorCompactedPanel.classList.remove("active");
      compactorTabBtns.forEach((b) => {
        if (b.getAttribute("data-target") === "raw") {
          b.classList.add("active");
        } else {
          b.classList.remove("active");
        }
      });
    }

    vBCompressBtn.disabled = false;
    vBCompressBtn.textContent = "Run Compaction Pipeline";
  }

  function handlePresetSelection(btn) {
    presetBtns.forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    activePreset = btn.getAttribute("data-preset");
    resetVariantBSim();
  }

  presetBtns.forEach((btn) => {
    btn.addEventListener("click", (e) =>
      handlePresetSelection(e.currentTarget),
    );
  });

  function runVariantBSim() {
    vBSimIntervals.forEach(clearTimeout);
    vBSimIntervals = [];

    vBCompressBtn.disabled = true;
    vBCompressBtn.textContent = "Compacting...";

    const preset = PAYLOAD_PRESETS[activePreset];
    let stepDelays = [500, 1200, 2000, 2800, 3500, 4200];

    // Layer 0: Binary Pruning
    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[0].className = "pipeline-step-item active";
        pipelineSteps[0].querySelector(".status").textContent = "Running";
      }, stepDelays[0]),
    );

    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[0].className = "pipeline-step-item completed";
        pipelineSteps[0].querySelector(".status").textContent = "Done";
      }, stepDelays[1]),
    );

    // Layer 1: HTML-to-Markdown
    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[1].className = "pipeline-step-item active";
        pipelineSteps[1].querySelector(".status").textContent = "Running";
      }, stepDelays[1]),
    );

    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[1].className = "pipeline-step-item completed";
        pipelineSteps[1].querySelector(".status").textContent = "Done";
      }, stepDelays[2]),
    );

    // Layer 2: TSV Hoisting
    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[2].className = "pipeline-step-item active";
        pipelineSteps[2].querySelector(".status").textContent = "Running";
      }, stepDelays[2]),
    );

    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[2].className = "pipeline-step-item completed";
        pipelineSteps[2].querySelector(".status").textContent = "Done";
      }, stepDelays[3]),
    );

    // Layer 3: KV Flattening
    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[3].className = "pipeline-step-item active";
        pipelineSteps[3].querySelector(".status").textContent = "Running";
      }, stepDelays[3]),
    );

    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[3].className = "pipeline-step-item completed";
        pipelineSteps[3].querySelector(".status").textContent = "Done";
      }, stepDelays[4]),
    );

    // Layer 4: Dot-Notation Tree Flattening
    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[4].className = "pipeline-step-item active";
        pipelineSteps[4].querySelector(".status").textContent = "Running";
      }, stepDelays[4]),
    );

    vBSimIntervals.push(
      setTimeout(() => {
        pipelineSteps[4].className = "pipeline-step-item completed";
        pipelineSteps[4].querySelector(".status").textContent = "Done";
      }, stepDelays[5]),
    );

    // Complete results
    vBSimIntervals.push(
      setTimeout(() => {
        compactedSizeLabel.textContent = preset.finalSize;
        if (compactedSizeTab) compactedSizeTab.textContent = preset.finalSize;

        // Auto switch to compacted tab on mobile when compaction finishes
        if (compactorRawPanel && compactorCompactedPanel) {
          compactorRawPanel.classList.remove("active");
          compactorCompactedPanel.classList.add("active");
          compactorTabBtns.forEach((b) => {
            if (b.getAttribute("data-target") === "compacted") {
              b.classList.add("active");
            } else {
              b.classList.remove("active");
            }
          });
        }

        // Determine if SQLite disk cache fallback gets triggered (emulated threshold check)
        const exceedsThreshold = parseFloat(preset.size) > 30; // Let's emulate threshold for logs & scraped

        if (exceedsThreshold) {
          compactedPayloadDisplay.style.display = "none";
          diskCacheBanner.style.display = "flex";
        } else {
          compactedPayloadDisplay.style.display = "block";
          compactedPayloadDisplay.textContent = preset.compacted;
          diskCacheBanner.style.display = "none";
        }

        // Show stats circles
        metricSaving.textContent = preset.saving;
        metricInitial.textContent = `${preset.size} KB`;
        metricFinal.textContent = `${preset.finalSize} KB`;
        metricRatio.textContent = preset.ratio;
        compactorMetrics.style.display = "flex";

        vBCompressBtn.disabled = false;
        vBCompressBtn.textContent = "Compaction Run Complete";
      }, stepDelays[5] + 500),
    );
  }

  if (vBCompressBtn) vBCompressBtn.addEventListener("click", runVariantBSim);

  // =========================================================================
  // 4. VARIANT C: GO DX & INTERACTIVE MIDDLEWARE HOOKS
  // =========================================================================
  const dxTabBtns = document.querySelectorAll(".dx-nav-btn");
  const dxFilename = document.getElementById("dx-filename");
  const dxCodeDisplay = document.getElementById("dx-code-display");

  const hookPII = document.getElementById("hook-pii");
  const hookGuard = document.getElementById("hook-guard");
  const hookHITL = document.getElementById("hook-hitl");
  const vCRunBtn = document.getElementById("vC-run-btn");
  const dxConsole = document.getElementById("dx-console");
  const hitlPrompt = document.getElementById("hitl-prompt");
  const hitlApproveBtn = document.getElementById("hitl-approve-btn");
  const hitlAbortBtn = document.getElementById("hitl-abort-btn");

  // SDK Code Snippets Source
  const DX_SNIPPETS = {
    config: {
      file: "config.go",
      code: `package main

import (
	"fmt"
	"tzro/internal/config"
)

func main() {
	// 1. Fetch engine configuration settings
	cfg := config.Get()
	fmt.Printf("Engine Mode: %s\\n", cfg.ModelMode) // cooperative, local, cloud

	// 2. Resolve environment-delegated credentials recursively
	// E.g., "$OPENAI_API_KEY" -> fetches active OS environment values
	apiKey := config.GetCloudAPIKey()
	fmt.Printf("Secret Decrypted: length = %d\\n", len(apiKey))
}`,
    },
    tools: {
      file: "tools_registry.go",
      code: `package main

import (
	"context"
	"encoding/json"
	"fmt"
	"tzro/internal/tools"
)

type FileArchiveTool struct{}

func (f *FileArchiveTool) Name() string {
	return "archive_files"
}

func (f *FileArchiveTool) GetSchema() (string, error) {
	return \`{
		"type": "object",
		"properties": {
			"sourcePath": {"type": "string", "description": "Target folder"},
			"compress": {"type": "boolean"}
		},
		"required": ["sourcePath"]
	}\`, nil
}

func (f *FileArchiveTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["sourcePath"].(string)
	compress, _ := args["compress"].(bool)
	fmt.Printf("[Archive Tool] Zipping path: %s\\n", path)
	return \`{"status": "success", "file": "backup.zip"}\`, nil
}

func init() {
	// Register the tool globally
	tools.Register(&FileArchiveTool{})
}`,
    },
    compile: {
      file: "compiler_run.go",
      code: `package main

import (
	"context"
	"fmt"
	"tzro/internal/compiler"
	"tzro/internal/executor"
)

func ExecuteTask(ctx context.Context) {
	// 1. Declare coarse Strategic Abstract Graph
	graph := &compiler.ExecutionGraph{
		TaskID: "t_demo_compile",
		Nodes: []compiler.GraphNode{
			{
				ID: "node_01",
				Type: "action",
				Action: "archive_files",
				Instructions: "Archive report records",
			},
		},
	}

	// 2. Compile and sort levels topologically using Kahn's Algorithm
	levels, _ := compiler.CompileAndSort(graph)
	fmt.Printf("Topological Sequence Sorted: %v\\n", levels)

	// 3. Execute concurrently through Kahn levels sequencer
	_ = executor.GlobalEngine.ExecuteGraph(ctx, graph, levels)
}`,
    },
    telemetry: {
      file: "telemetry.go",
      code: `package main

import (
	"fmt"
	"tzro/internal/stream"
)

func ListenToEvents(targetTaskID string) {
	// Subscribe to thread-safe telemetry updates on global StreamBus
	sub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
		return chunk.TaskID == targetTaskID
	})
	defer sub.Unsubscribe()

	// Consume streamed token deltas and node status updates asynchronously
	for chunk := range sub.Ch {
		fmt.Printf("[Telemetry] Node: %s | Type: %s | Content: %s\\n",
			chunk.NodeID, chunk.Type, chunk.Content)
	}
}`,
    },
    hooks: {
      file: "custom_hooks.go",
      code: `package main

import (
	"context"
	"fmt"
	"strings"
	"tzro/internal/compiler"
	"tzro/internal/executor"
)

type CustomSafetyHook struct{}

// BeforeNode runs immediately before single node executes
func (h *CustomSafetyHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
	if node.Action == "delete_all_records" {
		fmt.Printf("[Hook] Safety: skipping destructive node %s\\n", node.ID)
		return executor.ActionSkip, nil // Propagates ActionSkip downstream
	}
	return executor.ActionContinue, nil
}

// AfterNode runs after tool completion, enabling inline outputs sanitization
func (h *CustomSafetyHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (executor.HookAction, error) {
	if rawOutput != nil {
		*rawOutput = strings.ReplaceAll(*rawOutput, "SSN_SECRET_VALUE", "[REDACTED]")
	}
	return executor.ActionContinue, nil
}

func main() {
	// Register hook globally inside executing daemon
	executor.GlobalEngine.RegisterHook(&CustomSafetyHook{})
}`,
    },
  };

  function selectTab(btn) {
    dxTabBtns.forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");

    const tabKey = btn.getAttribute("data-tab");
    const snippet = DX_SNIPPETS[tabKey];
    if (snippet) {
      dxFilename.textContent = snippet.file;
      dxCodeDisplay.textContent = snippet.code;
    }
  }

  dxTabBtns.forEach((btn) => {
    btn.addEventListener("click", (e) => selectTab(e.currentTarget));
  });

  // Load first tab default snippet
  selectTab(dxTabBtns[0]);

  // Hook simulator state variables
  let hitlApprovalPromiseResolve = null;

  function appendDxConsole(text, cssClass = "") {
    const line = document.createElement("div");
    line.className = `console-line ${cssClass}`;
    line.textContent = `> ${text}`;
    dxConsole.appendChild(line);
    dxConsole.scrollTop = dxConsole.scrollHeight;
  }

  function resetVariantCSim() {
    vCSimIntervals.forEach(clearTimeout);
    vCSimIntervals = [];
    dxConsole.innerHTML =
      '<div class="console-line text-muted">Initialize the pipeline configuration to run...</div>';
    hitlPrompt.style.display = "none";
    vCRunBtn.disabled = false;
    vCRunBtn.textContent = "Execute Hooked Runtime";

    if (hitlApprovalPromiseResolve) {
      hitlApprovalPromiseResolve(false);
      hitlApprovalPromiseResolve = null;
    }
  }

  function runVariantCSim() {
    resetVariantCSim();
    vCRunBtn.disabled = true;
    vCRunBtn.textContent = "Running Hooked Engine...";

    appendDxConsole(
      "Initializing database engine layer... [Success]",
      "success",
    );
    appendDxConsole(
      "Registering execution hooks with GlobalEngine...",
      "console-line",
    );

    // Print which hooks are active based on toggles
    const piiActive = hookPII.checked;
    const guardActive = hookGuard.checked;
    const hitlActive = hookHITL.checked;

    if (piiActive)
      appendDxConsole(
        "[Hook Enabled] AfterNode output mutator registered.",
        "warning",
      );
    if (guardActive)
      appendDxConsole(
        "[Hook Enabled] BeforeNode safety blocker registered.",
        "warning",
      );
    if (hitlActive)
      appendDxConsole(
        "[Hook Enabled] BeforeLevel blocking supervisor gate registered.",
        "warning",
      );

    appendDxConsole(
      "Compiling Execution Graph (Task: t_hook_run)...",
      "console-line",
    );

    // Phase 1: Level 0 executes (Safe Nodes)
    vCSimIntervals.push(
      setTimeout(() => {
        appendDxConsole(
          "Kahn sorting complete. Running Level 0 steps...",
          "console-line",
        );
        appendDxConsole(
          "[Level 0 Node: node_01] Invoking tool 'fetch_user_details'...",
          "console-line",
        );
      }, 1200),
    );

    vCSimIntervals.push(
      setTimeout(() => {
        let rawResult =
          '{"status": "success", "username": "jp", "credentials": "SSN_SECRET_VALUE"}';
        appendDxConsole(
          `[Level 0 Node: node_01] Tool raw output: ${rawResult}`,
          "console-line",
        );

        if (piiActive) {
          appendDxConsole(
            "[Hook Triggered: AfterNode] Safety check. SSN credentials detected in raw response.",
            "warning",
          );
          rawResult =
            '{"status": "success", "username": "jp", "credentials": "[REDACTED]"}';
          appendDxConsole(`[Hook Mutated Output]: ${rawResult}`, "success");
        }
        appendDxConsole(
          "[Level 0 Node: node_01] Saved securely to database state checkpoints.",
          "success",
        );
      }, 2800),
    );

    // Phase 2: Level 1 executes
    vCSimIntervals.push(
      setTimeout(async () => {
        appendDxConsole(
          "Unblocking Level 1. Running level checks...",
          "console-line",
        );

        if (hitlActive) {
          appendDxConsole(
            "[Hook Triggered: BeforeLevel] Human-In-The-Loop hook blocks execution.",
            "warning",
          );
          appendDxConsole(
            "Sent Sentinel Error: ErrTaskPaused. Halting thread and yielding execution.",
            "error",
          );

          // Show approval panel and pause
          vCRunBtn.textContent = "Awaiting Approval...";
          hitlPrompt.style.display = "block";

          // Create a promise to wait for button click
          const approved = await new Promise((resolve) => {
            hitlApprovalPromiseResolve = resolve;
          });

          hitlPrompt.style.display = "none";
          if (approved) {
            appendDxConsole(
              "Durable task resumed successfully from checkpoint. Level 1 unblocked by human supervisor.",
              "success",
            );
            vCRunBtn.textContent = "Resuming Task...";
            continueLevel1Execution(guardActive);
          } else {
            appendDxConsole(
              "Task aborted by human supervisor. State reset.",
              "error",
            );
            vCRunBtn.disabled = false;
            vCRunBtn.textContent = "Run Hooked Runtime";
          }
        } else {
          continueLevel1Execution(guardActive);
        }
      }, 4500),
    );
  }

  function continueLevel1Execution(guardActive) {
    appendDxConsole(
      "[Level 1 Node: node_02] Intercepting action 'delete_all_records'...",
      "console-line",
    );

    vCSimIntervals.push(
      setTimeout(() => {
        if (guardActive) {
          appendDxConsole(
            "[Hook Triggered: BeforeNode] Safety violation! Destructive tool call blocked.",
            "error",
          );
          appendDxConsole(
            "[Hook Output: ActionSkip] Automatically skipping tool run, propagating skip state downstream.",
            "warning",
          );
          appendDxConsole(
            "[Level 1 Node: node_02] Marked as [skipped] in SQLite checkpoints.",
            "success",
          );
        } else {
          appendDxConsole(
            "[Level 1 Node: node_02] Invoking tool 'delete_all_records' directly without guardrails!",
            "error",
          );
          appendDxConsole(
            "[Level 1 Node: node_02] Database records deleted completely.",
            "success",
          );
        }

        finalizeLevel1Run();
      }, 1500),
    );
  }

  function finalizeLevel1Run() {
    vCSimIntervals.push(
      setTimeout(() => {
        appendDxConsole("Level 1 finished processing.", "warning");
        appendDxConsole(
          "Kahn executor complete. Running synthesis summary...",
          "console-line",
        );
      }, 1200),
    );

    vCSimIntervals.push(
      setTimeout(() => {
        appendDxConsole(
          "Task completed. All execution hooks disposed securely.",
          "success",
        );
        vCRunBtn.disabled = false;
        vCRunBtn.textContent = "Re-execute Hooked Run";
      }, 2500),
    );
  }

  if (vCRunBtn) vCRunBtn.addEventListener("click", runVariantCSim);

  if (hitlApproveBtn) {
    hitlApproveBtn.addEventListener("click", () => {
      if (hitlApprovalPromiseResolve) {
        hitlApprovalPromiseResolve(true);
        hitlApprovalPromiseResolve = null;
      }
    });
  }

  if (hitlAbortBtn) {
    hitlAbortBtn.addEventListener("click", () => {
      if (hitlApprovalPromiseResolve) {
        hitlApprovalPromiseResolve(false);
        hitlApprovalPromiseResolve = null;
      }
    });
  }

  // =========================================================================
  // 5. MCP CLIENT CONFIGURATION TABS
  // =========================================================================
  const mcpTabBtns = document.querySelectorAll(".mcp-tab-btn");
  const mcpConfigDisplay = document.getElementById("mcp-config-display");

  const MCP_CONFIGS = {
    claude: `{
  "mcpServers": {
    "tzro": {
      "command": "/absolute/path/to/tzro/bin/tzro-mcp",
      "args": [],
      "env": {
        "PORT": "8080"
      }
    }
  }
}`,
    cursor: `Name: tzro
Type: command
Command: /absolute/path/to/tzro/bin/tzro-mcp`,
    agy: `{
  "mcpServers": {
    "tzro": {
      "command": "/absolute/path/to/tzro/bin/tzro-mcp",
      "args": [],
      "env": {
        "TZRO_DIR": "/absolute/path/to/tzro",
        "ANTIGRAVITY_AGENT": "$ANTIGRAVITY_AGENT",
        "ANTIGRAVITY_TRAJECTORY_ID": "$ANTIGRAVITY_TRAJECTORY_ID",
        "ANTIGRAVITY_LS_ADDRESS": "$ANTIGRAVITY_LS_ADDRESS",
        "ANTIGRAVITY_CSRF_TOKEN": "$ANTIGRAVITY_CSRF_TOKEN"
      }
    }
  }
}`,
  };

  if (mcpTabBtns.length > 0 && mcpConfigDisplay) {
    mcpTabBtns.forEach((btn) => {
      btn.addEventListener("click", () => {
        mcpTabBtns.forEach((b) => b.classList.remove("active"));
        btn.classList.add("active");
        const client = btn.getAttribute("data-client");
        mcpConfigDisplay.textContent = MCP_CONFIGS[client];
      });
    });
  }

  // Perform initial resets once all elements are declared and listeners are bound
  resetVariantASim();
  resetVariantBSim();
  resetVariantCSim();

  // All simulators initialized successfully above on page load.
});
