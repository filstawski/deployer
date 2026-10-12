---
name: git-commit-messages
description: >-
  Conventional-commit-style message format — a type prefix (feat/fix/docs/
  chore/refactor/...), an imperative present-tense summary under ~70
  characters, an optional body explaining why rather than what, and no
  AI/assistant attribution or co-author watermark lines. Use when writing
  or reviewing a git commit message, or whenever an agent is about to run
  `git commit` and needs to know the message format and what not to
  include. Companion to git-commit-requires-approval (whether a commit
  should happen at all).
metadata:
  type: practice
  category: git
---

## Rule

- **First line:** `<type>(<optional scope>): <summary>` — imperative
  present tense ("add", not "added" or "adds"), under ~70 characters, no
  trailing period.
- **Type** is whichever of these best matches the primary change:
  `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `build`, `ci`, `perf`,
  `style`. If a change genuinely needs two types, that's two commits, not
  one commit picking the closer-sounding type.
- **Scope** (the optional `(scope)` part) names the affected area only
  when the repo is big enough that it actually disambiguates
  (`feat(auth): ...` in a large app). Skip it in a small repo where it adds
  no information over the summary alone.
- **Body** (blank line, then prose) explains *why* the change was made —
  the reasoning, the incident, the tradeoff behind it — not a restatement
  of the diff. The diff already says what changed; the body earns its
  place only by saying something the diff can't. Omit the body entirely
  for a change that's genuinely self-explanatory from the summary alone.
- **No AI/assistant attribution lines, ever, unless explicitly asked for in
  that instance.** No `Co-Authored-By: Claude ...`, no "Generated with
  ...", no session links, no watermark of any kind — regardless of what a
  tool's default commit template suggests appending. A commit's authorship
  metadata (the actual git author/committer fields) already records who
  ran the command; a text watermark in the message body is a separate,
  unwanted thing.

Examples:

```
feat: add per-route rate limit override

Some upload endpoints need a much higher ceiling than the global
default; per-route config keeps that from requiring a schema change.
```

```
fix: stop double-firing the delete audit entry
```

```
docs: correct the outdated symlink command in the README
```

## Why

Conventional types make `git log --oneline` and any changelog tooling
useful without extra setup, and a fixed format is one less thing to decide
per commit. The body's job is specifically to carry information the diff
itself cannot — motivation, context, a link to an incident — so a body
that just narrates the diff in prose is wasted text nobody will read twice.
Attribution/watermark lines are excluded on purpose: a commit is reviewed
and authored under my direction as far as the repository's history is
concerned, and I don't want that stamped as a permanent, unremovable
annotation on every commit going forward just because a tool defaults to
adding one.

## Exceptions

- A one-off WIP/checkpoint commit on a private branch that will be
  squashed before merging doesn't need to follow this — conventional
  formatting is for commits that will actually live in shared history.
- If a project's existing history already uses a different, established
  convention (a team-wide scope list, a differently-shaped format), match
  that existing convention instead of imposing this one on a shared repo
  whose conventions aren't mine to unilaterally change.

## See also

- [git-commit-requires-approval](../git-commit-requires-approval/SKILL.md) — whether a commit should happen at all, which comes before how it's worded.
