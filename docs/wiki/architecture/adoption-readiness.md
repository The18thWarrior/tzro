# Adoption readiness

Status: in progress. Last updated: September 28, 2026.

The [Adoption readiness map](../../../.scratch/adoption-readiness/MAP.md) tracks the shift to CLI-first documentation and optional proxy use.

The [README structure decisions](../../../.scratch/adoption-readiness/issues/01-readme-structure-decisions.md) define the layout. The [README rewrite](../../../.scratch/adoption-readiness/issues/02-readme-rewrite.md) records its application, evidence corrections, and checks.

The [README](../../../README.md) now begins with local discovery and compaction. It separates agent hooks from proxy configuration. The existing [System 1 architecture decision](../../adr/0095-system1-graph-calls-and-encoder-decision-engine.md) still governs optional model runtimes.

The glossary still needs human discussion in [CONTEXT.md vocabulary update](../../../.scratch/adoption-readiness/issues/06-context-glossary-update.md). These documentation headings do not settle that ticket.

The [First-install experience](../../../.scratch/adoption-readiness/issues/09-first-install-experience.md) decision is closed. Standard automatically configures supported integrations for six detected clients: Antigravity, Claude Code, Hermes, GitHub Copilot, Pi-Coder, and Codex. Native client approval requirements remain visible in setup results.

[Standard installer implementation](../../../.scratch/adoption-readiness/issues/13-standard-installer-implementation.md) owns the download path, configuration adapters, and installation checks. The current quickstart still uses source installation. The intended 30-second installation remains unverified.

The [CI pipeline](../../../.scratch/adoption-readiness/issues/03-ci-pipeline.md) is implemented locally. The race-enabled suite and separate performance checks passed. Its [preflight post-mortem](../bugs/ci-race-and-impact-preflight.md) records the required corrections. [Hosted CI verification](../../../.scratch/adoption-readiness/issues/10-hosted-ci-verification.md) owns the first Ubuntu run and badge check.

The [Benchmark categories and evidence](../../../.scratch/adoption-readiness/issues/11-benchmark-categories-and-evidence.md) decision is closed. Public workflow results will compare the agent before installation, Tzro Standard after default installation, and Tzro Full after optional setup. Full includes experimental System 1, Laya, and GLiNER capabilities. Component and proxy measurements remain supporting diagnostics.

[Benchmark installation profiles](../../../.scratch/adoption-readiness/issues/12-benchmark-installation-profiles.md) owns setup recipes, installed-agent fidelity, runtime readiness evidence, and complete task timing. It depends on Standard installer implementation. [Publish benchmark artifacts](../../../.scratch/adoption-readiness/issues/04-publish-benchmark-artifacts.md) waits for that harness. No new benchmark results are claimed.
