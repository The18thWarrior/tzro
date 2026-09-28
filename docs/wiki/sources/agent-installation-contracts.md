# Native agent installation contracts

Reviewed September 28, 2026 for [Standard installer implementation](../../../.scratch/adoption-readiness/issues/13-standard-installer-implementation.md).

The [installation guide](../../installation.md) records generated paths, supported environment overrides, and client capabilities. These contracts informed implementation; fixture tests do not certify live-client activation.

- [Codex hooks](https://learn.chatgpt.com/docs/hooks) require native review of untrusted definitions. [MCP](https://learn.chatgpt.com/docs/extend/mcp?surface=cli) uses TOML configuration. [Skills](https://learn.chatgpt.com/docs/build-skills) use the shared agent skill directory. Setup keeps trust decisions with Codex.
- [Claude hooks](https://code.claude.com/docs/en/hooks) support tool-result replacement through structured output. The adapter retains metadata when compacting stdout and stderr. [MCP scope](https://code.claude.com/docs/en/mcp) determines the configuration file.
- [Antigravity hooks](https://www.antigravity.google/docs/hooks) use named hook blocks. An allow decision bypasses normal permission handling; the adapter uses ask for requests that the workspace policy permits. Post-tool output does not replace tool results. [MCP](https://www.antigravity.google/docs/mcp) has separate native configuration.
- [Hermes shell hooks](https://hermes-agent.nousresearch.com/docs/user-guide/features/hooks/) use YAML configuration and native approval. Post-tool return values do not replace results. [MCP](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp/) shares the profile configuration file.
- [Copilot hook schemas](https://docs.github.com/en/copilot/reference/hooks-reference) define user hook files, pre-tool permission decisions, and post-tool result replacement. The installer targets Copilot CLI.
- [Pi extensions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md) can return changed tool-result content. The generated extension preserves non-text blocks. Native MCP registration remains unverified and is not generated.

[Published installation and client verification](../../../.scratch/adoption-readiness/issues/14-published-installation-and-client-verification.md) will record tested client versions, native loading, and actual invocation evidence.
