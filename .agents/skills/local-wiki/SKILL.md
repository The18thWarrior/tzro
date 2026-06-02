---
name: local-wiki
description: Maintain, ingest into, query, and lint the persistent local wiki using the LLM Wiki style. Use when managing knowledge, ingesting sources, querying history, or running wiki health checks.
---

# Local Wiki (LLM Wiki)

## Quick Start

The wiki resides at `docs/wiki/`. Whenever a source is ingested, or an engineering output (e.g., PRD, post-mortem, design) is created/modified, update the wiki files using this skill.

---

## Workflows

### 1. Ingest Source

Use this workflow to process a new source (articles, documents, requirements, logs):

- [ ] **Read the Source**: Extract key technical decisions, definitions, APIs, and constraints.
- [ ] **Create/Update Wiki Page**:
  - For third-party papers, specs, or documents: Create `docs/wiki/sources/<source-slug>.md`.
  - For feature PRDs: Create `docs/wiki/features/<feature-slug>.md`.
  - For bug diagnostics: Create `docs/wiki/bugs/<bug-slug>.md`.
  - For concepts/architecture: Create `docs/wiki/architecture/<concept-slug>.md`.
- [ ] **Integrate & Cross-Reference**:
  - Update any existing pages that this source touches, enhances, or contradicts.
  - Link new pages to relevant conceptual or design pages.
- [ ] **Update Index**: Add the new page to `docs/wiki/index.md` under the correct category with a link and one-line summary.
- [ ] **Log Action**: Append an entry to `docs/wiki/log.md` detailing what was ingested and which files were touched.

### 2. Query Wiki

Use this workflow to answer questions about the workspace or domain:

- [ ] **Scan Index**: Read `docs/wiki/index.md` to find relevant namespaces and page paths.
- [ ] **Extract Knowledge**: Read the target wiki pages to gather context.
- [ ] **Synthesize & Cite**: Generate the answer, referencing the wiki pages as citations.
- [ ] **File Back (Optional)**: If the synthesized answer is of high long-term value, create a new topic page under `docs/wiki/architecture/` or `docs/wiki/features/`, link it in `index.md`, and log it in `log.md`.

### 3. Lint Wiki

Use this workflow periodically to maintain wiki health:

- [ ] **Check Links**: Scan all markdown files under `docs/wiki/` for broken links or missing files.
- [ ] **Identify Orphans**: Locate any pages in the directory that are not linked in `docs/wiki/index.md` or any other page.
- [ ] **Resolve Contradictions**: Identify statements in newer log entries or source summaries that contradict older architectural summaries or feature pages. Discuss with the user or resolve by updating pages.
- [ ] **Update Stale Info**: Ensure pages reflect the latest engineering decisions (ADRs).
