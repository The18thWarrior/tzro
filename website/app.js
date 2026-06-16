/* =========================================================================
   TZRO AGENTIC OS WEBSITE: CORE CLIENT-SIDE ENGINE
   ========================================================================= */

document.addEventListener("DOMContentLoaded", () => {
  // =========================================================================
  // DUAL-AUDIENCE MODE TOGGLE SYSTEM
  // =========================================================================
  const modeToggleDesktop = document.getElementById("mode-toggle-desktop");
  const modeToggleMobile = document.getElementById("mode-toggle-mobile");

  function setMode(mode) {
    document.body.setAttribute("data-mode", mode);
    localStorage.setItem("tzro-site-mode", mode);

    // Update URL param
    const url = new URL(location);
    url.searchParams.set("view", mode);
    history.replaceState(null, "", url);

    // Sync all toggle button states
    document.querySelectorAll(".mode-toggle-btn").forEach((btn) => {
      btn.classList.toggle("active", btn.getAttribute("data-mode") === mode);
    });
  }

  function initMode() {
    const urlMode = new URLSearchParams(location.search).get("view");
    const storedMode = localStorage.getItem("tzro-site-mode");
    const mode = urlMode || storedMode || "user";
    setMode(mode);
  }

  // Attach click handlers to both desktop and mobile toggles
  [modeToggleDesktop, modeToggleMobile].forEach((toggle) => {
    if (!toggle) return;
    toggle.querySelectorAll(".mode-toggle-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        setMode(btn.getAttribute("data-mode"));
      });
    });
  });

  initMode();
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

    mobileNavLinks.forEach((link) => {
      link.addEventListener("click", () => {
        mobileNavDrawer.classList.remove("open");
        mobileMenuToggle.classList.remove("active");
        mobileMenuToggle.setAttribute("aria-expanded", "false");
      });
    });

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
  // INITIALIZE PLAYGROUND TIMERS
  // =========================================================================
  let vASimIntervals = [];
  let vBSimIntervals = [];
  let vCSimIntervals = [];

  // =========================================================================
  // OS ARCHITECTURE DIAGRAM: INTERACTIVITY
  // =========================================================================
  const osBlocks = document.querySelectorAll(".os-block");
  const osTooltip = document.getElementById("os-tooltip");
  const osTooltipText = document.getElementById("os-tooltip-text");

  osBlocks.forEach((block) => {
    // Click → smooth scroll to target section
    block.addEventListener("click", () => {
      const target = block.getAttribute("data-target");
      if (target) {
        const el = document.querySelector(target);
        if (el) {
          el.scrollIntoView({ behavior: "smooth", block: "start" });

          // Briefly highlight the clicked block
          osBlocks.forEach((b) => b.classList.remove("active"));
          block.classList.add("active");
          setTimeout(() => block.classList.remove("active"), 2000);
        }
      }
    });

    // Hover → show tooltip with label
    block.addEventListener("mouseenter", (e) => {
      const label = block.getAttribute("data-label");
      if (label && osTooltip && osTooltipText) {
        osTooltipText.textContent = label;
        osTooltip.classList.add("visible");
      }
    });

    block.addEventListener("mousemove", (e) => {
      if (osTooltip) {
        osTooltip.style.left = e.clientX + 12 + "px";
        osTooltip.style.top = e.clientY + 12 + "px";
      }
    });

    block.addEventListener("mouseleave", () => {
      if (osTooltip) {
        osTooltip.classList.remove("visible");
      }
    });
  });

  // Scroll-spy: highlight active OS block based on scroll position
  const sectionTargetMap = new Map();
  osBlocks.forEach((block) => {
    const target = block.getAttribute("data-target");
    if (target) {
      sectionTargetMap.set(target, block);
    }
  });

  let scrollSpyTicking = false;
  window.addEventListener("scroll", () => {
    if (!scrollSpyTicking) {
      requestAnimationFrame(() => {
        const scrollY = window.scrollY + 200;
        let activeTarget = null;

        sectionTargetMap.forEach((block, selector) => {
          const el = document.querySelector(selector);
          if (el && el.offsetTop <= scrollY) {
            activeTarget = selector;
          }
        });

        osBlocks.forEach((b) => b.classList.remove("active"));
        if (activeTarget && sectionTargetMap.has(activeTarget)) {
          sectionTargetMap.get(activeTarget).classList.add("active");
        }
        scrollSpyTicking = false;
      });
      scrollSpyTicking = true;
    }
  });

  // =========================================================================
  // TWO-ONRAMP TAB SWITCHING
  // =========================================================================
  const onrampTabBtns = document.querySelectorAll(".onramp-tab-btn");
  const onrampMcp = document.getElementById("onramp-mcp");
  const onrampFramework = document.getElementById("onramp-framework");

  onrampTabBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      onrampTabBtns.forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      const tab = btn.getAttribute("data-onramp");

      if (tab === "mcp") {
        onrampMcp.classList.add("active");
        onrampFramework.classList.remove("active");
      } else {
        onrampMcp.classList.remove("active");
        onrampFramework.classList.add("active");
      }
    });
  });

  // =========================================================================
  // QUICKSTART TAB SWITCHING
  // =========================================================================
  const qsTabBtns = document.querySelectorAll(".quickstart-tab-btn");
  const qsMcp = document.getElementById("qs-mcp");
  const qsFramework = document.getElementById("qs-framework");

  qsTabBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      qsTabBtns.forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      const tab = btn.getAttribute("data-qs");

      if (tab === "mcp") {
        qsMcp.classList.add("active");
        qsFramework.classList.remove("active");
      } else {
        qsMcp.classList.remove("active");
        qsFramework.classList.add("active");
      }
    });
  });

  // =========================================================================
  // PROCESS SCHEDULER: KAHN SIMULATOR WITH DYNAMIC NODE SPAWNING
  // =========================================================================
  const vAStartBtn = document.getElementById("vA-start-btn");
  const vAResetBtn = document.getElementById("vA-reset-btn");
  const vAConsole = document.getElementById("vA-console");
  const vAHorizonPulse = document.getElementById("vA-horizon-pulse");

  const nodePlanner = document.getElementById("node-planner");
  const node01 = document.getElementById("node-01");
  const node02 = document.getElementById("node-02");
  const node03 = document.getElementById("node-03");
  const nodeEdgeThought = document.getElementById("node-edge-thought");
  const nodeSpawned = document.getElementById("node-spawned");
  const edgeConfidence = document.getElementById("edge-confidence");

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

    // Reset all nodes
    [nodePlanner, node01, node02, node03].forEach((n) => {
      if (n) {
        n.className = "sim-node pending";
      }
    });
    if (nodePlanner) nodePlanner.className = "sim-node planner";

    // Reset edge thought and spawned nodes
    if (nodeEdgeThought) {
      nodeEdgeThought.className = "sim-node edge-thought-node hidden";
    }
    if (nodeSpawned) {
      nodeSpawned.className = "sim-node spawned-node hidden";
    }
    if (edgeConfidence) {
      edgeConfidence.textContent = "—";
      edgeConfidence.className = "";
    }

    vAConsole.innerHTML =
      '<div class="log-line text-muted">Awaiting topological compilation triggers...</div>';
    vAStartBtn.disabled = false;
    vAStartBtn.textContent = "Compile & Run Task";
  }

  function runVariantASim() {
    resetVariantASim();
    vAStartBtn.disabled = true;
    vAStartBtn.textContent = "Running...";

    if (vAHorizonPulse) {
      vAHorizonPulse.classList.add("pulsing");
      vAHorizonPulse.style.transform = "scaleX(1)";
      vAHorizonPulse.style.opacity = "1";
    }

    appendConsoleLine(
      "Compiling abstract graph with Kahn Compiler...",
      "cyan",
    );

    // Phase 1: Cloud Strategist plans (called ONCE)
    vASimIntervals.push(
      setTimeout(() => {
        nodePlanner.className = "sim-node running";
        appendConsoleLine(
          "Cloud Strategist called ONCE. Compiling abstract execution blueprint...",
          "log-line",
        );
      }, 1000),
    );

    vASimIntervals.push(
      setTimeout(() => {
        nodePlanner.className = "sim-node completed";
        appendConsoleLine(
          "Strategic compilation complete. Event-driven ready queue initialized.",
          "success",
        );
        appendConsoleLine(
          "Level mapping: Level 0 [node_01, node_02] → Edge Thought → Level 1 [node_03]",
          "cyan",
        );
      }, 2500),
    );

    // Phase 2: Level 0 executes in parallel
    vASimIntervals.push(
      setTimeout(() => {
        node01.className = "sim-node running";
        node02.className = "sim-node running";
        appendConsoleLine(
          "Ready queue fires Level 0. Dispatching parallel goroutines.",
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
          "[Level 0: Goroutine 2] node_02 completed. Output: user profiles compiled (4.8KB).",
          "success",
        );
      }, 7200),
    );

    // Phase 3: EDGE THOUGHT — Neural Edge Traversal
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "All Level 0 edges satisfied. Generating Edge Thought on outgoing edge...",
          "cyan",
        );
        // Show edge thought node
        nodeEdgeThought.className =
          "sim-node edge-thought-node visible evaluating";
        edgeConfidence.textContent = "0.42";
        edgeConfidence.className = "confidence-low";
        appendConsoleLine(
          "Edge Thought evaluated: goalConfidence=0.42, threshold=0.70",
          "log-line",
        );
      }, 8500),
    );

    // Phase 4: Confidence below threshold → DYNAMIC NODE SPAWN
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "⚡ Confidence (0.42) < Activation Threshold (0.70) — SPAWNING new node!",
          "warning",
        );
        // Show spawned node with animation
        nodeSpawned.className = "sim-node spawned-node visible spawning";
        appendConsoleLine(
          "[Dynamic Spawn] node_03a created: enrich_user_data. Mutation budget: 14 remaining.",
          "success",
        );
      }, 10000),
    );

    // Phase 5: Spawned node executes
    vASimIntervals.push(
      setTimeout(() => {
        nodeSpawned.className = "sim-node running visible";
        appendConsoleLine(
          "Executing node_03a (enrich_user_data) via MCP Host...",
          "log-line",
        );
      }, 11200),
    );

    vASimIntervals.push(
      setTimeout(() => {
        nodeSpawned.className = "sim-node completed visible";
        appendConsoleLine(
          "[Spawned Node] node_03a completed. Enriched user profiles with org data.",
          "success",
        );

        // Update edge thought confidence
        edgeConfidence.textContent = "0.91";
        edgeConfidence.className = "confidence-high";
        nodeEdgeThought.className =
          "sim-node edge-thought-node visible completed";
        appendConsoleLine(
          "Edge Thought re-evaluated: goalConfidence=0.91 ≥ threshold — CONTINUE",
          "success",
        );
      }, 12800),
    );

    // Phase 6: Level 1 executes (original node_03)
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "Edge satisfied with sufficient confidence. Unblocking Level 1.",
          "cyan",
        );
        node03.className = "sim-node running";
        appendConsoleLine(
          "Executing node_03 (send_team_alert) using Stdio MCP gateway...",
          "log-line",
        );
      }, 14000),
    );

    vASimIntervals.push(
      setTimeout(() => {
        node03.className = "sim-node completed";
        appendConsoleLine(
          "[Level 1] node_03 completed. Notification pushed via Slack MCP server.",
          "success",
        );
      }, 15500),
    );

    // Terminal synthesis
    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "Terminal synthesis node compiling summary...",
          "cyan",
        );
      }, 16500),
    );

    vASimIntervals.push(
      setTimeout(() => {
        appendConsoleLine(
          "TASK COMPLETE. Status: success. Cloud calls: 1. Local dispatches: 4 (1 dynamically spawned). Checkpoints: 6.",
          "success",
        );
        if (vAHorizonPulse) {
          vAHorizonPulse.classList.remove("pulsing");
          vAHorizonPulse.style.transform = "scaleX(0)";
          vAHorizonPulse.style.opacity = "0";
        }
        vAStartBtn.disabled = false;
        vAStartBtn.textContent = "Re-run Task";
      }, 17800),
    );
  }

  if (vAStartBtn) vAStartBtn.addEventListener("click", runVariantASim);
  if (vAResetBtn) vAResetBtn.addEventListener("click", resetVariantASim);

  // =========================================================================
  // VIRTUAL MEMORY: 5-LAYER CONTEXT COMPACTION PLAYGROUND
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
      raw: `<!DOCTYPE html>\n<html>\n<head>\n  <title>TZRO Github Repository Documentation Page</title>\n</head>\n<body>\n  <div id="wrapper">\n    <header class="repo-header">\n      <h1 class="repo-title"><a href="https://github.com/The18thWarrior/tzro">The18thWarrior/tzro</a></h1>\n      <span class="star-count">Stars: 1420</span>\n    </header>\n    \n    <main class="content-body">\n      <div class="readme-preview">\n        <h2>Overview</h2>\n        <p class="summary-p">tzro is a durable, local-first agentic operating system — a portable runtime that carries everything an AI agent needs to be productive.</p>\n        <p class="detail-p">By implementing strategy-vs-tactics planner routing and topological sorted goroutine execution grids, tzro allows local lightweight GGUF models to call tools without argument hallucination.</p>\n      </div>\n      \n      <aside class="sidebar-info">\n        <h3>Build Status</h3>\n        <div class="status-indicator">passing</div>\n        <h3>License</h3>\n        <span>Apache-2.0</span>\n      </aside>\n    </main>\n  </div>\n</body>\n</html>`,
      compacted: `URL: https://github.com/The18thWarrior/tzro\nStars: 1420 | Status: passing | License: Apache-2.0\nTitle: TZRO Github Repository Documentation Page\nContent:\n- tzro: durable, local-first agentic operating system — portable runtime carrying everything an AI agent needs.\n- System uses strategy-vs-tactics planner and topological sorted goroutines to prevent local tool argument hallucinations.`,
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

    const preset = PAYLOAD_PRESETS[activePreset];
    rawSizeLabel.textContent = preset.size;
    compactedSizeLabel.textContent = "0.0";
    rawPayloadDisplay.value = preset.raw;

    if (rawSizeTab) rawSizeTab.textContent = preset.size;
    if (compactedSizeTab) compactedSizeTab.textContent = "0.0";

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

    // Layer 0-4 pipeline animation
    for (let i = 0; i < 5; i++) {
      vBSimIntervals.push(
        setTimeout(() => {
          pipelineSteps[i].className = "pipeline-step-item active";
          pipelineSteps[i].querySelector(".status").textContent = "Running";
        }, stepDelays[i]),
      );

      vBSimIntervals.push(
        setTimeout(() => {
          pipelineSteps[i].className = "pipeline-step-item completed";
          pipelineSteps[i].querySelector(".status").textContent = "Done";
        }, stepDelays[i + 1]),
      );
    }

    // Complete results
    vBSimIntervals.push(
      setTimeout(() => {
        compactedSizeLabel.textContent = preset.finalSize;
        if (compactedSizeTab) compactedSizeTab.textContent = preset.finalSize;

        // Auto switch to compacted tab on mobile
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

        const exceedsThreshold = parseFloat(preset.size) > 30;

        if (exceedsThreshold) {
          compactedPayloadDisplay.style.display = "none";
          diskCacheBanner.style.display = "flex";
        } else {
          compactedPayloadDisplay.style.display = "block";
          compactedPayloadDisplay.textContent = preset.compacted;
          diskCacheBanner.style.display = "none";
        }

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
  // FRAMEWORK ONRAMP: GO DX & INTERACTIVE MIDDLEWARE HOOKS
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

  const DX_SNIPPETS = {
    config: {
      file: "config.go",
      code: `package main

import (
\t"fmt"
\t"tzro/internal/config"
)

func main() {
\t// 1. Fetch engine configuration settings
\tcfg := config.Get()
\tfmt.Printf("Engine Mode: %s\\n", cfg.ModelMode) // cooperative, local, cloud

\t// 2. Resolve environment-delegated credentials recursively
\t// E.g., "$OPENAI_API_KEY" -> fetches active OS environment values
\tapiKey := config.GetCloudAPIKey()
\tfmt.Printf("Secret Decrypted: length = %d\\n", len(apiKey))
}`,
    },
    tools: {
      file: "tools_registry.go",
      code: `package main

import (
\t"context"
\t"encoding/json"
\t"fmt"
\t"tzro/internal/tools"
)

type FileArchiveTool struct{}

func (f *FileArchiveTool) Name() string {
\treturn "archive_files"
}

func (f *FileArchiveTool) GetSchema() (string, error) {
\treturn \`{
\t\t"type": "object",
\t\t"properties": {
\t\t\t"sourcePath": {"type": "string", "description": "Target folder"},
\t\t\t"compress": {"type": "boolean"}
\t\t},
\t\t"required": ["sourcePath"]
\t}\`, nil
}

func (f *FileArchiveTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
\tpath, _ := args["sourcePath"].(string)
\tcompress, _ := args["compress"].(bool)
\tfmt.Printf("[Archive Tool] Zipping path: %s\\n", path)
\treturn \`{"status": "success", "file": "backup.zip"}\`, nil
}

func init() {
\t// Register the tool globally
\ttools.Register(&FileArchiveTool{})
}`,
    },
    compile: {
      file: "compiler_run.go",
      code: `package main

import (
\t"context"
\t"fmt"
\t"tzro/internal/compiler"
\t"tzro/internal/executor"
)

func ExecuteTask(ctx context.Context) {
\t// 1. Declare coarse Strategic Abstract Graph
\tgraph := &compiler.ExecutionGraph{
\t\tTaskID: "t_demo_compile",
\t\tNodes: []compiler.GraphNode{
\t\t\t{
\t\t\t\tID: "node_01",
\t\t\t\tType: "action",
\t\t\t\tAction: "archive_files",
\t\t\t\tInstructions: "Archive report records",
\t\t\t},
\t\t},
\t}

\t// 2. Compile and sort levels topologically using Kahn's Algorithm
\tlevels, _ := compiler.CompileAndSort(graph)
\tfmt.Printf("Topological Sequence Sorted: %v\\n", levels)

\t// 3. Execute concurrently through event-driven ready queue
\t_ = executor.GlobalEngine.ExecuteGraph(ctx, graph, levels)
}`,
    },
    telemetry: {
      file: "telemetry.go",
      code: `package main

import (
\t"fmt"
\t"tzro/internal/stream"
)

func ListenToEvents(targetTaskID string) {
\t// Subscribe to thread-safe telemetry updates on global StreamBus
\tsub := stream.GlobalBus.Subscribe(func(chunk stream.StreamChunk) bool {
\t\treturn chunk.TaskID == targetTaskID
\t})
\tdefer sub.Unsubscribe()

\t// Consume streamed token deltas and node status updates asynchronously
\tfor chunk := range sub.Ch {
\t\tfmt.Printf("[Telemetry] Node: %s | Type: %s | Content: %s\\n",
\t\t\tchunk.NodeID, chunk.Type, chunk.Content)
\t}
}`,
    },
    hooks: {
      file: "custom_hooks.go",
      code: `package main

import (
\t"context"
\t"fmt"
\t"strings"
\t"tzro/internal/compiler"
\t"tzro/internal/executor"
)

type CustomSafetyHook struct{}

// BeforeNode runs immediately before single node executes
func (h *CustomSafetyHook) BeforeNode(ctx context.Context, taskID string, node *compiler.GraphNode) (executor.HookAction, error) {
\tif node.Action == "delete_all_records" {
\t\tfmt.Printf("[Hook] Safety: skipping destructive node %s\\n", node.ID)
\t\treturn executor.ActionSkip, nil // Propagates ActionSkip downstream
\t}
\treturn executor.ActionContinue, nil
}

// AfterNode runs after tool completion, enabling inline outputs sanitization
func (h *CustomSafetyHook) AfterNode(ctx context.Context, taskID string, node *compiler.GraphNode, rawOutput *string) (executor.HookAction, error) {
\tif rawOutput != nil {
\t\t*rawOutput = strings.ReplaceAll(*rawOutput, "SSN_SECRET_VALUE", "[REDACTED]")
\t}
\treturn executor.ActionContinue, nil
}

func main() {
\t// Register hook globally inside executing daemon
\texecutor.GlobalEngine.RegisterHook(&CustomSafetyHook{})
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
  if (dxTabBtns.length > 0) selectTab(dxTabBtns[0]);

  // Hook simulator
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

    // Phase 1: Level 0
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

    // Phase 2: Level 1
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

          vCRunBtn.textContent = "Awaiting Approval...";
          hitlPrompt.style.display = "block";

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
  // MCP CLIENT CONFIGURATION TABS
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

  // =========================================================================
  // INITIAL RESET ON PAGE LOAD
  // =========================================================================
  resetVariantASim();
  resetVariantBSim();
  resetVariantCSim();
});
