---
name: trend-architect
description: Explore and ideate on high-leverage architectural expansions and product features based on modern market trends, competitive analysis, and local-first architecture. Use when designing new capabilities, researching competitor features, proposing market-driven expansions, or aligning system boundaries with new industry patterns.
---

# Trend Architect

Ideate on new functionalities by combining market trend research with high-level system mapping and deep module design.

## Quick Start

1. **Market Pulse**: Use the `browser` subagent to search for recent articles, API changes, or competitor updates in the sector.
2. **System Map**: Zoom out to map the existing system modules and caller relationships defined in `CONTEXT.md` and `LANGUAGE.md`.
3. **Deep Expansion**: Draft a high-leverage, highly localized architectural expansion plan for the chosen capabilities.
4. **HTML Visuals**: Generate a gorgeous interactive HTML report with before/after Mermaid diagrams and open it for the user.

## Workflows

### 1. Market Research & Browser Discovery
- Invoke the `browser` subagent or perform a `search_web` query targeting the relevant sector (e.g., "local-first sync protocols 2026", "edge AI task orchestration").
- Identify 2-3 key patterns, architectural standards (like Wasm components or SQLite Sync), or competitor capabilities.
- Summarize these patterns inside the temporary workspace.

### 2. High-Level Zoom Out (Abstraction Mapping)
- Locate the existing boundaries of `tzro` under `/cmd/` and `/internal/`.
- Draw a broad relationship map of major components: how intents are classified, how the Kahn compiler schedules tasks, and how the MCP hosts run child processes.
- Identify the exact **seams** where the new functionality should be introduced.

### 3. Deep Feature Design
- Do not design shallow wrappers or leaky modules. Apply the **deletion test**: if the new feature is deleted, its complexity must vanish entirely from the rest of the application.
- Design the feature behind a small, high-leverage interface with strong operational locality.
- See [TRENDS-IDEATION.md](TRENDS-IDEATION.md) for full principles.

### 4. Interactive HTML Presentation
- Construct a standalone HTML file and write it to `$TMPDIR` as `ideation-report-<timestamp>.html`.
- Style it elegantly using Tailwind CSS and integrate side-by-side before/after Mermaid diagrams showing the proposed extension.
- Provide clear Cards detailing: Market Trend, Proposed Seam, Deep Module Solution, and Locality benefits.
- Open the report in the user's browser using `open` (macOS), `xdg-open` (Linux), or `start` (Windows).

### 5. Wiki Alignment
- Once the user selects and refines a proposal through the grilling loop:
  - Create or update the feature summarization file under `docs/wiki/features/<feature-slug>.md`.
  - Record major conceptual decisions under `docs/wiki/architecture/`.
  - Log the action in `docs/wiki/log.md`.

## Advanced features

For advanced guidelines, structured ideation prompts, and HTML dashboard templates, see [TRENDS-IDEATION.md](TRENDS-IDEATION.md).
