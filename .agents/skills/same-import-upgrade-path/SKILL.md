---
name: same-import-upgrade-path
description: >-
  When shipping a deliberately minimal version of something you already
  know you'll eventually need to replace with a more capable backing
  (in-process queue now, Redis-backed queue later; in-memory cache now,
  distributed cache later), shape its public API to match the eventual
  real version's shape from day one, so upgrading later is a same-import
  swap instead of a rewrite of every call site. Use when building the
  first, intentionally minimal version of a subsystem that has an obvious,
  well-known "real" implementation you're deferring for now, or when
  reviewing whether a quick internal tool's API will survive being
  swapped out later.
metadata:
  type: practice
  category: architecture
---

## Rule

Before writing the minimal version, look at the shape of the real
implementation you're deferring — its client API, not its internals — and
copy that shape for the minimal version's public interface, even though
the minimal version's actual guts (an array, an in-memory map, a single
process) look nothing like the real thing.

```ts
// The minimal version's public surface: enqueue + a typed job registry,
// deliberately shaped like BullMQ's API even though there's no Redis yet.
export async function enqueue<K extends keyof JobPayloadMap>(
  name: K,
  data: JobPayloadMap[K],
): Promise<string> { /* pushes onto an in-process array */ }

export function registerHandler<K extends keyof JobPayloadMap>(
  name: K,
  handler: (job: Job<JobPayloadMap[K]>) => Promise<void>,
): void { /* ... */ }
```

State the tradeoff explicitly in the doc for the minimal version, so
"why does this look over-engineered for what it does" has a written
answer:

```markdown
## Current backing: in-process, in-memory

This is deliberately the minimum viable queue. The API surface is shaped
like BullMQ (typed job registry, single `enqueue(name, data)` call,
handlers registered at boot) so swapping to Redis + BullMQ later is a
same-import replacement — call sites don't change.

Trade-offs of the current backing:
- ✅ Zero infra, ships today.
- ✅ Same-import BullMQ swap when we outgrow it.
- ❌ Jobs enqueued but not yet drained are lost on process restart.
- ❌ Failing handlers log + swallow; no automatic retry.

## Path to <the real backing>

1. Replace the internals with the real client.
2. Public function signatures stay the same.
3. <Domain concept, e.g. job names> map onto <the real system's equivalent
   concept> unchanged.

No call sites change.
```

Rules of thumb:

- **Model the real thing's API, not its capabilities.** The minimal
  version genuinely won't have retries, persistence, or distribution —
  that's fine and expected. What has to match is the *shape callers see*:
  function names, argument shape, what's synchronous vs. what returns a
  promise, what's registered at boot vs. called per-use.
- **Write the tradeoffs down where the minimal version lives**, not just
  in your head — a ✅/❌ list of what you get and what you're deliberately
  not getting yet, so a future reader (including future you, sizing
  whether it's time to upgrade) doesn't have to reverse-engineer the
  tradeoff from behavior.
- **State explicitly that call sites won't need to change** when you're
  confident of it — that's the whole payoff of this pattern, and it's
  worth confirming in writing rather than leaving implicit, so nobody
  budgets a migration project for what should be a one-file swap.
- **Only do this when the "real" version is genuinely well-known and
  likely**, not for a hypothetical future need you're guessing at. This is
  not "design for extensibility" in general — it's specifically "you
  already know what you'll swap this for; make the swap cheap."

## Why

The alternative — building the minimal version with whatever API is most
convenient for its own simple internals — works fine until the day it
actually needs to grow up, at which point every caller has to change
because the minimal version's API leaked its own limited implementation
(synchronous where the real thing will be async, a bare array where the
real thing needs a typed job name, etc.). Paying the small cost of
matching a known target shape up front is cheap compared to a rewrite of
every call site later, and it costs nothing in the meantime — the minimal
version still ships today with zero added infrastructure.

## Exceptions

- If there's no well-known "real" version yet — genuinely undecided
  between two future directions — don't guess at a shape just to have one.
  Ship the simplest API for the minimal version's actual current job and
  revisit the shape once the real direction is clearer; a wrong guessed
  shape is worse than an honestly provisional one.
- Don't let this become a reason to over-build the minimal version's
  *internals* to prematurely resemble the real thing (adding fake retry
  logic, a fake distributed-lock abstraction) — the payoff is specifically
  in matching the *public shape*, not in simulating capabilities the
  minimal version doesn't actually need yet.

## See also

- [docs-roadmap-notes](../docs/docs-roadmap-notes/SKILL.md) — write the tradeoff and upgrade path down using that note shape.
