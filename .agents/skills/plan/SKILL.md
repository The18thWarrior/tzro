---
name: plan
description: Take a spec or design document and produce a detailed, TDD-aligned implementation plan with vertical tracer-bullet slices. Use when user says "plan this", "write an implementation plan", "plan the implementation", wants to turn a spec into actionable work, or when transitioning from brainstorming to implementation.
---

# Plan

Turn a spec into a detailed implementation plan using TDD best practices and vertical slices.

## Input

A spec document — either passed as a file path, an issue reference, or already in conversation context from a brainstorming session.

## Process

### 1. Read the spec

Load and internalize the full spec. If a path was given, read it. If coming from brainstorming, use the conversation context.

### 2. Explore affected code

Understand the codebase areas the spec touches:

- [ ] Read `CONTEXT.md` for domain glossary — plan vocabulary MUST match it
- [ ] Check `docs/adr/` for relevant architectural decisions in the affected area
- [ ] Explore the packages/modules the spec will modify or create
- [ ] Identify existing patterns, conventions, and test infrastructure

### 3. Decompose into vertical slices

Break the spec into **tracer-bullet slices** — each slice cuts through ALL layers end-to-end. See [slicing.md](slicing.md) for rules and examples.

<HARD-GATE>
DO NOT produce horizontal slices (all types first, then all logic, then all tests). Every slice must be a thin, independently-verifiable path through the full stack.
</HARD-GATE>

Order slices so each builds on the last:

1. **Tracer bullet** — the thinnest possible end-to-end path proving the integration works
2. **Core behavior slices** — one per key behavior, ordered by dependency
3. **Edge cases and hardening** — error paths, validation, limits
4. **Polish** — logging, metrics, documentation

### 4. Design interfaces first

For each slice, define the public interface BEFORE describing implementation:

- What types/functions does this introduce or modify?
- What does the caller see? What's hidden?
- Apply [deep module](deep-modules.md) thinking — small interface, deep implementation
- Apply [interface design](interface-design.md) — injectable dependencies, return values over side effects

### 5. Write the TDD loop for each slice

Each slice gets a RED→GREEN micro-plan. See [tdd-loop.md](tdd-loop.md) for the template.

```
Slice N: [name]
  Interface: [what the public API looks like]
  RED:   [what test to write — behavior, not implementation]
  GREEN: [what minimal code to write]
  Verify: [how to confirm it works]
```

### 6. Present and iterate

Present the full plan to the user. Ask:

- Does the slice ordering make sense?
- Are the interfaces right?
- Are we testing the right behaviors?
- Anything missing or over-engineered?

Iterate until approved.

### 7. Publish

Save the approved plan to `implementation_plan.md` artifact with `request_feedback = true`. The plan is now ready for execution via the `tdd` skill.

## Checklist

After drafting, verify:

- [ ] Every slice is vertical (end-to-end), not horizontal (one layer)
- [ ] Slice 1 is a tracer bullet — thinnest possible proof of integration
- [ ] Interfaces defined before implementation details
- [ ] Test descriptions focus on behavior, not implementation
- [ ] Domain vocabulary matches CONTEXT.md glossary
- [ ] No ADR violations in affected areas
- [ ] No speculative features (YAGNI)
- [ ] Each slice is independently verifiable
