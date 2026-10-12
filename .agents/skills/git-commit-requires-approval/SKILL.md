---
name: git-commit-requires-approval
description: >-
  Code changes must be reviewed by a human before being committed — an
  agent must never run `git commit` (or push) on its own initiative, only
  when the user has explicitly asked for a commit in that specific
  instance. Use before running `git commit`/`git push`, when deciding
  whether finishing a task also means committing it, and when designing
  an autonomous workflow, loop, or scheduled agent run that touches code.
metadata:
  type: practice
  category: ai-collaboration
---

## Rule

- **Never commit (or push) as an automatic, implicit step at the end of a
  task.** Finishing an edit, a feature, or a bugfix does not by itself mean
  "and now commit it." Report the change as done and stop there unless
  told otherwise.
- **Only commit when explicitly asked, in that instance.** "Commit this,"
  "make a commit," "commit and push" authorize the commit being asked for
  — not every future change in the same session. A green light once isn't
  a standing green light.
- **The human needs an actual chance to look at the diff before it's
  committed.** If more changes were made after the point the human last
  looked, don't fold those into a commit on the assumption that an earlier
  "looks good" still covers them.
- **This applies to autonomous work too** — loops, scheduled runs,
  multi-step agent workflows. An autonomous task that produces code
  changes stops at "changes ready for review," not "changes committed,"
  unless the human set up that specific automation with a standing,
  explicit instruction to commit (e.g. a durable rule in project config
  written for exactly that purpose) — never as an inference from silence
  ("they didn't say not to commit").

## Why

A commit is a permanent, shared record. Reverting one is possible but
noisy, and a bad commit that gets pushed, reviewed, or built on by someone
else is worse than a bad uncommitted diff sitting in the working tree. The
commit boundary is also the one natural checkpoint where a human catches a
mistake before it becomes part of the project's actual history — skipping
it removes the checkpoint, not just the ceremony. This is the general
"match the blast radius of the action" principle applied to git
specifically: a commit is visible to others and hard to fully undo, so it
defaults to asking rather than defaulting to acting, the same way a force
push or a deleted branch would.

## Exceptions

- A project can grant standing commit autonomy for a specific, well-scoped
  automation via a deliberate, durable instruction (e.g. a written rule in
  that project's own config, set up by the human on purpose for that
  workflow) — but that has to be an explicit decision made in advance, not
  something inferred from the absence of an objection.
- Within a single already-approved commit action, running the `git commit`
  command itself doesn't need re-approval per file — the approval covers
  "make the commit," not a second check on every file that goes into it.

## See also

- [git-commit-messages](../git-commit-messages/SKILL.md) — the message format to use once a commit is actually authorized.
