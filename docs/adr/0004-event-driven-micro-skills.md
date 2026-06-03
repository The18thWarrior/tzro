# ADR-0004: Event-Driven Procedural Micro-Skills Pipeline

## Context & Problem Statement

When agentic engines are requested to perform highly specialized data integrations (such as executing Salesforce SOQL queries or configuring complex Jira filters), zero-shot LLM planning frequently falls victim to API syntax hallucinations.

Standard RAG (Retrieval-Augmented Generation) systems can provide raw documentation, but struggle to inject step-by-step _procedural workflows_ that match the exact sequence of events required for specific enterprise tasks.

Furthermore, injecting entire libraries of full-text procedural documents directly into the context window of local worker LLMs causes severe context bloat, slowing down inference speeds and exceeding RAM limits.

## Proposed Decision

We choose to implement an autonomous, event-driven **Procedural Micro-Skills Pipeline**.

1. **Successful Trajectory Extraction:** When a compiled Go execution graph completes successfully, a background synthesizer evaluates the execution trajectory.
2. **Double-Gate Filter Protection:**
   - **Complexity Gate:** Trajectories containing fewer than **3 steps** are skipped as "too trivial to represent a reusable system SOP".
   - **Semantic Deduplication Gate:** The system generates a vector embedding of the trigger description. If the semantic similarity score against an already saved skill exceeds **0.8**, the trigger is skipped to prevent duplicate database bloat.
3. **Automated SOP Synthesis:** The synthesizer processes the trajectory and compiles a standardized, highly compact Markdown Standard Operating Procedure (SOP).
4. **Dual-Inject Optimization:**
   - **Cloud Planner Index-Only Injection:** The Cloud Planner receives only a lightweight JSON array mapping Trigger IDs to Trigger Descriptions. It selects matching skill IDs and adds them to `suggestedSkillIds`.
   - **Local Step Executor Full-Text Injection:** The Local Step Executor receives _only_ the full-text Markdown SOPs matching the suggested IDs.

---

## Technical Specifications

### 1. Database Schema (`synthesized_skills`)

SOP procedural definitions and semantic triggering signatures are stored in a dedicated SQLite table:

```sql
CREATE TABLE synthesized_skills (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    trigger_description TEXT NOT NULL, -- Semantic search vector target
    sop_content         TEXT NOT NULL, -- Compact Markdown SOP
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
```

---

### 2. Dual-Inject Memory Control Architecture

```
                        User Goal / Input Prompt
                                  │
                                  ▼
┌───────────────────────────────────────────────────────────────────┐
│                       Cloud Planner v2                            │
│                                                                   │
│   Injected Context (Compact Trigger Index):                       │
│   - Skill 42: "Bulk Salesforce contact update from sheet"         │
│   - Skill 88: "Jira issue status sync from external slack web"    │
└───────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼ Output: { "suggestedSkillIds": ["42"] }
┌───────────────────────────────────────────────────────────────────┐
│                      Local Step Executor                          │
│                                                                   │
│   Injected Context (Full-Text SOP for Skill 42 only):              │
│   # Step 1: Call `salesforce_query` ...                           │
│   # Step 2: Flatten JSON fields to TSV ...                        │
└───────────────────────────────────────────────────────────────────┘
```

---

### 3. Compiled SOP Output Format Example

```markdown
# Trigger

When the user requests a bulk merge of duplicate Salesforce accounts using email addresses.

# Context

Salesforce query outputs must exclude deleted contacts. Ensure to query the Account ID alongside the email to establish relational mapping before executing the merge API.

# Steps

1. Call `salesforce_query` with query: "SELECT Id, Email, AccountId FROM Contact WHERE Email != NULL AND IsDeleted = false"
2. Group the contact maps inside memory by Email key.
3. For every contact list having size > 1, execute `salesforce_merge_contacts` using the primary Account ID.
```

---

## Consequences

- **Pros:**
  - **Zero-Hallucination API Syntax:** Complex sequences are locked into structured, verified steps, ensuring local workers execute APIs flawlessly.
  - **Drastic Context Savings:** The Dual-Inject architecture protects the local model context window from bloat, saving up to 90% of memory during the planning phase.
  - **Autonomous Optimization:** The system grows smarter over time, naturally building a tailored Standard Operating Procedure library for the enterprise.
- **Cons:**
  - **Synthesizer Latency:** Trajectory analysis and SOP creation add a small processing overhead (~1-2 seconds) strictly _after_ successful task execution.
  - **Database Drift:** If third-party APIs undergo breaking changes, cached SOPs must be forcefully invalidated or re-synthesized to prevent execution errors.
