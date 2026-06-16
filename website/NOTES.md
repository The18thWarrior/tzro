# TZRO Landing Website - Design Notes

This directory contains the high-fidelity, single-page interactive product website for **tzro**, positioning it as an **Agentic Operating System** with a **"Jumpdrive"** metaphor. The design was updated from the previous "Local Offload Engine / Complementary Companion" framing per ADR-0032.

## Positioning: AgenticOS Jumpdrive

> **tzro is The Jumpdrive for AI Agents** — a portable, self-contained runtime that any agent can plug into via MCP or Go framework embedding. One config block = full OS: kernel, scheduler, memory, self-improving inference, and background intelligence.

The "Jumpdrive" metaphor conveys three value propositions simultaneously:
- **Instant activation**: One MCP config block or one Go import, and the full OS activates.
- **Completeness**: Everything an agent needs in one package.
- **Portability**: Copy `tzro.db` + binary to another machine = perfect clone.

## Bio-Synthetic Aesthetic (_Verdant-Synth_)

The visual design system is unchanged from the original. See the project's DESIGN.md for full details:

- **Colors:** Deep Moss canvas (`#0A110D`), Forest Shadow surfaces (`#131F18`), Foliage Line dividers (`#223329`), Off-Alabaster text (`#F1F4F2`), Muted Sage meta (`#5C6B62`).
- **AI Spark Accent:** Cyber Lime (`#B6FF00`) reserved for intelligent actions, status indicators, and active processing triggers.
- **Typography:** Outfit & Inter for copy. JetBrains Mono inside terminal/code areas only.

---

## Dual-Audience Mode Toggle

The site supports two audience modes — **User** and **Developer** — toggled via a pill control in the header. The toggle is visible on both desktop and mobile.

### Mechanism

- `<body data-mode="user|developer">` drives all visibility
- Elements with `data-audience="developer"` are hidden in User mode (and vice versa)
- CSS rules: `body[data-mode="user"] [data-audience="developer"] { display: none !important; }`
- Elements without `data-audience` are always visible (shared content)

### State Persistence

1. URL parameter `?view=user|developer` (highest priority, for direct linking)
2. `localStorage` key `tzro-site-mode` (persists across sessions)
3. Default: `user` (most first-time visitors aren't Go developers)

### What Changes Per Mode

| Aspect | User Mode | Developer Mode |
|--------|-----------|----------------|
| Hero badge | WORKS WITH CLAUDE, CURSOR & ANY MCP AGENT | AGENTIC OPERATING SYSTEM |
| Hero title | "Stop babysitting your AI agents" | "tzro — The Jumpdrive for AI Agents" |
| Hero CTAs | See Use Cases / Install in 60 Seconds | Get Started / Explore the OS |
| Nav links | Use Cases, How It Works, Install | Architecture, Get Started, Playgrounds, Under the Hood, Quickstart |
| Comparison copy | Plain-language descriptions | Technical descriptions |
| Use Cases section | Visible (3 cards) | Hidden |
| How It Works pipeline | Visible (5 steps) | Hidden |
| Architecture diagram | Hidden | Visible |
| Get Started | Simplified: curl install + paste config + start | Full: build binary + config + verify + tools panel + delegation policy |
| Framework tab | Hidden | Visible |
| Playgrounds | Hidden | Visible (Kahn simulator + compaction) |
| Under the Hood | Hidden | Visible |

---

## Page Structure

### Developer Mode Page Structure

1. **Hero Section**: Badge: `AGENTIC OPERATING SYSTEM`. Title: `tzro — The Jumpdrive for AI Agents`. Subtitle: "Your agent has a brain. What it's missing is a body..."
2. **"Why Not Just Cloud?" Comparison**: 5-row glass comparison table with technical copy (Cost, Privacy, Latency, Self-Improvement, Crash Recovery).
3. **Interactive OS Architecture Diagram**: 4×4 block grid mapping classical OS primitives to tzro equivalents. Click any block to scroll to its section. Tooltip on hover. Scroll-spy highlights the active block.
4. **Get Started — Two Onramps** (tabbed):
   - **Plug In (MCP)**: Build binary, MCP config, client tabs (Claude/Cursor/Antigravity), verify JSON-RPC, tools list, delegation/wait protocol.
   - **Build On (Framework)**: Go SDK code browser (5 guides), synchronous hook playground with PII/Safety/HITL toggles.
5. **Playgrounds**:
   - **Process Scheduler**: Extended Kahn simulator with **Neural Edge Traversal** — Edge Thought evaluation, confidence below threshold triggers dynamic node spawning with animation.
   - **Virtual Memory**: 5-layer compaction pipeline with SQLite cache threshold.
6. **Under the Hood**: 6 glass cards for non-demoed moats (GBNF + Semantic Validator, KV Cache Preemption, Hybrid Vector Search, Self-Improving Inference, Background Intelligence, Generative Dashboard).
7. **Quickstart** (tabbed): MCP path (default) and Framework path.
8. **Footer**: GitHub, Architecture, Get Started links.

### User Mode Page Structure

1. **Hero Section**: Badge: `WORKS WITH CLAUDE, CURSOR & ANY MCP AGENT`. Title: `Stop babysitting your AI agents`. Plain-language subtitle.
2. **"What Can I Do With tzro?"**: 3 use-case cards (Deep Research, Local Document Processing, Queue & Resume Overnight Work).
3. **"Why Not Just Cloud?" Comparison**: Same 5-row table with plain-language copy.
4. **"How It Works" Pipeline**: 5-step visual flow (Goal → Cloud Plans → Local Executes → Progress Saves → Results Delivered).
5. **Install**: Simplified 3-step flow (curl install → paste config → start using it). No Framework tab.
6. **Quickstart**: MCP path only.
7. **Footer**: GitHub, Use Cases, Install links.

---

## How to Run Locally

Start the static web server directly using Go:

```bash
go run website/main.go
```

Open your web browser to **`http://localhost:8080`** to interact with the playfields.

---

## Hosting on GitHub Pages

The website is fully client-side and optimized for static hosting. A GitHub Actions workflow (`.github/workflows/deploy-pages.yml`) is provided to automate deployment to GitHub Pages.

To enable automated deployment:

1. Push the repository to GitHub.
2. In the GitHub Repository settings, navigate to **Pages** (under the "Code and automation" section).
3. Under **Build and deployment** -> **Source**, select **GitHub Actions** from the dropdown menu.
4. The workflow will automatically deploy the site on pushes to `main` or `release/0.1` branches.
