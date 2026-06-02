# ADR-0008: Mode-Dependent KV Cache Quantization

In cooperative mode the local model only performs short GBNF-constrained JSON extraction (tool argument generation), so we quantize both K and V caches to **Q4_0** — contexts are short and fresh (the slot is erased between tasks), grammar constraints catch any attention drift, and there is no accumulated conversation history, yielding 4× memory savings over FP16. In local mode the model handles planning AND execution: planning prompts are longer, require reasoning, and generate complex abstract graph JSON (up to 14–16K tokens), so we use **Q8_0** for both K and V to preserve attention fidelity while still halving memory versus FP16. Because the KV cache type is a server launch flag, changing `modelMode` requires a server restart.

## Status
Accepted

## Considered Options
* **FP16 for all modes**: Rejected — 720 MB at 32K context is excessive for resource-constrained hardware.
* **Q4_0 for all modes**: Rejected — planning quality degrades noticeably with aggressive KV quantization on longer contexts.
* **Configurable multi-tier strategy (e.g. X's 4-tier system)**: Rejected as over-engineered; two clear modes don't warrant a strategy abstraction.
