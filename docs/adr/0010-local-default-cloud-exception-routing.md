# Local-default / cloud-exception model routing

All LLM work defaults to the Local Model (llama-server sidecar). The Cloud Model is invoked only in three cases: (1) conversational T0 responses requiring world knowledge or low latency, (2) DAG planning via the Cloud Planner, and (3) T2 Supervised guardrail oversight for risky operations.

The `ModelMode` config (`cooperative`, `local`, `cloud`) determines how this split works:

- **Cooperative**: Local Model classifies intent and complexity. T0 chat routes to Cloud Model for the streamed response. T1/T2 compiles a DAG (Cloud Planner) and executes nodes (Local Model). If the sidecar is offline, classification falls back to heuristic matching, escalating to Cloud on inconclusive results.
- **Local**: Everything runs on the Local Model — classification, chat responses, DAG planning, execution. No cloud calls.
- **Cloud**: Everything runs on the Cloud Model. No sidecar needed.

A complexity gate promotes conversational-sounding prompts to tasks when the Complexity Tier resolves to T1 or T2, preventing shallow chat responses for requests that actually require multi-tool orchestration. The user is notified of the promotion.

This architecture optimizes for cost efficiency (the stated goal for X: most cost-efficient orchestration platform) by reserving expensive cloud inference for the narrow cases where the local model genuinely can't do the job.

## Considered Options

- **Cloud-default**: Use cloud for everything, local as optional accelerator. Rejected — violates the cost-efficiency principle and creates cloud dependency.
- **Strict split by stage**: Cloud always plans, local always executes, no crossover. Rejected — too rigid; conversational T0 responses benefit from cloud knowledge, and classification is a natural local model job.
