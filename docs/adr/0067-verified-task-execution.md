# Verified Task Execution

The local model can produce 5.0-quality research output — benchmark data shows `technical_deep_dive_gguf` scoring 5.0 in R15 and 1.0 in R14 on the same task, same model, same framework. The capability is in the weights; the problem is variance with no verification mechanism. Nine compensatory mechanisms (Generation Guard, DRY Sampling, meta-commentary detection, Synthesis Validation Gate, Two-Pass Extraction, Recall Loop Inversion, No-Action Retry, Exploration Queue, SQL Auto-Extraction) were added since v1.0.2 to address output quality, but the benchmark quality trend plateaued at R15 (3.70 avg) and regressed in subsequent runs.

We adopt **Verified Task Execution**: the local model completes tasks fully (exploration + synthesis), then a single cloud call verifies the output against the original goal and exploration context, optionally re-synthesizing in the same call if the verification rejects. This is not context compression — the local model does the work, and the cloud model acts as a QA department.

## Considered Options

1. **Context Compression** — reposition the local model as a context preparation engine for the frontier model. Rejected: abandons the demonstrated capability (stochastic 5.0 scores), commoditizes tzro into invisible infrastructure competing with Claude `/compact` and Google ADK compaction.

2. **More execution-layer hardening** — continue adding compensatory mechanisms. Rejected: nine mechanisms create an exponential interaction space where threshold tuning for one failure pattern causes false positives on another. Quality plateaued despite continued investment.

3. **Verified Task Execution** — the local model completes the task, a cloud verification gate accepts or rejects, cloud re-synthesizes on rejection from the Recall Node's compacted context. Accepted: preserves core ethos ("engineering makes the SLM capable"), adds verification not replacement, and enables a progressive learning loop via Corrective Micro-Skills.

## Consequences

- All eight existing compensatory mechanisms are **retained** — their purpose reframes from "compensate for bad output" to "maximize context quality for higher verification accept rates." The meta-commentary detector moves into the Structural Pre-Check stage.
- **Confidence Tier** scope narrows to per-node parameter extraction only. Terminal output quality is owned by the Verification Gate.
- Cloud cost increases by $0.03-0.05 per Probe/Analyze task (verification call with compressed refinedContext). On rejection, the same call produces a re-synthesis — no additional round-trip.
- `strict-local` Privacy Level runs Structural Pre-Check only — no cloud verification, no re-synthesis. The user accepts the variance.
- The Execution Envelope gains a `verification` section with the Verification Rubric scores, surfaced to the harness via MCP.
