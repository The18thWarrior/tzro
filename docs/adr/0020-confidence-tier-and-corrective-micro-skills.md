# Confidence Tier Pre-Flight and Corrective Micro-Skills

The Local Model (Tactician) now runs a pre-flight Confidence Tier classification before every GBNF bridge/exec node, asking itself whether it can extract the required parameters. If it claims `sufficient` but the subsequent tool call fails, the node surgically escalates to the Cloud Model, which re-runs successfully. The Cloud Model then immediately extracts a Corrective Micro-Skill from the diff between the failed local output and the successful cloud output, persisting it via the existing `synthesized_skills` pipeline. On future invocations matching the same trigger, the corrective skill is injected into the Tactician's context, teaching it to self-correct without weight updates.

We chose this over historical success-rate injection (Option 1) because success rates are a blunt instrument — a high-frequency tool with a specific failure pattern would be over-penalized and broadly routed to the cloud, contradicting our goal of maximizing local execution. Corrective Micro-Skills are surgical: they fix the specific anti-pattern (e.g., "use double quotes for SOQL string values") while keeping the tool routed locally for everything else.

The sticky escalation threshold (consecutive `insufficient` results before forcing cloud for the remainder of the task) defaults to 3, matching the existing Speed Floor, and is configurable via `EngineConfig.ConfidenceThreshold`.

## Considered Options

- **Historical success-rate injection:** Per-tool success/failure ratios injected into the classification prompt. Rejected because non-uniform usage distributions cause broad over-routing.
- **Bayesian per-tool threshold drift:** Mathematically rigorous but heavy infrastructure cost for v1.
- **Mid-generation abort signal (GAPG-style):** Requires custom model surgery or logit interception, breaking the clean llama-server HTTP abstraction.
