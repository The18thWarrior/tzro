# ADR-0002: Local Model Delegation & GBNF Constraints

## Context & Problem Statement

Running complex multi-system automations entirely on standard office laptops presents massive operational challenges. Small local models (ranging from 2B to 4B parameters, such as Qwen 2.5) are highly cost-efficient but lack the syntactic instruction-following capability of giant cloud models. 

When instructed to output a structured JSON payload for a tool call (such as a specific database insert or API request), these local models frequently hallucinate markdown backticks, conversational preambles, or malformed/unbalanced braces. This leads to high execution parser failure rates.

Furthermore, processing verbose tool descriptions and large system prompts repeatedly causes immense Cold-Start processing latency.

## Proposed Decision

We choose to implement strict **GBNF (GGML Backus-Naur Form) Grammar Constraints** at the local model inference gateway, coupled with aggressive performance optimization strategies.

1. **GBNF-Constrained Inference:** Every request sent to the local worker LLM (via a local server like `llama.cpp`) is wrapped with a dynamic grammar ruleset that physically permits the model to *only* generate syntax matching the targeted tool parameter schema. Markdown wrappers and conversational filler are mathematically forbidden at the token-selection logit level.
2. **Speculative Decoding:** We use n-gram simple speculation (`--spec-type ngram-simple`, `--draft-max 48`) to accelerate inference. Instead of loading a separate draft model, the engine finds verbatim n-gram matches in the prompt—which already contains the GBNF schema field names that appear in the output—and speculatively drafts token sequences with zero additional memory.
3. **KV Cache Prefix-Sharing:** Warm system prompts—which contain the baseline tool descriptions, operational instructions, and core context files—are pinned in KV Cache slot `0`. Sub-steps reuse this slot, bypassing heavy cold-start processing times.
4. **Active Cache Garbage Collector:** A background scheduler monitors local RAM limits, forcefully purging idle KV caches that have persisted for over 10 minutes.

---

## Technical Specifications

### 1. Production GBNF Grammar for Structured JSON Tool Calls

Below is the dynamic, highly robust GBNF grammar compiled at runtime. When passed to the llama.cpp engine, it ensures the model only selects tokens conforming to valid, standardized JSON data structures.

```ebnf
# Standard structured JSON parameters extraction grammar
root   ::= object
object ::= "{" ws member (ws "," ws member)* ws "}"
member ::= string ws ":" ws value
value  ::= object | array | string | number | "true" | "false" | "null"
array  ::= "[" ws (value (ws "," ws value)*)? ws "]"

# Scalar data types
string ::= "\"" ([^"\\] | "\\" (["\\/bfnrt] | "u" [0-9a-fA-F]{4}))* "\""
number ::= "-"? ([0-9] | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [+-]? [0-9]+)?

# Robust whitespace handling
ws     ::= [ \t\n\r]*
```

---

### 2. Local Hardware Optimizations Flow

```
┌────────────────────────────────────────────────────────┐
│              Local System Memory (RAM)                 │
└────────────────────────────────────────────────────────┘
  │
  ├─► warm system prompt cached in KV Cache Slot 0 (Prefix-Sharing)
  │   [Skips Prompt Processing Phase: 8s ──► < 100ms]
  │
  ├─► Speculative Decoding Pipeline:
  │   [N-gram Simple (--draft-max 48) + Target Worker Model (4B) ──► 2-3x Speedup, 0 extra RAM]
  │
  └─► Active Cache Garbage Collector (Background process):
      [Monitors idle context; flushes KV Cache if idle > 10 mins]
```

---

## Consequences

* **Pros:**
  * **100% Parsing Reliability:** GBNF physically prevents the model from generating malformed braces or markdown text wrapper blocks, ensuring error-free parsing.
  * **Minimized Latency:** Warm KV prefix caching and speculative decoding bring response times down to cloud-like execution speed entirely on local office hardware.
  * **Memory Preservation:** The garbage collector prevents memory leaks and background resource drain when the worker is inactive.
* **Cons:**
  * **Engine Dependency:** Requires inference backends that natively support GBNF logit bias constraints (such as `llama.cpp` or compatible runners).
  * **Speculation Ceiling:** N-gram simple speculation relies on verbatim prompt n-gram matches; speedup degrades on outputs with tokens not present in the prompt context.
