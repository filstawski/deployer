---
name: docs-roadmap-notes
description: >-
  How to write down a known-but-deferred improvement so it's a reviewable
  decision instead of either silent debt or a premature build — state the
  natural next step, why it's deferred right now, and what would trigger
  actually doing it, right next to the code/doc it concerns. Use when
  choosing a deliberately simpler design over a "better" one because the
  better one isn't justified yet, when documenting a known limitation, or
  when capturing a raw idea that isn't ready to be a full roadmap note yet.
metadata:
  type: practice
  category: documentation
---

## Rule

When a design deliberately stops short of the "obviously better" version,
write down the gap as a roadmap note with three parts — not a vague TODO,
not silence:

```markdown
## Roadmap: <the deferred improvement>

The natural next step is <what it would look like>. Deferred because:

- <concrete reason 1 — a real cost, not "not done yet">
- <concrete reason 2>

When <the trigger condition>, <what changes and what doesn't need to>.
```

A real example:

```markdown
## Roadmap: bulk event

The natural next step is a single `${name}:bulk-update` event carrying
`Array<{ id, ...diff }>`. Deferred because:

- Requires a new rooms map entry per resource.
- Requires a client-side handler.
- Needs a decision on room targeting: union of per-row rooms (correct but
  N × rooms internally) vs. a coarser bulk room (fires once but
  subscribers to per-user rooms miss updates).

When designed, the base's `updateMany` grows a bulk emit block; call sites
don't need changes.
```

Rules of thumb:

- **The trigger condition is the part that makes this worth writing at
  all.** "Deferred because it's complex" without a trigger is just a TODO
  with extra words. "Revisit when N crosses this threshold" or "revisit
  when this second thing lands" gives a future reader (including future
  you) a concrete signal for when to stop deferring.
- **State what does *not* need to change**, when you know it. "Call sites
  don't need changes" tells the reader the deferred work is additive, not
  a breaking migration waiting to happen — that's often the detail that
  makes deferring feel safe rather than risky.
- **Put it where the decision lives** — next to the code or in the same
  doc as the simpler version it's deferring past — not in a separate
  "future ideas" doc disconnected from the thing it's about. A roadmap
  note only helps the next reader if they find it while looking at the
  relevant code.
- **A raw, unstructured idea doesn't need this shape yet.** A scratch file
  of half-formed ideas (optimistic updates, "should dates be typed or
  strings," open questions with no leaning) is a legitimate *earlier*
  stage — brainstorming before you've thought through the trigger
  condition. Graduate an idea from the scratch file into a proper roadmap
  note (with reasons and a trigger) once it's actually being deferred on
  purpose, not just unbuilt.

## Why

An unwritten limitation is invisible until someone hits it, at which point
it looks like a bug instead of a decision — and the person who hits it has
no way to tell "this was a deliberate tradeoff" from "nobody thought of
this." A bare TODO comment records that *something* should happen
eventually but not why it hasn't, which means every reader has to
re-derive the reasoning (or worse, assumes there wasn't any) before they
can decide whether to just go build it now. Writing the reasons and the
trigger down turns "known limitation" into a decision that can be reviewed,
agreed with, or explicitly overturned — instead of technical debt nobody
remembers agreeing to.

## Exceptions

- Don't write a full roadmap note for something that's genuinely not worth
  doing later either — that's not deferred work, it's a decision not to do
  something, and belongs in a design doc's rationale, not a roadmap
  section implying "eventually."
- A trigger condition that's really just "when we have time" isn't a
  trigger — if there's no concrete signal, say so honestly ("no specific
  trigger; revisit opportunistically") rather than inventing a fake one.

## See also

- [docs-migration-status-tracking](../docs-migration-status-tracking/SKILL.md) — the checklist-table shape for tracking many deferred items at once, versus one inline note for a single deferred decision.
- [same-import-upgrade-path](../../same-import-upgrade-path/SKILL.md) — a specific, recurring flavor of deferred work (a minimal implementation deferring a known "real" backing) worth recognizing on sight.
