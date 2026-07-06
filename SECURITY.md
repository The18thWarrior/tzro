# Security & Sandbox Manifesto

> **tl;dr** — tzro runs a local LLM that intercepts and executes tool calls on your machine. We assume you are skeptical. This document explains, with no hand-waving, exactly how every layer of the system is designed to prevent remote code execution, prompt injection, and data exfiltration — and what the remaining threat boundaries are.

---

## Table of Contents

- [Philosophy: Least-Privilege by Default](#philosophy-least-privilege-by-default)
- [1. Data Sovereignty — Nothing Leaves Your Machine](#1-data-sovereignty--nothing-leaves-your-machine)
- [2. Sandboxed Execution Boundaries](#2-sandboxed-execution-boundaries)
  - [2.1 Container Isolation (Podman / Docker)](#21-container-isolation-podman--docker)
  - [2.2 WebAssembly Sandboxing (wazero)](#22-webassembly-sandboxing-wazero)
  - [2.3 Loopback-Only Network Binding](#23-loopback-only-network-binding)
- [3. Grammar-Constrained Generation (GBNF)](#3-grammar-constrained-generation-gbnf)
  - [3.1 How Logit-Level Constraints Work](#31-how-logit-level-constraints-work)
  - [3.2 The Semantic Validator Pipeline](#32-the-semantic-validator-pipeline)
  - [3.3 What GBNF Does and Does Not Guarantee](#33-what-gbnf-does-and-does-not-guarantee)
- [4. Defense-in-Depth: Runtime Safety Controls](#4-defense-in-depth-runtime-safety-controls)
  - [4.1 Tool Dispatch Allow-Lists](#41-tool-dispatch-allow-lists)
  - [4.2 Loop Circuit Breakers](#42-loop-circuit-breakers)
  - [4.3 Proactivity Ladder & Attention Queue](#43-proactivity-ladder--attention-queue)
- [5. Credential & Secret Management](#5-credential--secret-management)
- [6. Observability & Audit Trail](#6-observability--audit-trail)
- [7. Threat Model & Remaining Boundaries](#7-threat-model--remaining-boundaries)
- [8. Responsible Disclosure](#8-responsible-disclosure)

---

## Philosophy: Least-Privilege by Default

tzro follows a **Security-by-Design** posture across every architectural layer:

1. **Local-first**: All data, model weights, execution state, and memory persist on your local filesystem. Cloud models are only contacted when local capability is insufficient, and this is configurable.
2. **Deny-by-default**: Tool execution runs with minimal permissions. Write access, network access, and external side effects require explicit opt-in or user approval.
3. **Structural enforcement over behavioral promises**: Where traditional AI guardrails rely on prompt instructions ("don't run dangerous commands") — which can be jailbroken — tzro enforces constraints mathematically at the token-generation level. It's the difference between asking someone nicely not to open a door and welding the door shut.

---

## 1. Data Sovereignty — Nothing Leaves Your Machine

| Guarantee | Mechanism |
|:---|:---|
| **Zero telemetry** | `tzrod` makes zero outbound network requests during normal operation. Model weights are fetched once during bootstrap. Everything else is local. Verify: `sudo lsof -i -P \| grep tzrod`. |
| **Local state storage** | All execution state, memory, and knowledge graph data persists in `~/.tzro/tzro.db` — a single SQLite file on your disk. No cloud sync. |
| **Loopback binding** | The MCP server and HTTP/SSE daemon bind exclusively to `127.0.0.1`. Zero external network interfaces are exposed. |
| **Configurable privacy** | The `privacyLevel` setting provides three tiers: **strict-local** (blocks all cloud interactions), **hybrid** (default — escalates to cloud only when local capability is insufficient), and **cloud-preferred**. |

---

## 2. Sandboxed Execution Boundaries

tzro employs three complementary isolation layers — containers for process isolation, WebAssembly for compute sandboxing, and loopback network binding for host-level protection.

### 2.1 Container Isolation (Podman / Docker)

When tasks require code execution beyond simple file operations, or when third-party MCP tool servers are untrusted, tzro orchestrates short-lived, task-scoped container sandboxes using rootless Podman or Docker.

**Container security properties:**

| Property | Enforcement |
|:---|:---|
| **Ephemeral** | Containers are created per-task with `--rm` and destroyed on completion. No persistent state leaks between executions. |
| **Read-only filesystem** | The `--read-only` flag prevents all writes to the container filesystem. |
| **Network decoupled** | Containers run with `--network=none`. No DNS resolution, no HTTP requests, no data exfiltration vector. |
| **Resource-capped** | Each container is CPU and memory-limited (`--memory=512m --cpus=1.0`) to prevent runaway processes from impacting development workflow. |
| **Read-only mounts** | Project directories are mounted with `:ro` by default. Write access requires explicit opt-in. |
| **No package installation** | With `--network=none`, containers have no access to package registries — no `npm install`, no `pip install`, no supply-chain attack vector. |
| **Rootless execution** | Podman runs entirely in userspace via `podman --rootless` — no Docker daemon, no root access required. |

**Example: How tzro spawns a sandboxed container:**

```bash
# Sandboxed execution container with all security flags
podman run --rm --read-only \
  --network=none \
  --memory=512m --cpus=1.0 \
  -v /project:/workspace:ro \
  tzro-sandbox:latest \
  sh -c "$TASK_COMMAND"
```

**Containerized MCP Hosts:**

MCP tool servers flagged with `useDocker: true` in configuration run inside isolated Docker containers. Only environment variables explicitly declared in the server's `env` configuration are resolved from the host and injected via `docker run -e` flags — the container never inherits the host's ambient environment.

```json
{
  "mcpServers": {
    "postgres": {
      "command": "mcp-server-postgres",
      "useDocker": true,
      "dockerImage": "mcp/postgres:latest",
      "env": {
        "DATABASE_URL": "$DATABASE_URL"
      }
    }
  }
}
```

In this example, only `DATABASE_URL` is resolved from the host environment. All other host variables remain invisible to the containerized process.

### 2.2 WebAssembly Sandboxing (wazero)

For custom compute logic that doesn't need I/O access, tzro executes **Sandboxed Micro-Skills** — compiled WebAssembly binaries — inside a hermetic wazero runtime with zero ambient authority.

**WASM sandbox properties:**

| Property | Enforcement |
|:---|:---|
| **No filesystem access** | No pre-opened directories. The module cannot read or write any files on the host. |
| **No network access** | The WASI configuration provides no network capabilities. |
| **No environment variables** | Environment variables are not forwarded to the module. |
| **Stateless execution** | Each invocation compiles and instantiates a fresh module. No state persists between calls. |
| **Timeout enforcement** | A configurable execution timeout (default: 30s) is enforced via Go context cancellation with `WithCloseOnContextDone`. |
| **I/O isolation** | Input is provided via stdin (JSON), output is captured from stdout. No other I/O channels are available. |

This is the highest-security tool execution mode. The WASM module literally *cannot* read files, make network calls, or see environment variables. It exists in a hermetic sandbox where only stdin/stdout communication is permitted.

### 2.3 Loopback-Only Network Binding

The `tzrod` daemon and MCP server bind exclusively to `127.0.0.1:0` (dynamically assigned port on the loopback interface). This means:

- **Zero external attack surface** — the service is unreachable from any other machine on the network.
- **No TLS complexity** — loopback traffic is inherently protected from network-level interception.
- **OS-level process isolation** — only processes running on the same machine under the same user account can communicate with the daemon.

---

## 3. Grammar-Constrained Generation (GBNF)

This is where tzro fundamentally diverges from other AI tool-calling frameworks. Instead of relying solely on prompt-level instructions to prevent malicious output, tzro applies **logit-level grammar constraints** that structurally prevent entire classes of injection attacks.

### 3.1 How Logit-Level Constraints Work

When the local LLM generates tool calls, it doesn't produce free-form text. Every generation call is bound to a **GBNF (GGML Backus-Naur Form) grammar** that restricts which tokens the model can emit at each decoding step.

This operates at the logit sampling level — before token probabilities are even considered, tokens that would violate the grammar are masked to zero probability:

```
P(Token ∉ Grammar Schema) = 0
```

The model is physically incapable of producing output that violates the grammar structure. This is not post-hoc filtering or regex validation — it is mathematically enforced during generation.

**Example grammar constraint for tool dispatch:**

```ebnf
root      ::= "{" ws "\"tool\":" ws tool-name "," ws
               "\"args\":" ws json-object "," ws
               "\"reasoning\":" ws string ws "}"
tool-name ::= "\"read_file\"" | "\"list_dir\"" | "\"search_files\"" | "\"web_search\""
```

When this grammar is bound to the inference engine, the model cannot:
- Emit arbitrary shell commands or code outside the JSON structure
- Reference tools that aren't in the allow-list
- Produce malformed JSON that could be parsed ambiguously by downstream systems
- Inject conversational text, markdown wrappers, or XML tags around the structured output

### 3.2 The Semantic Validator Pipeline

To balance generation speed with structural safety, tzro uses a two-stage coercion pipeline:

```
           Local Model
               │
               ▼
    ┌─────────────────────┐
    │  Pass 1: Free-form  │   Model generates loose XML tags
    │  XML Generation     │   under shallow GBNF wrapper
    │  (maximum decoding  │   constraints for speed
    │   freedom)          │
    └─────────┬───────────┘
              │
              ▼
    ┌─────────────────────┐
    │  Pass 2: Semantic   │   Deterministic coercion:
    │  Validator Node     │   type casting, default
    │  (GBNF-constrained  │   imputation, fuzzy matching
    │   JSON refinement)  │   → strict JSON tool params
    └─────────┬───────────┘
              │
              ▼
        Tool Execution
```

**Pass 1** lets the model reason freely in a format it handles well (XML), under shallow structural constraints that ensure parsability.

**Pass 2** is a deterministic boundary seam — a durable, checkpointed DAG node — that coerces the extracted values into the exact JSON schema required by the target tool. If coercion fails (e.g., a string value where an integer is required), it triggers a targeted retry loop with an explicit error message, not a silent failure.

This architecture achieves both high generation speed (no dense token-by-token grammar masking for deeply nested schemas) and structural safety (the execution engine only ever receives schema-valid JSON).

### 3.3 What GBNF Does and Does Not Guarantee

We want to be precise about the security claims:

**GBNF eliminates:**
- ✅ **Structural injection attacks** — the model cannot emit arbitrary commands, shell scripts, or code fragments outside the defined grammar structure.
- ✅ **Malformed output** — every generated payload is syntactically valid JSON/XML that matches the declared schema structure.
- ✅ **Tool name hallucination** — when tool names are enumerated in the grammar, the model can only reference tools that actually exist.
- ✅ **Format-based prompt injection** — an attacker cannot craft input that causes the model to break out of the structured output format, because the grammar is enforced at the logit level regardless of prompt content.

**GBNF does not eliminate:**
- ⚠️ **Semantic errors** — the model might extract the wrong *value* for a parameter (e.g., reading the wrong column from a database query). The output is syntactically valid but semantically incorrect.
- ⚠️ **Incorrect tool selection** — the model might choose a valid tool from the allow-list that isn't the right tool for the task.
- ⚠️ **Upstream data quality issues** — if the data provided to the model is misleading or corrupted, GBNF cannot fix that.
- ⚠️ **Cloud model failures** — GBNF applies only to the local inference engine (`llama.cpp`). When execution falls back to cloud providers, equivalent protection is achieved via strict JSON schema injection into the system prompt — effective but not mathematically guaranteed.

This is why tzro layers multiple defense mechanisms rather than relying on any single control.

---

## 4. Defense-in-Depth: Runtime Safety Controls

Beyond structural generation constraints, tzro implements runtime safety controls that limit blast radius even if the model makes incorrect decisions.

### 4.1 Tool Dispatch Allow-Lists

Each DAG execution node specifies an `allowedTools` list at compile time. The Kahn Compiler constrains each node to only the tools it needs for its specific step. A node compiled to call `salesforce_query` cannot suddenly invoke `write_file` — the dispatch layer rejects unlisted tools regardless of what the model requests.

### 4.2 Loop Circuit Breakers

To prevent runaway execution, the runtime enforces hard limits:

| Control | Threshold | Behavior |
|:---|:---|:---|
| **Consecutive error breaker** | 3 consecutive failures | Breaks the loop, unbinds tools, forces a failure summary |
| **Duplicate call blocker** | 2 identical calls (same tool + same args) | Rejects the call with a corrective nudge |
| **Per-tool call cap** | 5 calls per tool (configurable) | Prevents infinite retry spirals on a single tool |
| **Mutation budget** | Per-task cap on dynamic node spawns | Prevents runaway DAG expansion from Edge Thought traversal |
| **Failure dampening** | 3 consecutive spawned-node failures | Suppresses further dynamic node creation |

### 4.3 Proactivity Ladder & Attention Queue

Background agents and autonomous workflows are governed by a five-tier safety classification:

| Tier | Classification | Gate |
|:---|:---|:---|
| **L0** | Observe | No user interaction — passive monitoring only |
| **L1** | Prepare | Internal state changes (memory, caching) — no user approval needed |
| **L2** | Suggest | Presents recommendations to the user via Attention Queue |
| **L3** | Reversible Action | Requires explicit user approval before execution |
| **L4** | External Side Effect | Requires explicit user approval; effects may be irreversible |

Each tool has a **Tool Proactivity Level** annotation. The execution harness gates tool dispatch against the workflow's approved ceiling. Background agents cannot escalate above their approved tier without returning to the Attention Queue for user consent.

---

## 5. Credential & Secret Management

tzro never stores raw API keys or secrets in configuration files. All sensitive values use **Delegated Secrets** — environment variable references prefixed with `$` that are resolved at runtime from the host environment:

```json
{
  "cloudProvider": "gemini",
  "apiKey": "$GEMINI_API_KEY"
}
```

**Security properties:**

- **No secrets in config files** — configuration files contain only variable references, never actual credentials.
- **Runtime resolution** — the `$` prefix triggers recursive resolution from the host shell environment at the moment of use.
- **Container isolation** — for Docker-hosted MCP servers, only explicitly declared environment variables are resolved and passed via `docker run -e`. The container never inherits the host's full environment.
- **CI/CD alignment** — this pattern works natively with cloud CI/CD systems, terminal profiles, and headless server pipelines without requiring proprietary credential storage.

---

## 6. Observability & Audit Trail

Every execution decision is logged and auditable:

| Capability | Description |
|:---|:---|
| **Durable checkpointing** | Every DAG node execution is checkpointed to SQLite. If the process crashes mid-task, execution state is preserved and resumable. |
| **Observer Agent verification** | A background agent performs post-execution reflection — verifying grammar constraint compliance and extracting memory from completed trajectories. |
| **Telemetry dashboard** | Grammar constraint verification (confirmation that all LLM outputs matched GBNF schemas), token consumption metrics, tool dispatch logs, and execution latencies are recorded per-task. |
| **Edge Thought audit trail** | Every dynamic graph mutation (node spawning from Activation Threshold evaluation) is logged with confidence scores, reasoning state, and the decision to continue/spawn/halt. |
| **Zero-cost verification** | Run `sudo lsof -i -P | grep tzrod` at any time to verify that the daemon has no open network connections beyond loopback. |

---

## 7. Threat Model & Remaining Boundaries

We believe in honest security communication. Here is what tzro protects against, what it mitigates, and what remains outside scope:

### Protected Against (Structural Controls)

| Threat | Defense |
|:---|:---|
| **Remote Code Execution via model output** | GBNF grammar constraints prevent the model from generating arbitrary executable payloads. Output is structurally constrained to declared tool schemas. |
| **Data exfiltration from execution containers** | `--network=none` on containers blocks all outbound traffic. No DNS, no HTTP, no covert channels via network. |
| **Supply-chain attacks in containers** | Network isolation + read-only filesystem prevent `npm install`, `pip install`, or any package manager from executing. |
| **Credential leakage to containers** | Only explicitly declared environment variables are passed to Docker containers. Host environment is never inherited. |
| **Tool name injection** | Grammar-enumerated tool names prevent the model from calling tools outside the allow-list. |
| **Cross-task state leakage** | Ephemeral containers (`--rm`) and stateless WASM execution prevent state from persisting between tasks. |

### Mitigated (Defense-in-Depth)

| Threat | Mitigation |
|:---|:---|
| **Semantic hallucination** | Semantic Validator pipeline with deterministic retry loops. Corrective Micro-Skills extracted from past failures. Confidence Tier pre-flight assessment. |
| **Runaway execution** | Circuit breakers, per-tool call caps, mutation budgets, failure dampening. |
| **Unauthorized autonomous actions** | Proactivity Ladder tiers with explicit user approval gates for L3/L4 actions. |

### Out of Scope

| Threat | Rationale |
|:---|:---|
| **Host OS compromise** | tzro assumes the host operating system and user account are trusted. If an attacker has root access to your machine, containerization and grammar constraints provide no additional protection. |
| **Malicious GGUF model weights** | tzro trusts the model weights loaded into the inference engine. Users should only load models from trusted sources. |
| **Cloud provider security** | When `privacyLevel` is set to `hybrid` or `cloud-preferred`, prompts and data are sent to the configured cloud LLM provider. Cloud provider security is governed by their respective policies. |
| **First-party tool bugs** | GBNF ensures the model calls tools with structurally valid parameters, but if a first-party tool implementation has a bug, that bug is outside tzro's grammar constraint scope. |

---

## 8. Responsible Disclosure

If you discover a security vulnerability in tzro, we want to hear about it. Please report vulnerabilities responsibly:

- **Email**: security@tzro.dev
- **Scope**: Vulnerabilities in the tzro engine, daemon, MCP server, container isolation, or grammar constraint enforcement.
- **Response**: We aim to acknowledge reports within 48 hours and provide a fix timeline within 7 days.
- **Recognition**: We credit researchers who responsibly disclose vulnerabilities (with their permission) in our release notes.

Please do **not** file security vulnerabilities as public GitHub issues.

---

## Summary

tzro's security architecture is built on structural enforcement, not behavioral promises:

```
┌──────────────────────────────────────────────────────────────┐
│                     HOST OPERATING SYSTEM                    │
│  ┌────────────────────────────────────────────────────────┐  │
│  │              tzrod (127.0.0.1 only)                    │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │          Local Inference Engine                   │  │  │
│  │  │  ┌────────────────────────────────────────────┐  │  │  │
│  │  │  │  GBNF Grammar Constraint Layer             │  │  │  │
│  │  │  │  • Logit masking: P(invalid token) = 0     │  │  │  │
│  │  │  │  • Tool name enumeration                   │  │  │  │
│  │  │  │  • Schema-bound structured output          │  │  │  │
│  │  │  └────────────────────────────────────────────┘  │  │  │
│  │  │  ┌────────────────────────────────────────────┐  │  │  │
│  │  │  │  Semantic Validator                        │  │  │  │
│  │  │  │  • Type coercion + retry loops             │  │  │  │
│  │  │  │  • Default imputation                      │  │  │  │
│  │  │  │  • Deterministic JSON conformance          │  │  │  │
│  │  │  └────────────────────────────────────────────┘  │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  │  ┌──────────────────┐  ┌──────────────────────────┐   │  │
│  │  │  WASM Sandbox    │  │  Container Sandbox       │   │  │
│  │  │  • No FS access  │  │  • --network=none        │   │  │
│  │  │  • No network    │  │  • --read-only           │   │  │
│  │  │  • No env vars   │  │  • --rm (ephemeral)      │   │  │
│  │  │  • Timeout-bound │  │  • Resource-capped       │   │  │
│  │  └──────────────────┘  └──────────────────────────┘   │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │  Runtime Safety                                  │  │  │
│  │  │  • Circuit breakers  • Tool allow-lists          │  │  │
│  │  │  • Proactivity gates • Mutation budgets          │  │  │
│  │  │  • Delegated secrets • Durable audit logging     │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

Every layer is independently verifiable. Every constraint is structural, not behavioral. And when you want to verify our claims, the tools to do so ship with the product.
