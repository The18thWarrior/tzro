# Adoption readiness

Status: in progress. Last updated: September 28, 2026.

The [Adoption readiness map](../../../.scratch/adoption-readiness/MAP.md) tracks the shift to CLI-first documentation and optional proxy use.

The [README structure decisions](../../../.scratch/adoption-readiness/issues/01-readme-structure-decisions.md) define the layout. The [README rewrite](../../../.scratch/adoption-readiness/issues/02-readme-rewrite.md) records its application, evidence corrections, and checks.

The [README](../../../README.md) now begins with local discovery and compaction. It separates agent hooks from proxy configuration. [ADR-0096](../../adr/0096-migration-to-jev-style-and-libllama-decision-engine.md) replaces the Laya decision runtime with the JEV-style runtime in `pkg/decision`. GLiNER remains optional.

The [CONTEXT.md vocabulary update](../../../.scratch/adoption-readiness/issues/06-context-glossary-update.md) is resolved. Token Shield now stands as the umbrella concept, explicitly divided into the zero-trust CLI Toolkit and the opt-in Proxy Shield. System 1 Decision Daemon reflects the native JEV-style runtime, Laya and proxy-first assumptions are marked deprecated, and the Context Pack Assembler (`tzro context`) has its own canonical definition.

The [First-install experience](../../../.scratch/adoption-readiness/issues/09-first-install-experience.md) decision is closed. Standard automatically configures supported integrations for six detected clients: Antigravity, Claude Code, Hermes, GitHub Copilot, Pi-Coder, and Codex. Native client approval requirements remain visible in setup results.

[Standard installer implementation](../../../.scratch/adoption-readiness/issues/13-standard-installer-implementation.md) is complete locally. The installer checks release checksums, configures native integrations, preserves unrelated configuration, and reports partial failures. Isolated installation tests and builds for all three release targets pass. The [installation guide](../../installation.md) records supported capabilities and remaining limitations.

[Published installation and client verification](../../../.scratch/adoption-readiness/issues/14-published-installation-and-client-verification.md) is closed. Standalone release downloads via `https://get.tzro.ai | sh` are verified and active on S3 (v3.1.0 release), with SHA-256 integrity and automated client profile configurations verified. The quickstart has been updated with the verified one-liner installer.

The [CI pipeline](../../../.scratch/adoption-readiness/issues/03-ci-pipeline.md) and [Hosted CI verification](../../../.scratch/adoption-readiness/issues/10-hosted-ci-verification.md) are closed. Commit `8e467d1` on branch `v3.1` passed all hosted Ubuntu checks in GitHub Actions (run 36484736347) with race detection across 22 packages and uninstrumented latency limits passing. The README workflow badge is verified green.

The [Benchmark categories and evidence](../../../.scratch/adoption-readiness/issues/11-benchmark-categories-and-evidence.md) decision is closed. Public workflow results will compare the agent before installation, Tzro Standard after default installation, and Tzro Full after optional setup. Full includes experimental System 1, the JEV-style decision runtime, and GLiNER capabilities. Component and proxy measurements remain supporting diagnostics.

[Benchmark installation profiles](../../../.scratch/adoption-readiness/issues/12-benchmark-installation-profiles.md) is complete locally. The [native Pi recipe](../../benchmark-workflows.md) checks installation and runtime readiness before provider calls. Reports separate readiness from observed task use and preserve failures, usage, costs, and timing.

The [JEV worker](../../../cmd/jev-score/README.md) now performs real libllama inference. Its typed results and verdict margins pass real-model and reference-parity checks. Native Pi integration passed against a loopback response fixture, including installed hooks, Full proxy routing, and real JEV use. The extractor remained a fixture in that smoke test.

[Publish benchmark artifacts](../../../.scratch/adoption-readiness/issues/04-publish-benchmark-artifacts.md) is now unblocked and is the final remaining task on the adoption-readiness map.

The [Contributing guide](../../../.scratch/adoption-readiness/issues/05-contributing-guide.md) is resolved. The root [CONTRIBUTING.md](../../../CONTRIBUTING.md) defines onboarding pathways, the 21-package subsystem map, ~1:1 test ratio expectations, build/test workflows, and high-impact areas to reduce single-author bus factor.

The [AGENTS.md update](../../../.scratch/adoption-readiness/issues/07-agents-md-update.md) is resolved. The repository's agent instructions lead with the CLI-first discovery and compaction workflows, split the agent CLI Reference table into dedicated CLI Toolkit and Proxy Shield sections, and synchronize the Domain Language Glossary with `CONTEXT.md`—mirroring the umbrella Token Shield concept, explicit CLI Toolkit and Proxy Shield definitions, AST declaration spans, and the native JEV-style / libllama scoring daemon.

The [Tier strategy](../../../.scratch/adoption-readiness/issues/08-tier-strategy.md) decision is closed. The core engine remains a single unified binary distributed under permissive Apache 2.0 without user caps, telemetry, or artificial code gates. Tiers represent an adoption and positioning progression (CLI Toolkit as default, Proxy Shield as opt-in). Future enterprise fleet governance and centralized gateway features will be isolated to an independent commercial binary, preserving zero-friction adoption for individual developers.
