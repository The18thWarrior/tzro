---
name: ideate-and-drill
description: Combined ideation and implementation deep-dive. Brainstorms the idea creatively, then stress-tests every design decision against the domain model, codebase, and existing documentation. Updates CONTEXT.md and ADRs inline. Use when user wants to both explore an idea AND drill into the implementation, says "ideate and drill", "brainstorm and grill", "design deep-dive", or wants creative exploration grounded in the real codebase.
---

# Ideate & Drill

Two-phase collaborative design: **Phase 1** explores the idea creatively (brainstorming). **Phase 2** stress-tests every decision against the domain model and codebase (drilling). Domain docs are updated inline as decisions crystallise.

<HARD-GATE>
Do NOT invoke any implementation skill, write any code, scaffold any project, or take any implementation action until you have completed BOTH phases, presented a spec, and the user has approved it.
</HARD-GATE>

## Checklist

Complete in order:

### Phase 1 — Ideate

1. **Explore project context** — check files, docs, recent commits, CONTEXT.md, ADRs
2. **Offer visual companion** (if visual questions ahead) — own message, nothing else
3. **Ask clarifying questions** — one at a time, understand purpose/constraints/success criteria
4. **Propose 2-3 approaches** — with trade-offs and your recommendation
5. **Present design sections** — scaled to complexity, get user approval after each section

### Phase 2 — Drill

6. **Announce phase transition** — "Design looks good. Now let's stress-test it."
7. **Challenge every decision** — walk each branch of the design tree, one question at a time, with your recommended answer
8. **Cross-reference with code** — verify assumptions against the actual codebase; surface contradictions
9. **Sharpen terminology** — flag vague/overloaded terms, propose canonical names, update CONTEXT.md inline (see [CONTEXT-FORMAT.md](../grill-with-docs/CONTEXT-FORMAT.md))
10. **Probe edge cases** — invent concrete scenarios that test boundaries between concepts
11. **Offer ADRs sparingly** — only when hard-to-reverse, surprising, and the result of a real trade-off (see [ADR-FORMAT.md](../grill-with-docs/ADR-FORMAT.md))

### Finalise

12. **Write design spec** — save to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`
13. **Spec self-review** — scan for placeholders, contradictions, ambiguity, scope issues; fix inline
14. **User reviews spec** — wait for explicit approval
15. **Transition to implementation** — invoke writing-plans skill (the ONLY valid next skill)

## Phase 1 — Ideate (Detail)

**Understanding the idea:**
- Check current project state (files, docs, recent commits)
- If request spans multiple independent subsystems, flag it and decompose before deep-diving
- Ask questions one at a time; prefer multiple-choice when possible
- Focus on: purpose, constraints, success criteria

**Exploring approaches:**
- Propose 2-3 approaches with trade-offs
- Lead with your recommendation and reasoning

**Presenting the design:**
- Present section by section, scaled to complexity
- Ask after each section whether it looks right
- Cover: architecture, components, data flow, error handling, testing
- Design for isolation: each unit has one clear purpose, well-defined interfaces, independently testable

## Phase 2 — Drill (Detail)

The drill phase is adversarial by design. Your job is to find weaknesses.

**Challenge against the glossary:**
When a term conflicts with existing language in `CONTEXT.md`, call it out immediately. _"Your glossary defines 'cancellation' as X, but you seem to mean Y — which is it?"_

**Sharpen fuzzy language:**
When vague or overloaded terms appear, propose a precise canonical term. _"You're saying 'account' — do you mean the Customer or the User?"_

**Discuss concrete scenarios:**
Stress-test domain relationships with specific scenarios that probe edge cases and force precision about boundaries.

**Cross-reference with code:**
When the user states how something works, check whether the code agrees. Surface contradictions: _"Your code cancels entire Orders, but you just said partial cancellation is possible — which is right?"_

**Update CONTEXT.md inline:**
When a term is resolved, update `CONTEXT.md` immediately — don't batch. CONTEXT.md is a glossary only, no implementation details.

**Update Local Wiki:**
When terms are resolved, CONTEXT.md is updated, or new ADRs are created:
- Update glossary/concept pages under `docs/wiki/architecture/`
- Map new ADRs into `docs/wiki/index.md`
- Log session outcomes in `docs/wiki/log.md`

## Key Principles

- **One question at a time** — never overwhelm
- **Multiple choice preferred** — easier to answer when possible
- **YAGNI ruthlessly** — strip unnecessary features from all designs
- **Always propose alternatives** — 2-3 approaches before settling
- **Incremental validation** — present, get approval, move on
- **Adversarial drilling** — find weaknesses before implementation does
- **Domain docs are living** — update CONTEXT.md and ADRs as decisions happen, not after

## Visual Companion

When visual questions are anticipated, offer once for consent (own message, nothing else):

> "Some of what we're working on might be easier to explain if I can show it to you in a web browser. I can put together mockups, diagrams, comparisons, and other visuals as we go. This feature is still new and can be token-intensive. Want to try it?"

Per-question decision: **would the user understand this better by seeing it than reading it?**
- Browser: mockups, wireframes, layout comparisons, architecture diagrams
- Terminal: requirements questions, conceptual choices, tradeoff lists, scope decisions

If accepted, read the detailed guide: `../brainstorming/visual-companion.md`
