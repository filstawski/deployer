---
name: docs-feature-planning-spec
description: >-
  A structured template for planning a non-trivial feature before writing
  any code — goal and design principles, explicit in/deferred/out-of-scope
  boundaries, a recap of the relevant current architecture, a table of
  decisions already locked in, the concrete design (data model, API
  surface, abstraction layer), a numbered phased rollout, and separate
  Resolved-decisions vs. Still-open-questions sections. Use when about to
  design a feature big enough that "just start coding" would mean
  re-deciding the same tradeoffs mid-implementation, or when asked to
  write a design doc / RFC / planning doc before building something.
metadata:
  type: practice
  category: documentation
---

## Rule

For a feature substantial enough to need a plan before code, write it as a
single doc with these sections, in this order:

```markdown
# <Feature Name> — Concept

> Status: Draft / Planning · Owner: <you> · Last updated: <date>
>
> One or two sentences on what this plans and its current state —
> "no code has been written yet" or "phase 1 is live, this covers phase 2."

## 1. Goal

What this feature does and why, in a few sentences. Name who it's for
and what changes for them.

### Design principles

The handful of qualitative constraints every later decision gets checked
against — e.g. "minimal user configuration," "non-destructive to the
existing flow," "fail safe, not silent." Write these *before* the detailed
design so later sections can be checked against them instead of each
inventing its own taste.

## 2. Scope

### In scope (this phase)
### Deferred (later phases)
### Out of scope

Three explicit buckets, not one vague "future work" list. "Deferred"
means planned but not now (state why); "out of scope" means not part of
this feature's problem at all, even later.

## 3. Current architecture (recap)

A factual recap of the parts of the existing system this feature touches
— just enough that someone reading only this doc doesn't need the
codebase open to follow the rest. Not a design decision itself.

## 4. Locked decisions

| # | Decision |
|---|----------|
| 1 | ... |

Decisions already made (in discussion, in a review, in your own head)
that later sections build on without re-litigating. If a later section's
design conflicts with a locked decision, the doc has a bug — fix one or
the other before continuing.

## 5+. The actual design

Whatever sections the feature needs — data model changes, new API
surface, an abstraction layer, security considerations. Each non-obvious
call gets a `> **Decision (...):**` callout with a one-line reason, right
where the decision is made, not deferred to a decisions-log section.

## N. Phased rollout

1. **Phase 0 — ...** No behavior change; foundational only.
2. **Phase 1 — ...**
   ...

Numbered, and each phase should be independently shippable — a phase
that only makes sense bundled with the next one is really one phase, not
two.

## N+1. Resolved decisions (this round)

Short recap of what got decided *in this planning pass* — useful for
anyone catching up without reading the whole doc.

## N+2. Still-open questions

Numbered, each with your own leaning stated if you have one — "(Daily
cron recommended.)" — so a reader can fast-path agreeing with you instead
of starting from zero on every question.

## N+3. Further considerations worth deciding

Things that came up while writing the doc that don't have an answer yet
and weren't part of the original ask — flagged rather than silently
assumed away. It's fine for this section to outlive the doc's "planning"
status; revisit it once the feature ships.
```

## Why

A feature big enough to need a plan is exactly the kind where skipping the
plan means re-deciding the same tradeoff three times during implementation,
each time slightly differently, because nothing wrote down the reasoning
the first time. Splitting scope into in / deferred / out (rather than one
scope list) matters because "deferred" and "out of scope" get treated very
differently later — a deferred item shapes the current design (the
abstraction has to leave room for it); an out-of-scope item doesn't. The
Locked-decisions table exists so later sections can *build on* a decision
instead of re-arguing it every time it's relevant — decide once, reference
after. Separating Resolved from Still-open (rather than one flat "open
questions" list that slowly gets crossed out) keeps the doc honest about
what's actually settled at any point someone re-reads it.

## Exceptions

- A small feature — a few hours of work, no real design tradeoffs — doesn't
  need this. The template earns its cost only when skipping it would mean
  re-deciding something mid-implementation.
- Not every section is needed every time. A feature with no data model
  changes skips that section; the shape (goal → scope → current state →
  decisions → design → rollout → open questions) is the reusable part, not
  every literal heading.
- If the plan is small enough to fully resolve in one sitting with no
  remaining open questions, the "Still-open questions" section can be
  genuinely empty — don't pad it to look thorough.

## See also

- [docs-agent-handoff-spec](../docs-agent-handoff-spec/SKILL.md) — the companion doc for handing the *implementation* of this plan to someone else once it's built (or partially built).
- [docs-roadmap-notes](../docs-roadmap-notes/SKILL.md) — the smaller-scale version of "Deferred" scope items and "Still-open questions" for a single design decision, not a whole feature.
