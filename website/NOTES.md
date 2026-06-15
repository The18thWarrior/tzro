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

## Page Structure

The page is organized around an **Interactive OS Architecture Diagram** as the navigation spine:

1. **Hero Section**: Badge: `AGENTIC OPERATING SYSTEM`. Title: `tzro — The Jumpdrive for AI Agents`. Subtitle: "Your agent has a brain. What it's missing is a body..."
2. **"Why Not Just Cloud?" Comparison**: 5-row glass comparison table (Cost, Privacy, Latency, Self-Improvement, Crash Recovery).
3. **Interactive OS Architecture Diagram**: 4×4 block grid mapping classical OS primitives to tzro equivalents. Click any block to scroll to its section. Tooltip on hover. Scroll-spy highlights the active block.
4. **Get Started — Two Onramps** (tabbed):
   - **Plug In (MCP)**: MCP config, tool list, client tabs (Claude/Cursor/Antigravity), delegation/wait protocol.
   - **Build On (Framework)**: Go SDK code browser (5 guides), synchronous hook playground with PII/Safety/HITL toggles.
5. **Playgrounds**:
   - **Process Scheduler**: Extended Kahn simulator with **Neural Edge Traversal** — Edge Thought evaluation, confidence below threshold triggers dynamic node spawning with animation.
   - **Virtual Memory**: 5-layer compaction pipeline with SQLite cache threshold.
6. **Under the Hood**: 6 glass cards for non-demoed moats (GBNF + Semantic Validator, KV Cache Preemption, Hybrid Vector Search, Self-Improving Inference, Background Intelligence, Generative Dashboard).
7. **Quickstart** (tabbed): MCP path (default) and Framework path.
8. **Footer**: GitHub, Architecture, Get Started links.

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
