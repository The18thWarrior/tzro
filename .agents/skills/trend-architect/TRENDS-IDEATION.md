# Trend-Driven Architectural Ideation

This reference manual governs the advanced execution of the `trend-architect` skill. It provides guidelines for conducting market research, performing architectural zoom-out mapping, designing high-leverage deep modules, and generating high-fidelity HTML reports.

---

## 1. Market Research Workflow

When ideating on new functionalities, do not guess what is modern. Perform structured queries using the `browser` subagent or web search tool:

### Query Strategy

- **Sector Analysis**: Search for `"[target-domain] trends 2026"`, `"modern [target-domain] architectures"`, or `"next-gen [target-domain] tools"`.
- **Competitor/Alternative Deep Dives**: Look up architecture docs of popular tools in the agentic / automation space (e.g., _PowerSync_, _ElectricSQL_, _Temporal_, _Dify_, _LangGraph_, _LlamaIndex_).
- **Standards & Specifications**: Search for recent IETF drafts, W3C standards, OpenAPI specifications, or Wasm component model specs.

### Information Extraction Checklist

For each trend or competitor capability:

1. **The Core Value**: What developer friction does this feature solve?
2. **The Mechanics**: How does it operate under the hood (e.g., CRDTs, WAL-streaming, event sourcing, WebSockets, background threads)?
3. **The Interface**: What is the minimal API or schema presented to callers?

---

## 2. High-Level Zoom Out (Abstraction Mapping)

Before deciding _how_ a trend fits into `tzro`, you must zoom out and understand the current system layout without getting bogged down in low-level functions.

### Codebase Zoom-Out Rules

1. **Locate Core Boundaries**: Look at `/cmd` to find entry points, and `/internal` to find decoupled packages.
2. **Identify Callers and Owners**: Map out how a user request progresses:
   - _Intake_: Conversation / Command Line -> Intent Classification -> Complexity Tiering.
   - _Planning_: Task compilation (Kahn topological sort) -> Abstract Graph creation.
   - _Execution_: Task Execution -> Tool dispatching (MCP Host child processes) -> Output Compaction -> Disk-Backed JQ Cache.
3. **Find the Seams**: Identify where interfaces already exist or where a new interface can be introduced cleanly without breaking existing caller contracts.

---

## 3. Designing Deep Modular Extensions

Applying the `improve-codebase-architecture` principles, any new trend-driven feature must be designed as a **deep module**.

```
   ┌────────────────────────────────────────────────────────┐
   │                  SHALLOW EXTENSION                     │
   │                                                        │
   │   Intent  ──►  TrendService  ──►  Raw API  ──► Caller  │
   │                 (Leaky, wrapper-heavy, fragile)        │
   └────────────────────────────────────────────────────────┘

   ┌────────────────────────────────────────────────────────┐
   │                    DEEP EXTENSION                      │
   │                                                        │
   │   Intent  ──►  [ Unified Exec Seam ]                   │
   │                        │                               │
   │                        ▼ (Encapsulated)                │
   │               ┌──────────────────┐                     │
   │               │   Trend Adapter  │                     │
   │               │   (Deep Logic)   │                     │
   │               └──────────────────┘                     │
   └────────────────────────────────────────────────────────┘
```

### Depth & Deletion Test

- **Depth**: Put complex third-party APIs, network sync loops, or heavy logic behind a simple interface.
- **The Deletion Test**: If the user decides to drop this feature in 6 months, can we delete the module and have 100% of the complexity vanish? If we have to modify dozens of files across `/internal`, the extension was **shallow and leaky**. Keep it highly localized!

---

## 4. High-Fidelity HTML Presentation Template

The interactive HTML report should look extremely premium. Write it dynamically to `$TMPDIR` as `<tmpdir>/trend-ideation-<timestamp>.html` and open it immediately for the user.

### Template Skeleton

