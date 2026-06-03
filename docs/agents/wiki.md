# Local Wiki (LLM Wiki Style)

This document governs how AI coding agents maintain the persistent, compounding knowledge base under `docs/wiki/`. It is inspired by the LLM Wiki pattern, transforming transient conversations and engineering artifacts into a structured, interlinked wiki.

## Directory Structure

All wiki files live under `docs/wiki/` and must adhere to this structure:

```
docs/wiki/
├── index.md           # Content-oriented map/catalog (updated on every change)
├── log.md             # Chronological append-only record of all actions
├── features/          # Summaries of features, PRDs, and implementation plans
├── bugs/              # Bug diagnoses, reproduction steps, and post-mortems
├── architecture/      # System designs, core concepts, diagrams, and decisions
└── sources/           # Summaries and notes from ingested third-party documents/APIs
```

---

## Core Operations

### 1. Ingest

When a new raw source (notes, API schema, third-party doc, log) is processed:

1. Read the source, extract key findings, and write a summary file in `docs/wiki/sources/<source-slug>.md`.
2. Update relevant entity/concept pages or create new ones under `docs/wiki/architecture/` if the source introduces new terms.
3. Update `docs/wiki/index.md` under the appropriate category.
4. Append an entry to `docs/wiki/log.md` in this format: `## [YYYY-MM-DD HH:MM] ingest | <source-title>`.

### 2. Query

When a user asks a complex question about the repository or domain:

1. First read `docs/wiki/index.md` to find relevant files.
2. Read the identified files to synthesize the answer.
3. If the generated synthesis or comparison is of high long-term value, **file it back into the wiki** under the appropriate namespace, link it in `index.md`, and log it in `log.md`.

### 3. Lint

Periodically, run a health check over the wiki:

1. Scan for broken markdown links or orphan pages (pages with no inbound links).
2. Scan for logical contradictions (e.g., a feature page stating behavior A while an ADR or architecture page states behavior B).
3. Scan for stale claims superseded by newer logs or sources.
4. Propose updates or fixes to resolve the conflicts.

---

## Document Templates

### 1. Index Template (`docs/wiki/index.md`)

```markdown
# Local Wiki Index

Welcome to the persistent repository knowledge base.

- **Chronological Log**: [Log](log.md)

---

## Features & PRDs

_Map of system features, product requirements, and specs._

- [Feature Name](features/feature-slug.md) - One line summary. (Sources: N | Last Updated: YYYY-MM-DD)

## Bugs & Post-Mortems

_Analyses of critical bugs, diagnostic loops, and prevention measures._

- [Bug Name](bugs/bug-slug.md) - One line summary of bug and fix. (Verified: YYYY-MM-DD)

## Architecture & Concepts

_Glossary terms, data models, ADR summaries, and architectural diagrams._

- [Concept Name](architecture/concept-slug.md) - One line summary of the concept. (Decided in: ADR-NN)

## Ingested Sources

_Immutable third-party references, notes, and raw inputs._

- [Source Title](sources/source-slug.md) - One line summary of the ingested material.
```

### 2. Log Template (`docs/wiki/log.md`)

```markdown
# Wiki Operation Log

Chronological append-only record of wiki operations and major agent engineering activities.

---

## [YYYY-MM-DD HH:MM] <operation_type> | <short_title>

- **Activity**: Description of what was completed (e.g., implemented vertical slice X, triaged bug Y).
- **Files Touched**:
  - [Page Name](path/to/page.md)
  - [Code File](path/to/code.go)
```

### 3. Feature Summary Template (`docs/wiki/features/<feature-slug>.md`)

```markdown
# Feature: [Feature Name]

## Problem & Solution

- **Context**: Why this feature exists.
- **Value**: What problem it solves.

## Technical Design Summary

- **Core Modules**: Modules built or modified.
- **Data Models / APIs**: Schema definitions or API contracts.

## References

- **PRD**: [.scratch/feature-slug/PRD.md](../../.scratch/feature-slug/PRD.md)
- **Log Entry**: [Log Link](../log.md#yyyy-mm-dd-hhmm-ingest--prd-feature-name)
```

### 4. Bug Post-Mortem Template (`docs/wiki/bugs/<bug-slug>.md`)

```markdown
# Bug Post-Mortem: [Bug Name]

## Symptom

- **Description**: What went wrong and how it was reported.
- **Reproduction**: Link to the reproduction loop/test created.

## Diagnosis

- **Hypotheses**: What was tested.
- **Root Cause**: The actual cause of the bug.

## Resolution

- **Fix**: Description of code changes.
- **Regression Prevention**: How we locked it down (regression test seam).

## Long-term Prevention

- Architectural changes needed to prevent this class of bug from reoccurring.
```
