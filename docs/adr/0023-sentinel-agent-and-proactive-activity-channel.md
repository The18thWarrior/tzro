# Sentinel Agent and Proactive Activity Channel

We introduce the Sentinel Agent — the second **Background Agent** (ADR-0022) — designed as an intelligence agent that reasons over accumulated context to surface emergent, proactive insights to the user. Unlike the Observer Agent (reactive, operational), the Sentinel is proactive and anticipatory, analogous to a knowledgeable colleague who notices patterns across everything they've seen.

## Trigger Mechanism

The Sentinel fires on a periodic heartbeat timer (default 5 minutes, configurable via `EngineConfig.SentinelInterval`). It does not wait for task execution events.

## Context Gathering

On each heartbeat, the Sentinel assembles a context snapshot from three sources:

1. **Workspace file changes**: Git-based (`git diff --name-only`, `git status`) in git workspaces, mtime-based scan with deterministic ignore list in non-git workspaces. The manual scan applies regex filtering for sensitive filenames (`passwords`, `secrets`, `.env`, `*.pem`, `*.key`, `credentials`, `id_rsa`, etc.) and structural ignores (`.git/`, `node_modules/`, build artifacts, binary extensions).

2. **Activity reports** (opt-in): A lightweight `tzro_activity_report` MCP tool that the cloud agent calls to report what it's working on. Schema: `{activity, filesTouched, toolsUsed}`. Governed by `AGENTS.md` as a recommended practice, not a hard requirement. When absent, the Sentinel relies solely on workspace scanning.

3. **Accumulated state**: Memory store, Knowledge Graph, micro-skills index, recent task outputs — all always available.

## Evaluation Pipeline: Retrieval-Grounded Synthesis

The Sentinel does NOT perform open-ended reasoning. It follows a structured pipeline:

1. **Embed**: Convert the context snapshot (file changes, activity description) into embeddings.
2. **Retrieve**: Semantic search against memory store, KG entities, and micro-skills. Produces a candidate context set with similarity scores.
3. **Threshold**: If no candidates exceed the similarity threshold, stop. Silence is preferred over noise.
4. **Synthesize**: The Local Model receives the matched context and synthesizes an alert explaining why these matches are relevant to the user's current work. The prompt is grounded — it references specific memories, entities, or skills, not open-ended advice.
5. **Confidence gate**: The synthesis output includes a GBNF-constrained confidence score. Below threshold, the alert is suppressed.

## Alert Delivery: Dual Path

Alerts are delivered through two mechanisms for maximum harness compatibility:

- **MCP resource notifications**: `tzro://sentinel/alerts` resource with `ResourceUpdated` notifications for harnesses that support resource subscriptions.
- **Tool-based discovery**: `tzro_sentinel_alerts` tool (analogous to `tzro_hook_list`) for harnesses that discover state via tool calls.

Both paths read from the same `durable_notifications` table filtered by `source = "sentinel"`.

## Alert Deduplication

Uses the existing `DurableNotification` status lifecycle. Each alert stores a context fingerprint hash in the `TargetID` field (e.g., `sha256("mem_12345:auth/middleware.go")`). Before producing a new alert, the Sentinel checks for existing notifications with the same `TargetID` and active status (`unread` or `read`). If found, the alert is suppressed. Alerts only regenerate after being `dismissed` by the user or the cloud agent.

## AGENTS.md Contract

The Offload Policy section gains two recommended practices:

- **Activity Reporting**: After every 5th consecutive in-context tool call, call `tzro_activity_report` to enable richer proactive assistance.
- **Sentinel Alert Handling**: When checking `tzro_hook_list` for pending approvals, also check `tzro_sentinel_alerts` for proactive insights. Surface `critical` alerts immediately, `suggestion` alerts at natural breaks.

## Considered Options

- **Deterministic evaluation pipeline**: Threshold checks for stale workflows, escalation counts, etc. Rejected for the Sentinel — these are operational concerns that belong in the Observer Agent. The Sentinel's value is emergent synthesis from accumulated context, which requires inference.
- **Open-ended reasoning**: Give the Local Model full context and ask "what proactive assistance can you offer?" Rejected — small models produce generic advice or hallucinated connections without retrieval grounding. The retrieval-first pipeline ensures alerts are anchored in concrete matched data.
- **MCP Sampling (server-initiated LLM calls)**: Use MCP's `sampling/createMessage` to have the harness run a prompt on the user's behalf. Rejected — most harnesses don't support it yet, and it requires significant trust escalation.
- **Resource-only delivery**: Only use MCP resource notifications. Rejected — harness support for resource subscriptions is uneven. Dual-path (resource + tool) ensures universal compatibility.