```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <title>Market-Driven Ideation Dashboard</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script type="module">
      import mermaid from "https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.esm.min.mjs";
      mermaid.initialize({ startOnLoad: true, theme: "dark" });
    </script>
    <style>
      body {
        background-color: #0b0f19;
        color: #f3f4f6;
        font-family: "Inter", sans-serif;
      }
    </style>
  </head>
  <body class="p-8 max-w-6xl mx-auto">
    <header class="mb-12 border-b border-gray-800 pb-6">
      <div class="flex justify-between items-center">
        <div>
          <h1
            class="text-4xl font-extrabold text-transparent bg-clip-text bg-gradient-to-r from-blue-400 to-indigo-500"
          >
            Trend Architect Ideation
          </h1>
          <p class="text-gray-400 mt-2">
            Market trends mapped to local-first codebase expansions
          </p>
        </div>
        <span
          class="px-3 py-1 bg-indigo-900/50 border border-indigo-500/30 text-indigo-300 rounded-full text-xs font-semibold"
        >
          Active Ideation
        </span>
      </div>
    </header>

    <!-- Market Trends Section -->
    <section class="mb-12">
      <h2 class="text-2xl font-bold text-gray-200 mb-6 flex items-center gap-2">
        ⚡ Market & Competitor Trends Discovered
      </h2>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <!-- Trend Card -->
        <div
          class="bg-gray-900/60 border border-gray-800 p-6 rounded-2xl hover:border-gray-700/60 transition-all duration-300"
        >
          <h3 class="text-xl font-bold text-white mb-2">
            Trend 1: [Trend Name]
          </h3>
          <p class="text-gray-400 text-sm mb-4">
            [Description of trend from competitor docs/standards]
          </p>
          <div class="flex gap-2">
            <span
              class="text-xs bg-emerald-950 text-emerald-400 px-2.5 py-1 rounded-md border border-emerald-800/40"
              >Market Relevance: High</span
            >
            <span
              class="text-xs bg-blue-950 text-blue-400 px-2.5 py-1 rounded-md border border-blue-800/40"
              >Competitor: [Name]</span
            >
          </div>
        </div>
        <!-- Add more trend cards -->
      </div>
    </section>

    <!-- Proposed Architectural Expansion Candidates -->
    <section class="mb-12">
      <h2 class="text-2xl font-bold text-gray-200 mb-6 flex items-center gap-2">
        🛠️ Deep Expansion Candidates
      </h2>

      <!-- Candidate Card -->
      <div class="bg-gray-900/40 border border-gray-800 p-8 rounded-3xl mb-8">
        <div class="flex justify-between items-start mb-6">
          <div>
            <span
              class="text-xs font-semibold tracking-wider text-indigo-400 uppercase"
              >Candidate 1</span
            >
            <h3 class="text-2xl font-extrabold text-white mt-1">
              [Candidate Feature Name]
            </h3>
          </div>
          <span
            class="px-3 py-1 bg-emerald-950 text-emerald-400 border border-emerald-800/40 rounded-full text-xs font-bold"
          >
            Recommendation: Strong
          </span>
        </div>

        <div class="grid grid-cols-1 lg:grid-cols-2 gap-8 mb-6">
          <div>
            <h4
              class="text-sm font-bold text-gray-400 uppercase tracking-wide mb-2"
            >
              The Opportunity
            </h4>
            <p class="text-gray-300 mb-4">
              [How this addresses the market trend using tzro concepts]
            </p>

            <h4
              class="text-sm font-bold text-gray-400 uppercase tracking-wide mb-2"
            >
              Depth & Locality
            </h4>
            <p class="text-gray-300">
              [Explain how the interface is kept extremely small while the
              implementation does the heavy lifting, passing the deletion test]
            </p>
          </div>

          <div
            class="bg-gray-950/80 border border-gray-850 p-6 rounded-2xl flex flex-col justify-center"
          >
            <h4
              class="text-sm font-bold text-gray-400 uppercase tracking-wide mb-4"
            >
              Architectural Impact
            </h4>
            <div class="mermaid">
              graph TD A[Existing Caller] --> B(New Small Interface Seam)
              subgraph Deepened Module B --> C[Concrete Trend Adapter] C -->
              D[(Encapsulated State/API)] end
            </div>
          </div>
        </div>
      </div>
    </section>
  </body>
</html>
```

---

## 5. Wiki Persistence Protocol

Once a candidate is approved and designed, record the output in the local wiki to make it discoverable by other agents.

### Path Mapping

1. **Feature Specs**: Create `.scratch/<feature-slug>/PRD.md` detailing the product requirements.
2. **Wiki Feature Record**: Create `docs/wiki/features/<feature-slug>.md` using the standard local wiki layout.
3. **Architecture Concepts**: If new terminology is defined, add the glossary words to `CONTEXT.md` and document the concept under `docs/wiki/architecture/<concept-slug>.md`.
4. **Operations Log**: Append a new action block to `docs/wiki/log.md`.
