# TZRO Landing Website - Design Notes

This directory contains the final, high-fidelity, single-page interactive product website for **tzro**. It consolidates all three visual playgrounds into a unified user experience designed under the **Verdant-Synth (Bio-Synthetic)** aesthetic guidelines.

## Bio-Synthetic Aesthetic Integration (_Verdant-Synth_)

Following the project's [DESIGN.md](file:///Users/jp/Desktop/Repos/tzro/DESIGN.md), the styling is tailored to feel organic, grounded, and clean, subverting traditional cyberpunk clichés:

- **Colors:** Default backgrounds use a terrestrial Deep Moss canvas (`#0A110D`) with soft elevated Forest Shadow surfaces (`#131F18`) and division rules defined in Foliage Line (`#223329`). Primary text utilizes Off-Alabaster (`#F1F4F2`), and inactive meta captions use Muted Sage (`#5C6B62`).
- **AI Spark Accent:** The Cyber Lime accent (`#B6FF00`) is used deliberately like a rare element, reserved strictly for intelligent actions, status dots, and active processing triggers. Buttons styled in Cyber Lime use high-contrast dark Deep Moss text for extreme readability.
- **Organic Canvas Backdrops:** Substituted matrix retro grids with soft bioluminescent moss blurs (top-left and bottom-right glowing radial gradients blending into Deep Moss).
- **Typography Hierarchy:** Core headers and page copy utilize clean geometric sans-serif typefaces (`Outfit` & `Inter`). Pairings are completed using monospace `JetBrains Mono` strictly inside raw terminal boxes, logging console areas, and SDK snippets to represent raw machine stream tokens.
- **Horizon Pulse Processing:** A custom linear 2px Cyber Lime processor line sits directly above the simulator card layout and pulses dynamically (`@keyframes horizonPulse`) while topological goroutines compile.

---

## Page Layout & Demonstration Playgrounds

The unified page vertically stacks the core sections to give developers an intuitive, high-density overview of the systems core:

1. **Strategic Hero Section:** Features the bio-synthetic branding banner.
2. **Strategy-vs-Tactics Split Columns:** Highlights tzro's core architectural separation. Introduces **The Strategist** (Gemini Cloud planner building coarse abstract graphs) and **The Tactician** (Lightweight GGUF llama-server pinning core performance cores to execute GBNF-constrained tools).
3. **Kahn Parallel Sorter Simulator:** The visual Kahn Level executor. Watch levels execute goroutines concurrently (Level 0 $\to$ Level 1) and inspect compiled steps inside a mock terminal daemon console.
4. **5-Layer Context Compactor Playground:** A live playground to test data compression. Click homogeneously-sized CRM payloads, exceptions logs, or HTML scrapings, and watch the pipeline compact data to save up to **85% KV token slots**. Shows the SQLite cache threshold envelope triggering when compacted content exceeds 12KB.
5. **Developer SDK & Synchronous Hooks Simulator:** Integrates a typed Go SDK browser mapping code tabs (Config, Tools, Compiler, Stream Telemetry, Middleware Hooks). Includes a live Synchronous Hook simulator: toggle PII Sanitization, Destructive Safety filters, or blocking Human approval gates, and see how the console reacts to Pause sentinel errors (`ErrTaskPaused`) and skips.
6. **Zero-Setup Quickstart & Footer:** Monospace blocks outlining command integrations.

---

## How to Run Locally

Start the static web server directly using Go:

```bash
go run website/main.go
```

_Note: If your active working directory is already inside `website/`, simply run `go run main.go`._

Open your web browser to **`http://localhost:8080`** to interact with the playfields.

---

## Hosting on GitHub Pages

The website is fully client-side and optimized for static hosting. A GitHub Actions workflow (`.github/workflows/deploy-pages.yml`) is provided to automate deployment to GitHub Pages.

To enable automated deployment:

1. Push the repository to GitHub.
2. In the GitHub Repository settings, navigate to **Pages** (under the "Code and automation" section).
3. Under **Build and deployment** -> **Source**, select **GitHub Actions** from the dropdown menu.
4. The workflow will automatically deploy the site on pushes to `main` or `release/0.1` branches.
