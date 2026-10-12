---
name: docs-migration-status-tracking
description: >-
  Track an incremental migration or a guideline-compliance backlog as an
  explicit checklist/status table in the docs, checked off as each piece
  actually completes, rather than trusting memory or a stale "done" claim.
  Use when migrating a system piece by piece (v1 to v2, old pattern to new
  pattern) where old and new coexist for a while, or when a written
  convention exists but the codebase hasn't fully caught up to it yet.
metadata:
  type: practice
  category: documentation
---

## Rule

When old and new coexist during a migration — or when a written convention
exists ahead of full codebase compliance — keep a literal status table
in the docs, and update it the moment each row actually changes state, not
in a later cleanup pass.

**Migration port-status table:**

```markdown
| Manager | v2 file | Notes |
| ------- | ------- | ----- |
| Image   | ✅ `images.ts` | Shareable · has userDelta · S3 + CDN owned |
| Folder  | ✅ `folders.ts` | Shareable · has userDelta · S3 preview owned |
| Domain  | ✅ `domains.ts` | Non-shareable · custom deleteFlow |
| Invite  | ⏳ pending | |
```

**Guideline-compliance backlog**, same shape, applied to "written
convention vs. actual code" instead of "old system vs. new system":

```markdown
## Decided (keep docs in sync when you refactor code)

- [x] Section order on structures — enforced for all files, any size.
- [x] Named exports for managers — no `export default`.

## Structures — layout / surface

- [ ] Reorder all structures to match the guideline's section order.
- [ ] Add numbered section comments to every structure file (currently
      only `user.ts` has them).
```

Rules of thumb:

- **A row's status reflects reality at time of reading, not at time of
  writing.** The whole value of this doc is that someone can trust it
  without re-auditing the code — a status table that's gone stale is worse
  than no table, since it now actively misleads instead of just being
  silent.
- **Split "decided and enforced" from "still open" as separate sections**
  (checked-off vs. unchecked), the same way a roadmap note separates
  Resolved from Still-open (see
  [docs-roadmap-notes](../docs-roadmap-notes/SKILL.md)) — a flat list that
  mixes settled and pending items forces a re-read of every row to know
  which is which.
- **When a rename or convention change ships, update the doc in the *same*
  change**, not as a follow-up. The most common way this pattern fails in
  practice: code renames an internal method (`_patch` → `$patch`,
  deprecating the old name) but the written guideline describing that
  method still shows the old name — now every reader of the guideline
  learns the wrong convention until someone notices the drift separately.
  If the doc update would be a second PR "later," that's the failure mode,
  not an acceptable sequencing.
- **This doc is disposable once the migration finishes** — a fully-checked
  table or a table with no `⏳`/`[ ]` rows left has done its job; delete it
  or fold anything still relevant into permanent documentation rather than
  leaving a completed checklist around indefinitely. A doc that was true
  once shouldn't linger past being true.

## Why

An incremental migration without a status doc means "is X migrated yet"
requires reading the code every time someone needs to know — and worse,
different people form different beliefs about migration status from
whatever code they last happened to read, with no way to reconcile them.
A guideline-compliance backlog exists for the same reason applied to
"written rule vs. actual code": writing a convention down doesn't make the
whole codebase follow it instantly, and without an explicit backlog,
"already documented" quietly gets treated as "already true" by anyone who
reads the guideline without also checking the code.

## Exceptions

- For a migration small enough to finish in one sitting (a handful of call
  sites, done in one PR), a status table is unnecessary ceremony — this
  pattern earns its keep specifically when old and new *coexist* for a
  meaningful stretch of time.
- Don't let the tracking doc become a place to defer decisions
  indefinitely — an item that's sat unchecked for a long time with no
  progress is a signal to either prioritize it or explicitly decide not to
  do it (and remove the row), not to leave it as permanent background
  guilt.

## See also

- [docs-roadmap-notes](../docs-roadmap-notes/SKILL.md) — the inline, single-decision version of writing down deferred work, versus this doc's many-rows checklist shape.
- [sdk-file-section-order](../../sdk/conventions/sdk-file-section-order/SKILL.md) and [sdk-cache-update-placement](../../sdk/managers/sdk-cache-update-placement/SKILL.md) — real conventions that use exactly this tracking shape to roll out incrementally.
