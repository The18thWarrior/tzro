# Standard installation

Standard installs the native CLI and configures supported detected agent clients. It does not download model runtimes or change provider routing.

## One-liner release download

The installer selects macOS arm64, macOS amd64, or Linux amd64 and verifies SHA-256 integrity against published checksums:

```sh
curl -sSL https://get.tzro.ai | sh
```

For CLI-only installation (skipping agent configuration):

```sh
curl -sSL https://get.tzro.ai | sh -s -- --cli-only
```

Direct S3 distribution command:

```sh
curl -fsSL https://tzro-app.s3.amazonaws.com/install.sh | sh
```

## Install from source checkout

Requirements: Git and Go 1.26 or later.

```sh
CGO_ENABLED=0 go build -o ./bin/tzro ./cmd/tzro
TZRO_SOURCE_BIN=./bin/tzro sh ./install.sh
export PATH="$HOME/.tzro/bin:$PATH"
```

To install only the CLI from source:

```sh
TZRO_SOURCE_BIN=./bin/tzro sh ./install.sh --cli-only
```

| Setting | Purpose |
| :--- | :--- |
| `TZRO_INSTALL_DIR` | Installation root; defaults to `~/.tzro` |
| `TZRO_VERSION` | Pin a release tag; defaults to `latest` |
| `TZRO_DOWNLOAD_BASE_URL` | Override the release root for a mirror or controlled fixture |
| `TZRO_SOURCE_BIN` | Install an explicitly selected local executable |
| `--cli-only` | Skip agent configuration |
| `--no-modify-path` or `TZRO_NO_MODIFY_PATH=1` | Skip shell startup changes |

The installer writes an idempotent PATH entry for Bash, Zsh, or POSIX sh. Bash setup covers interactive and login startup files. Open a new shell or apply the printed export command. Agent configuration uses an absolute binary path.

## Agent capabilities

These entries describe generated configuration and adapter behavior. Fixture tests cover configuration preservation and protocol handling. Live-client activation still needs validation against recorded client versions.

| Client | Skill | MCP | Hook behavior |
| :--- | :--- | :--- | :--- |
| Antigravity | `~/.gemini/config/skills` | `~/.gemini/config/mcp_config.json` | Workspace privacy checks; post-tool result replacement is unavailable |
| Claude Code | `~/.claude/skills` | `~/.claude.json` | Privacy checks and supported tool-output replacement |
| Hermes | `~/.hermes/skills` | `~/.hermes/config.yaml` | Privacy checks; native hook approval required; post-tool return values are ignored |
| GitHub Copilot CLI | `~/.copilot/skills` | `~/.copilot/mcp-config.json` | Privacy checks and supported tool-output replacement |
| Pi-Coder | `~/.pi/agent/skills` | Not automatically registered | Native extension compacts text results and retains non-text content |
| Codex | `~/.agents/skills` | `~/.codex/config.toml` | Privacy checks; native hook review required; no tool-output replacement claimed |

Skills guide explicit use of tzro CLI tools. MCP-capable clients can also discover tzro tools through the local server. This avoids assuming identical hook behavior across clients.

Pi extensions can supply MCP bridges. This installer does not install a third-party bridge or claim that an unverified MCP file is active.

`CLAUDE_CONFIG_DIR`, `HERMES_HOME`, `COPILOT_HOME`, and `PI_CODING_AGENT_DIR` relocate their respective client configuration roots. `CODEX_HOME` relocates Codex configuration; its shared user skill remains under `~/.agents/skills`.

`--workspace` writes project configuration where applicable. Copilot CLI MCP still requires user-scope initialization. Hermes uses profile configuration rather than automatic repository discovery; select the profile with `HERMES_HOME`.

Configure clients installed later with:

```sh
tzro init --hooks auto
```

Select one client with `--hooks codex`, or all six with `--hooks all`. Detection uses client-specific directories or executables. Generic `.agents` and `.github` directories alone do not trigger setup.

## Configuration and activation

Setup reports each integration separately. A failure returns a nonzero exit status even if other integrations succeed. The CLI remains installed after partial agent setup.

Codex requires review of new or changed hook definitions through `/hooks`. Hermes can request approval on first hook use. The installer does not modify native trust decisions. Existing trust is not independently verified by setup. Restart clients after configuration changes.

Generated files have adjacent `.tzro.sha256` ownership records. Setup preserves a generated file that the user modified. Conflicting MCP entries and modified tzro hooks also remain intact. Review the reported file, then move the conflicting tzro entry aside before repeating initialization.

Codex configuration with an inline `mcp_servers` table can prevent safe insertion. Setup reports the conflict and preserves the file. Convert that table to standard TOML sections before repeating setup.

`tzro doctor` detects client environments without changing them. Its detection result does not certify that integrations are loaded.

## Full setup

Full adds proxy routing and experimental runtimes to Standard. The decision runtime is now `pkg/decision`, with `jev-score` and the JEV-style Qwen 0.8B model. GLiNER remains the span extractor. See [ADR-0096](adr/0096-migration-to-jev-style-and-libllama-decision-engine.md).

## Client references

- [Codex hooks and native trust](https://learn.chatgpt.com/docs/hooks), [MCP](https://learn.chatgpt.com/docs/extend/mcp?surface=cli), and [skills](https://learn.chatgpt.com/docs/build-skills).
- [Claude Code hooks](https://code.claude.com/docs/en/hooks) and [MCP scopes](https://code.claude.com/docs/en/mcp).
- [Antigravity hooks](https://www.antigravity.google/docs/hooks) and [MCP](https://www.antigravity.google/docs/mcp).
- [Hermes shell hooks](https://hermes-agent.nousresearch.com/docs/user-guide/features/hooks/) and [MCP](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp/).
- [Copilot CLI hook schemas](https://docs.github.com/en/copilot/reference/hooks-reference).
- [Pi native extensions](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md).
