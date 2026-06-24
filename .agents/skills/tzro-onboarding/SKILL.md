---
name: tzro-onboarding
description: Runs the automated installation script, configures PATH environment variables, and registers MCP servers for AI editors. Use when onboarding a non-technical user, performing initial system setup, or configuring tzro for the first time.
---

# tzro Onboarding

Automates the complete installation and integration workflow for the `tzro` local-first agentic engine.

## Quick start

To install `tzro`, bootstrap all local database schemas, configure the user's PATH environment, and register the MCP servers for all detected AI editors, execute the onboard script:

```bash
./.agents/skills/tzro-onboarding/scripts/onboard.sh
```

## Workflows

### 1. Perform New Install
When a user asks to install `tzro` or needs setup assistance:
1. Verify the project environment.
2. Run the onboarding script: `bash ./.agents/skills/tzro-onboarding/scripts/onboard.sh`.
3. Instruct the user to restart their terminal session or load their new path using `source ~/.zshrc` (or their active shell config).

### 2. Verify Install & MCP Config
To confirm a successful installation:
1. Check that `~/.tzro/bin/tzro` and `~/.tzro/tzro.db` exist.
2. Confirm `~/.gemini/config/mcp_config.json` contains the `tzro` MCP server command entry.
