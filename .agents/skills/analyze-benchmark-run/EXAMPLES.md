# Usage Examples: Generating Beautiful HTML Dashboards

This file provides reference structures and reusable HTML templates for generating premium, styled interactive HTML diagnostic reports using Tailwind CSS and Mermaid.

---

## 1. High-Fidelity HTML Report Template

When the agent compiles an analysis, write it to `$TMPDIR` and populate it with this scaffold:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Benchmark Diagnostic & Architectural Action Plan</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script type="module">
    import mermaid from 'https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.esm.min.mjs';
    mermaid.initialize({ startOnLoad: true, theme: 'dark' });
  </script>
  <style>
    body {
      background-color: #0B0F19;
      color: #E2E8F0;
      font-family: 'Inter', system-ui, -apple-system, sans-serif;
    }
  </style>
</head>
<body class="p-8 max-w-7xl mx-auto">
  <!-- Header -->
  <header class="mb-12 border-b border-gray-800 pb-6 flex justify-between items-center">
    <div>
      <span class="px-3 py-1 bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 text-xs font-semibold rounded-full uppercase tracking-wider">Benchmark Audit</span>
      <h1 class="text-4xl font-extrabold tracking-tight mt-2 text-white bg-clip-text bg-gradient-to-r from-white to-gray-400">
        Engine Diagnostic Report
      </h1>
      <p class="text-gray-400 mt-1">A thorough analysis of failure modes and sound architectural remedies.</p>
    </div>
    <div class="text-right">
      <p class="text-xs text-gray-500">Run File:</p>
      <p class="font-mono text-sm text-indigo-300">benchmark_results_5_27_2026_04_25.json</p>
    </div>
  </header>

  <!-- Metrics Grid -->
  <section class="grid grid-cols-1 md:grid-cols-4 gap-6 mb-12">
    <div class="bg-gray-900/50 border border-gray-800 rounded-xl p-6 backdrop-blur-md">
      <p class="text-gray-400 text-sm font-medium">Overall Pass Rate</p>
      <p class="text-3xl font-extrabold text-indigo-400 mt-2">56.00%</p>
      <p class="text-xs text-gray-500 mt-1">14 / 25 Scenarios Passed</p>
    </div>
    <div class="bg-gray-900/50 border border-gray-800 rounded-xl p-6 backdrop-blur-md">
      <p class="text-gray-400 text-sm font-medium">Planning Match</p>
      <p class="text-3xl font-extrabold text-emerald-400 mt-2">80.00%</p>
      <p class="text-xs text-gray-500 mt-1">20 / 25 Correct Tool Chains</p>
    </div>
    <div class="bg-gray-900/50 border border-gray-800 rounded-xl p-6 backdrop-blur-md">
      <p class="text-gray-400 text-sm font-medium">Parameter Match</p>
      <p class="text-3xl font-extrabold text-amber-400 mt-2">56.00%</p>
      <p class="text-xs text-gray-500 mt-1">14 / 25 Correct Args Extracted</p>
    </div>
    <div class="bg-gray-900/50 border border-gray-800 rounded-xl p-6 backdrop-blur-md">
      <p class="text-gray-400 text-sm font-medium">Token Distribution</p>
      <p class="text-3xl font-extrabold text-pink-400 mt-2">93.5% Cloud</p>
      <p class="text-xs text-gray-500 mt-1">7,187 Local vs. 104,470 Cloud</p>
    </div>
  </section>

  <!-- Failures & Architectural Candidates -->
  <section class="space-y-8">
    <h2 class="text-2xl font-bold text-white border-l-4 border-indigo-500 pl-3">Architectural Action Plan</h2>
    
    <!-- Candidate Card 1 -->
    <div class="bg-gray-900/40 border border-gray-800 rounded-2xl p-8 hover:border-gray-700 transition duration-300">
      <div class="flex justify-between items-start mb-6">
        <div>
          <span class="px-2.5 py-0.5 bg-rose-500/10 text-rose-400 border border-rose-500/20 text-xs font-semibold rounded-full">Parameter Mismatch</span>
          <h3 class="text-xl font-bold text-white mt-2">Refactor Schema Parser into a Deep Unmarshal Adapter</h3>
        </div>
        <span class="px-3 py-1 bg-indigo-500/20 text-indigo-300 border border-indigo-500/30 text-xs font-bold rounded-md">Strong Recommendation</span>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-2 gap-8 mb-6">
        <div>
          <h4 class="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-2">Friction</h4>
          <p class="text-sm text-gray-400 leading-relaxed">
            Many simple tool scenarios (e.g. <code>simple_python_253</code>, <code>live_multiple_371-134-16</code>) select the correct tool but fail due to parameter type mismatch or empty arguments. The engine currently lacks an intermediate type-coercion layer, leaving raw unmarshaling of LLM string outputs to simple individual tool boundaries which error out on slight deviations.
          </p>
        </div>
        <div>
          <h4 class="text-sm font-semibold text-gray-300 uppercase tracking-wider mb-2">Deepening Solution & Benefits</h4>
          <p class="text-sm text-gray-400 leading-relaxed">
            Consolidate argument parsing behind a <strong>Deep Unmarshal Adapter</strong> seam. Callers get maximum <strong>leverage</strong> because they pass unvalidated input, and the adapter internally applies strict JSON schema coercion, default injects, and handles date/time conversions. Complexity vanishes from individual tool modules.
          </p>
        </div>
      </div>

      <!-- Before/After Visualization using Mermaid -->
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6 bg-gray-950/60 p-6 rounded-xl border border-gray-900">
        <div>
          <p class="text-xs font-bold text-rose-400 uppercase tracking-widest mb-4">Before (Shallow, Leaky Seam)</p>
          <pre class="mermaid bg-transparent">
classDiagram
  class LLM_Planner {
    +ParseArgs(str)
  }
  class WeatherTool {
    +Execute(city, limit)
  }
  LLM_Planner --> WeatherTool : Raw string unmarshal (Fails on typing)
          </pre>
        </div>
        <div>
          <p class="text-xs font-bold text-emerald-400 uppercase tracking-widest mb-4">After (Deep Coercion Adapter)</p>
          <pre class="mermaid bg-transparent">
classDiagram
  class LLM_Planner
  class DeepUnmarshalAdapter {
    +CoerceToSchema(rawArgs, schema) JSON
  }
  class WeatherTool {
    +Execute(WeatherArgs)
  }
  LLM_Planner --> DeepUnmarshalAdapter : Raw string output
  DeepUnmarshalAdapter --> WeatherTool : Strictly Coerced Struct
          </pre>
        </div>
      </div>
    </div>
  </section>
</body>
</html>
```

This template leverages responsive flexbox/grid containers, curated color palettes, elegant gradients, semantic groupings, and seamless CDN-based rendering for charts and visual diagrams.
