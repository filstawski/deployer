---
name: docs-informal-to-formal-guidelines
description: >-
  Write project conventions as quick, informal, first-person notes first;
  formalize them into structured guideline docs once the patterns actually
  stabilize; keep the informal source around afterward as project voice
  rather than deleting or rewriting it. Use when a convention is being
  invented and it's too early to write a polished guideline doc, or when
  deciding whether to edit an old informal notes file versus writing a new
  formal doc that supersedes it.
metadata:
  type: practice
  category: documentation
---

## Rule

Don't try to write the polished guideline doc while a convention is still
being figured out. Write it in two stages instead:

**Stage 1 — informal notes**, first-person, no structure required, written
in the moment a decision gets made:

```
Managers should have as few fetchers as possible. Try to make the .fetch
as smart as possible.

_ methods and variables are temporary methods

everything with $ is internal and considered undocumented aka theres
somewhere an wrapper for it.

dont make methods async if they return the promise directly
```

This is allowed to be rough — typos, run-on sentences, no headings. Its
only job is to capture the decision before it's forgotten, cheaply enough
that writing it down is never more friction than not bothering.

**Stage 2 — formalize, once the patterns actually stabilize** (not on a
schedule, and not before there's enough real usage to know the informal
note is still right): rewrite the same content as a structured guideline
doc — the informal note above becomes the `$`/`_` naming rule, the async
rule, and the "few fetchers" rule in a proper conventions doc, each with
its own heading, cross-references to related docs, and room for the
exceptions that showed up in practice.

**Afterward, keep the informal source as-is.** Don't delete it and don't
rewrite it to match the formal doc — it's now a historical record of
"project voice," useful for tone and for understanding *why* a rule exists
in its original, unfiltered form. Point a reader at it explicitly:

```markdown
Informal project voice stays in **`DESIGN_GUIDELINES.md`** at the repo
root. Treat it as quick project voice, not something to rewrite unless
the author asks.
```

## Why

Trying to write the formal, structured version of a convention at the
moment it's invented either slows down the decision (because now writing
it down feels like a bigger task than it is) or produces a formal doc for
a pattern that turns out to be wrong after the first real use — churn that
a rough note wouldn't have cost. Splitting into two stages lets the cheap
version exist immediately and the expensive version exist only once it's
actually worth the investment. Keeping the informal source afterward
(instead of deleting it once formalized) preserves something the polished
version usually strips out: the original reasoning in the author's own
words, which is often more useful for understanding *intent* than the
tidied-up rule that came from it.

## Exceptions

- A convention that's obviously going to be permanent and important from
  the moment it's decided (a security rule, a hard legal constraint) can
  skip straight to being written formally — the two-stage approach is
  about conventions still being worked out, not everything.
- If the informal source turns out to be actively wrong or misleading
  after formalization (not just superseded, but incorrect), that's worth
  a note or a correction — "don't rewrite unless the author asks" is a
  default against casual editing, not a rule against ever fixing a real
  error.

## See also

- [docs-feature-planning-spec](../docs-feature-planning-spec/SKILL.md) — a formal doc template this pattern's "stage 2" often produces, for a feature rather than a standing convention.
