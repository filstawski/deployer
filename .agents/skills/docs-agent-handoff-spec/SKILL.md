---
name: docs-agent-handoff-spec
description: >-
  A self-contained handoff document for another agent, team, or future-you
  to build against a system that's only partially finished — states
  exactly what's live versus stubbed right now, gives hard constraints as
  an explicit table, documents the full request/response contract inline
  so the reader never needs the other side's source, and calls out
  anything that will change shape later. Use when one part of a system
  (backend, API) is ahead of another (frontend, consumer) and someone else
  needs to build the other half, or when handing off in-progress work to a
  different agent/session/person who won't have the context you have.
metadata:
  type: practice
  category: documentation
---

## Rule

Write the handoff as its own document — not a comment thread, not a chat
message — with these parts:

```markdown
# <Feature> — <Audience> Handoff

> Audience: <who this is for, specifically — "the dashboard frontend
> agent," not just "frontend">. This is a self-contained spec — you do not
> need the <other side>'s source; everything you need is here.
>
> Status: <what's actually live> **is live** under <namespace/location>.
> <What's NOT live yet> — see §N "Current status" before you set user
> expectations.

## 1. What you're building

Plain description of the deliverable, from the reader's-eyes view, not
the builder's. Include what the end user can *do*, not just what exists.

### Hard constraints (enforce in <reader's layer>)

| Constraint | Behavior |
|---|---|
| <a real limit that must be enforced client-side too> | <what to do about it> |

Constraints the backend enforces but the frontend must *also* respect
proactively (hide a disabled action, not just handle its error) belong
here as a table, not buried in prose where they're easy to miss.

## 2. <Contract basics>

Base URL, auth mechanism, the success/error envelope shape — copy-pasted
inline, not "see the other repo's types file." A reader who has to open a
second codebase to understand your envelope defeats the point of a
self-contained handoff.

## 3+. Per-capability sections

One section per user-facing capability, each with: the request shape, the
response shape, the states/errors specific to it, and any copy/microcopy
the backend expects the frontend to show verbatim (error messages, empty
states).

## N. Current backend status

The most important section when the handoff happens mid-build. Explicitly
list: what's fully wired, what exists as a stub (button can exist, but the
check behind it isn't real yet), and what doesn't exist at all yet. A
reader must be able to answer "can I promise this to a user today" from
this section alone.
```

Rules of thumb:

- **Name the audience specifically**, not generically — "the dashboard
  frontend agent," not "frontend developers." A specific audience line
  tells the reader immediately whether this doc is for them.
- **Self-contained means self-contained.** Inline the request/response
  shapes, the constants, the error codes — don't make the reader cross-
  reference the other side's source to fill in a gap this doc left.
- **The "current status" section is not optional** when any part of the
  backing system is still in progress. Silently omitting it is how a
  reader ships a feature assuming something works that's actually a stub.
- **A temporary namespace or prefix gets called out explicitly** (e.g. "the
  `storage-v2` prefix is intentional and temporary; treat it as the
  feature's namespace for now") so the reader doesn't wonder whether it's
  a typo or build something that assumes it's permanent.

## Why

A handoff doc fails quietly when it assumes shared context the reader
doesn't have — "see the API types" assumes the reader can and will open
that other codebase, which is exactly the assumption that breaks when the
reader is a different agent, a different session, or someone joining
later without the conversation history that produced the doc. The
"current backend status" section exists specifically because handoffs
written mid-build are the most likely to be misread as "this is all
finished" — being explicit about what's stubbed prevents the reader from
promising a user something that doesn't work yet.

## Exceptions

- For a fully-finished API with stable, published API documentation
  already covering the contract, a shorter handoff that links to that
  documentation instead of re-inlining it is fine — the self-containment
  requirement is about not making the reader reconstruct undocumented
  internal state, not about never linking to genuinely stable external
  docs.
- A handoff to someone who already has full access to and fluency with the
  other side's codebase (a teammate who wrote half of it) can be lighter —
  the self-contained framing matters most when the reader is meant to
  *not* need that access.

## See also

- [docs-feature-planning-spec](../docs-feature-planning-spec/SKILL.md) — the doc that typically precedes this one; a handoff spec describes what got built from a plan, for a different audience than the plan itself.
