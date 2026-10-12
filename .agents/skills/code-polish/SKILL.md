---
name: code-polish
description: >-
  Pre-commit cleanup pass over the current change set: strip the debris
  left behind by trial-and-error iteration (debug logs, commented-out
  attempts, stacked workarounds, dead branches, defensive checks for
  impossible states, swallowed errors, `any`/ignore escapes), replace
  symptom patches with the one clean root-cause fix, then bring what
  remains up to professional standard — clear naming, doc comments on
  public API, idiomatic error handling, the project's own conventions,
  passing format/lint/typecheck/tests. Behavior-preserving and scoped to
  the diff; the result is usually smaller, never more elaborate. Use when
  the user says "polish", "clean this up", "make it commit-ready", or
  before a commit is proposed after a stretch of iterative fixing. Does
  not commit — see git-commit-requires-approval.
metadata:
  type: practice
  category: ai-collaboration
---

## Rule

Polish is a **behavior-preserving rewrite of the current change set into
the version a careful senior engineer would have written on the first
try.** Not a feature pass, not a refactor of the codebase, not a bug
hunt — a pass that makes the diff about to be committed look intentional.

The benchmark for every hunk in the diff is one question:

> *If I were solving this problem fresh, knowing what I know now, would I
> write this line?*

If no, it goes or it gets rewritten. Iteration history — "tried X, didn't
work, added Y on top, then Z fixed it" — must not survive into the
commit. Only Z survives, written cleanly, at the right place.

Work through the steps below in order. Each step is a separate lens over
the same diff; don't skip ahead to "documentation" while debris is still
lying around, because half of what you'd document is about to be deleted.

---

### Step 0 — Establish scope: the diff, not the repo

Polish only what this change touches. Find it first:

```sh
# Uncommitted work (staged + unstaged) against the last commit
git status --short
git diff HEAD --stat
git diff HEAD

# Untracked files are part of the change too — git diff doesn't show them
git ls-files --others --exclude-standard

# On a feature branch, polish the whole branch, not just the last edit
git diff main...HEAD --stat
git diff main...HEAD
```

Rules for scope:

- **Code outside the diff is out of scope**, even if it's ugly. Mention it
  in the report if it's genuinely bad; don't touch it. A polish pass that
  reformats twenty unrelated files turns a reviewable diff into an
  unreviewable one.
- **Exception: code the diff made worse or made dead.** If the change
  left an old helper unused, an import orphaned, or a caller calling
  something in a now-wrong way, that's part of the change and in scope.
- **Unrelated files in the diff are a finding.** A stray edit to a config
  file, a lockfile churn that isn't needed, a formatting-only change in an
  untouched module, a leftover `test.ts`/`debug.py`/`scratch.md` — revert
  or delete them (after confirming with the user if they're not obviously
  scratch) so the commit is only the change.

### Step 1 — Reconstruct the intent

Before cutting anything, write down (for yourself) in one or two
sentences:

1. **What was the actual problem or feature?**
2. **What is the root cause / the minimal set of changes that achieves
   it?**

Read the diff as a whole with that in mind. Iterated-on code almost always
contains the real fix plus several earlier attempts that were never
removed because "it works now." Your job is to find the one change that
actually does the work, and recognize everything else as scaffolding.

A useful check: **revert individual hunks mentally (or actually, with
tests) and ask whether the behavior changes.** A hunk whose removal
changes nothing observable is debris.

### Step 2 — Remove iteration debris

Hunt these down in the changed lines. None of them belong in a commit.

| Debris | Looks like |
| --- | --- |
| Debug output | `console.log`, `print(`, `dbg!`, `fmt.Println("here")`, `debugger;`, `pdb.set_trace()` |
| Commented-out attempts | `// const unique = [...new Set(users)];` |
| History-narrating comments | `// FIX: attempt 3`, `// changed this because it was broken`, `// new version` |
| Stacked workarounds | a dedupe *and* a clear *and* a retry, each added to fix the same symptom |
| Guesswork fallbacks | `data.users ?? data.items ?? data ?? []` "just in case" the shape differs |
| Defensive checks for impossible states | `if (!user \|\| !user.id)` on a value the type system already guarantees |
| Silenced errors | `catch (e) { console.error(e); return []; }`, `except Exception: pass` |
| Timing band-aids | `setTimeout(..., 500)`, `sleep(1)`, arbitrary retries added to paper over a race |
| Type/lint escapes | `as any`, `: any`, `@ts-ignore`, `@ts-expect-error` without reason, `// eslint-disable-next-line`, `# type: ignore`, `# noqa` |
| Redundant assignments | `state.users = []; state.users = unique;` |
| Unused parameters/variables/imports | `force?: boolean` that nothing reads; imports left from an abandoned approach |
| Versioned names | `handleSubmitNew`, `fetchUsers2`, `parseV2`, `fixedLayout`, `tempResult` |
| Dead branches | `if (false)`, flags hard-coded to one value, `else` that can't be reached |
| Scratch files | `test.js`, `debug.py`, `out.json`, `notes.md` in the repo root |

A quick sweep over the changed lines only (not the whole repo):

```sh
# Added lines only, across the change set
git diff HEAD -U0 | grep '^+' | grep -v '^+++' | grep -nE \
  'console\.(log|debug|trace)|debugger|print\(|pdb|breakpoint\(\)|TODO|FIXME|XXX|HACK|@ts-ignore|@ts-expect-error|eslint-disable|as any|: any\b|# ?type: ?ignore|noqa|setTimeout|sleep\('

# Commented-out code heuristics (lines that are comments ending in ; or ) or {)
git diff HEAD -U0 | grep '^+' | grep -nE '^\+\s*(//|#)\s*.*[;){]\s*$'
```

The grep is a starting point, not the review. Read every changed hunk.

**`TODO` / `FIXME`:** an added TODO is allowed only if it names a real,
deliberate follow-up that's out of scope for this change (ideally with an
issue reference). "TODO: fix this properly" on code you're about to commit
means: fix it properly now, or tell the user it's not fixed.

### Step 3 — Replace symptom patches with the root-cause fix

This is the most important step, and the one that separates polish from
cosmetics. Iterated fixes tend to accumulate *around* the bug rather than
*at* it: every attempt adds a guard downstream of where things actually
go wrong.

A typical agent-iterated fix for "the user list shows duplicates after
refreshing":

```ts
// FIX: dedupe users (attempt 3)
export async function loadUsers(force?: boolean, retry = 0): Promise<any[]> {
  console.log('loadUsers called', force, retry);
  try {
    let users: any[] = [];
    const res = await fetch('/api/users');
    const data = await res.json();
    if (data && data.users && Array.isArray(data.users)) {
      users = data.users;
    } else if (data && Array.isArray(data)) {
      users = data; // fallback just in case
    }
    // const unique = [...new Set(users)];
    const seen: any = {};
    const unique = users.filter((u: any) => {
      if (!u || !u.id) return false;
      if (seen[u.id]) return false;
      seen[u.id] = true;
      return true;
    });
    state.users = [];
    state.users = unique;
    return unique;
  } catch (e) {
    console.error(e);
    if (retry < 3) {
      await new Promise((r) => setTimeout(r, 500));
      return loadUsers(force, retry + 1);
    }
    return [];
  }
}

// elsewhere, unchanged by the agent:
async function onRefresh() {
  const users = await loadUsers();
  state.users.push(...users); // <- the actual bug
}
```

The API never returned duplicates. `onRefresh` appended instead of
replacing. Every line of the dedupe/retry/fallback machinery was a guess
that happened to mask the symptom. The polished change:

```ts
/**
 * Fetches all users and replaces the current list in state.
 *
 * @throws {ApiError} if the request fails; callers decide how to surface it.
 */
export async function loadUsers(): Promise<User[]> {
  const res = await fetch('/api/users');
  if (!res.ok) throw new ApiError(res.status, 'Failed to load users');

  const { users } = (await res.json()) as UsersResponse;
  state.users = users;
  return users;
}

async function onRefresh() {
  await loadUsers();
}
```

What happened in that rewrite:

- **The fix moved to the root cause** (`push` → the function that already
  owns state assignment). The dedupe is gone because nothing produces
  duplicates any more.
- **The shape guesswork is gone** — the response type is known; if it
  weren't, the fix is to find out, not to accept three shapes.
- **The retry is gone** — it was never related to the bug. If retrying
  failed requests is actually wanted, that's a separate, deliberate
  feature, not something that rides along in a duplicate-list fix.
- **The error is no longer swallowed.** Returning `[]` on failure made a
  network error indistinguishable from "there are no users."
- **`force` and `retry` params are gone** — nothing used them.
- **`any` is gone** — real types.

How to find the root cause when the diff doesn't make it obvious:

1. Remove the workarounds and reproduce the original problem (test, or
   run the app).
2. Trace the bad value backwards to where it's first wrong.
3. Fix it there, in the smallest change that's correct.
4. Confirm the original symptom is gone without any of the workarounds.

If you can't establish the root cause with confidence, **don't guess
again** — leave the working fix in place, clean it as far as is safe, and
say explicitly in the report that the fix treats a symptom and what the
suspected root cause is. An honest "this works but I'm not sure why" is
better than a confident rewrite that reintroduces the bug.

### Step 4 — Make it clean

With only the real change left, bring it to a clean, conventional shape.

**Match the surrounding code first, industry convention second.** The
most "professional" thing a change can do is look like it was written by
the same person who wrote the rest of the file. Before applying any
general rule below, look at how neighboring code handles the same
concern — naming, error handling, import style, file layout, test style —
and follow that. If a project has a `CLAUDE.md`, lint config, style guide,
or other playbook skills linked in, those win over anything generic here.

**Naming** — names say what a thing *is* or *does*, in the domain's
vocabulary:

```ts
// before
const d = await get(u);
const tmp = d.filter((x) => x.a);
function handleData2(res: any) { ... }

// after
const orders = await fetchOrders(userId);
const activeOrders = orders.filter((order) => order.isActive);
function applyOrderUpdate(update: OrderUpdate) { ... }
```

- Booleans read as predicates: `isActive`, `hasAccess`, `shouldRetry`.
- Functions are verbs: `fetchOrders`, `parseHeader`, `assertOwner`.
- No versioned/temporal names: `New`, `2`, `Old`, `Fixed`, `Temp`,
  `Final`.
- No abbreviations that aren't universal in the domain (`id`, `url`,
  `db` are fine; `usr`, `cfg2`, `mgr` are not — unless the codebase
  already uses them consistently).

**Control flow** — flat and early-exit over nested:

```ts
// before
function canEdit(user: User, doc: Doc) {
  if (user) {
    if (doc.ownerId === user.id) {
      return true;
    } else {
      if (user.role === 'admin') {
        return true;
      } else {
        return false;
      }
    }
  }
  return false;
}

// after
function canEdit(user: User, doc: Doc): boolean {
  return doc.ownerId === user.id || user.role === 'admin';
}
```

**Magic values** — name them when the meaning isn't obvious from the
line itself:

```ts
// before
if (Date.now() - session.createdAt > 1209600000) logout();

// after
const SESSION_MAX_AGE_MS = 14 * 24 * 60 * 60 * 1000; // 14 days

if (Date.now() - session.createdAt > SESSION_MAX_AGE_MS) logout();
```

(`0`, `1`, `-1`, `''`, `100` as a percentage — obvious values stay
inline. Don't turn `items.length > 0` into `items.length > EMPTY_LENGTH`.)

**Functions** — one job each. If a function in the diff has a comment
like `// step 2: now validate`, the steps are probably functions. But
don't split a clear 25-line function into five 5-line ones just to hit a
size number; split when a piece has a name worth giving it.

**Near-duplicates and reusable practices — hunt, then extract one
primitive.** This is a search, not a "if you happen to notice" check.
For every function, method, class, hook, or component the change
added, *and* for any pair of similar ones already sitting in the same
diff, grep the rest of the diff and the rest of the repo for a
structural sibling: same operation on a different type, same
parameter bundle re-threaded through several functions, same steps in
the same order. Also ask whether what the change introduced is a
*practice later work will need* (a general mechanic, not a one-off
tied to this screen) — leaving a narrow helper that the next similar
feature will fork is a future duplicate. Follow
[extract-reusable-primitives](../extract-reusable-primitives/SKILL.md).

```sh
git grep -nE 'function (format|parse|to)Date|formatDate|toISODate'
```

If you can name the second copy, or the logic is a general practice
this codebase will keep doing, don't keep the narrow form. Fold into
one parameterized primitive — or extend the existing helper so later
call sites can use it — before the commit. That's the version a
careful engineer would have written on the first try; two near-copies
(or a one-off that the next feature will have to fork) in a
"polished" diff are still debris.

Scope still holds: only practices this change introduced or touched.
Pre-existing near-duplicates the diff never went near stay out of
scope — mention them in the report, don't start a repo-wide DRY pass.

**Error handling** — errors are either handled meaningfully or
propagated, never silenced:

```ts
// before — swallows everything, including bugs
try {
  await saveDraft(draft);
} catch (e) {
  console.error(e);
}

// after (a) — let it propagate; the caller / error boundary handles it
await saveDraft(draft);

// after (b) — handle the specific, expected failure; rethrow the rest
try {
  await saveDraft(draft);
} catch (error) {
  if (error instanceof ConflictError) {
    return { status: 'conflict', latest: error.latest } as const;
  }
  throw error;
}
```

```python
# before
try:
    config = load_config(path)
except Exception:
    config = {}

# after
try:
    config = load_config(path)
except FileNotFoundError:
    config = DEFAULT_CONFIG  # first run: no config file yet is expected
```

**Types** — the strictest honest type. Replace `any` with the real type,
`unknown` + narrowing at a trust boundary, or a generic. Remove casts that
only existed to silence a compiler complaint that is now fixed. A cast or
ignore that genuinely must stay gets a one-line comment saying *why*:

```ts
// The SDK's types omit `retryAfter`, but the API always sends it on 429.
const { retryAfter } = error.body as { retryAfter: number };
```

**Validation lives at boundaries.** Validate/parse untrusted input once,
where it enters (request handler, form submit, file read, env load), and
trust the typed result from there on. Delete re-validation scattered
through inner functions that iterating added "to be safe."

### Step 5 — Document it (and only as much as it needs)

Documentation is for the next reader, not for proving effort.

**Doc comments on anything exported / public** — what it does, what it
expects, what it returns, what it throws, and any non-obvious behavior.
Not on trivial private helpers whose name already says it all.

```ts
/**
 * Resolves the effective price for a line item, applying the best eligible
 * discount. Discounts never stack — only the single largest one applies.
 *
 * @param item - The line item; `quantity` must be a positive integer.
 * @param discounts - Candidate discounts; ineligible ones are ignored.
 * @returns The unit price in cents after discount, never below 0.
 */
export function resolveUnitPrice(item: LineItem, discounts: Discount[]): number {
```

```python
def resolve_unit_price(item: LineItem, discounts: list[Discount]) -> int:
    """Return the unit price in cents after the best eligible discount.

    Discounts never stack; only the single largest eligible one applies.
    The result is clamped at 0.

    Raises:
        ValueError: if ``item.quantity`` is not a positive integer.
    """
```

Use the ecosystem's standard format (TSDoc/JSDoc, Python docstrings in
the style the project already uses, `///` in Rust, Go doc comments
starting with the identifier's name, etc.).

**Inline comments explain *why*, never *what*:**

```ts
// bad — restates the code
// loop over users and check if active
for (const user of users) if (user.isActive) ...

// bad — narrates history (that's what git log is for)
// changed from map to forEach because map was causing issues

// good — captures a constraint the code can't express
// Stripe sends events out of order; ignore anything older than what we've stored.
if (event.created < subscription.lastEventAt) return;
```

**Don't add documentation bloat:** no file-header banners restating the
filename, no `@param id - the id`, no comment above every line, no
section dividers in a 40-line file. If code needs a comment to explain
*what* it does, first try renaming or restructuring so it doesn't.

If the change alters behavior users/devs rely on (config options, env
vars, CLI flags, public API), update the README / relevant docs in the
same change.

### Step 6 — Reusability, without speculation

"More reusable" means **the code that exists is easy to reuse**, not that
code is added for reuse nobody has asked for.

Do:

- Keep functions pure where it's free to do so (inputs → output, side
  effects at the edges).
- Take dependencies as parameters rather than reaching into globals when
  that's the project's existing pattern.
- Put a helper in the shared location (`lib/`, `utils/`, the relevant
  module) if it's a practice later work will need, or a second caller
  exists / is part of this change. The hunt for those practices is
  Step 4; this step is the brake on unused machinery.
- Export types that callers will need.

Don't:

- Add an interface with one implementation, a factory for one product, a
  config option nobody sets, a generic `<T>` that's only ever `User`, or a
  plugin system for one plugin.
- Extract a helper used exactly once just to make a function shorter,
  unless the name genuinely clarifies.
- Invent a framework for a call site that doesn't exist. A real
  practice this change introduced — something later work will need, or
  a second copy you can point at — is Step 4, not "speculation" to
  leave as a narrow one-off.

```ts
// over-engineered for one caller
interface NotificationSender { send(msg: Message): Promise<void> }
class EmailNotificationSender implements NotificationSender { ... }
class NotificationSenderFactory {
  static create(type: 'email'): NotificationSender { return new EmailNotificationSender(); }
}
await NotificationSenderFactory.create('email').send(msg);

// right-sized
await sendEmail(msg);
```

When the second transport actually arrives, the abstraction will be
shaped by two real cases instead of one imagined one.

### Step 7 — Verify nothing changed that shouldn't have

Polish is a refactor. It must not change behavior (except removing the
effects of debris, such as console noise). Run the project's own checks —
find them rather than guessing:

```sh
# JS/TS — see what the project actually defines
cat package.json | grep -A20 '"scripts"'
# typically some of:
npm run format   # or: npx prettier --write <changed files>
npm run lint     # or: npx eslint <changed files>
npm run typecheck  # or: npx tsc --noEmit
npm test

# Python
ruff format . && ruff check . && mypy . && pytest

# Rust
cargo fmt && cargo clippy -- -D warnings && cargo test

# Go
gofmt -w . && go vet ./... && go test ./...
```

Also check for a `Makefile`, `justfile`, pre-commit config, or CI workflow
— those define what "passing" means for this repo, and the commit will be
held to them anyway.

- **All checks that passed before polish must pass after.** If a check was
  already failing before the change, say so — don't silently fix unrelated
  failures, and don't claim green when it isn't.
- **If a test was only passing because of a workaround you removed**, that
  means the workaround wasn't debris or the test was wrong — investigate,
  don't just put the workaround back.
- **If there are no tests covering the change and it fixes a bug**, adding
  one focused regression test is in scope — it's the proof the root-cause
  fix is right. A sprawling new test suite is not.
- Re-read the final diff top to bottom one last time:

```sh
git diff HEAD --stat   # should be the change, and only the change
git diff HEAD
```

A good polish usually makes `--stat` *smaller* than before.

### Step 8 — Report, don't commit

Stop at "ready for review." Polish is the step *before* a commit, not the
commit itself — see
[git-commit-requires-approval](../git/git-commit-requires-approval/SKILL.md).
Give the user a short report they can check against the diff:

```md
## Polish summary

**Root cause:** `onRefresh` appended to `state.users` instead of replacing it.
Fixed at the source; removed the dedupe/retry/shape-fallback workarounds
that were masking it.

**Removed**
- Debug logging in `loadUsers`, `UserTable`
- Unused `force`/`retry` params and the retry loop
- Commented-out `Set`-based dedupe
- `scratch/test-users.ts` (untracked scratch file)

**Cleaned**
- `any` → `User` / `UsersResponse` in `users.ts`
- Errors now propagate as `ApiError` instead of returning `[]`
- TSDoc on `loadUsers`

**Added**
- Regression test: refreshing twice doesn't duplicate rows

**Checks:** format ✓ lint ✓ typecheck ✓ tests ✓ (42 passed)

**Flagged, not changed (out of scope)**
- `utils/date.ts` has two near-identical formatters; worth merging separately.
```

When a commit is then requested, write the message per
[git-commit-messages](../git/git-commit-messages/SKILL.md) — the polish
summary's "Root cause" line is usually the commit body.

---

### Hard rules (the short version)

1. **Behavior-preserving.** Polish never adds features or changes what
   the code does, beyond removing debris.
2. **Scoped to the change.** Don't refactor untouched code; flag it.
3. **Smaller, not bigger.** If polish grows the diff, check that every
   added line is a doc comment, a real type, or a regression test.
   Anything else is suspect.
4. **Root cause over symptom.** One correct fix at the source beats three
   guards downstream.
5. **Project conventions over generic "best practice."**
6. **No silenced errors, no unexplained escapes (`any`, ignores,
   disables).**
7. **Checks pass, reported honestly.**
8. **Never commits.**

## Why

AI-assisted (and, honestly, tired-human) bugfixing tends to converge by
accretion: each attempt that doesn't quite work stays in, and the next
attempt is layered on top, until the symptom goes away. The result
*works*, so it gets committed — and the repo inherits a dedupe that hides
a state bug, a retry that hides a race, a `catch` that hides a crash, and
three shapes of response parsing for an API with one shape. Each of those
is a future bug or a future hour of someone asking "why is this here?"
before they dare delete it.

The commit boundary is the cheapest place to stop that: the context of
what was tried and why is still fresh, the diff is small, and nothing has
been built on top of it yet. A deliberate pass at that moment, with an
explicit "reconstruct the intended change, keep only that" mindset, is far
cheaper than archaeology later.

The "smaller, not bigger" and "no speculation" constraints exist because
the naïve version of "make it more professional" goes the other way:
interfaces, factories, config options, and paragraph-long comments added
to look thorough. That's just a different kind of bloat. Professional
code is code where every line has a reason to exist — which, after
iteration, almost always means deleting more than adding.

Behavior-preservation and scope are what make polish safe to run
routinely: if it could also change behavior or touch unrelated files, the
user would need to review the polish itself as carefully as the original
change, and it would stop being a cleanup step.

## Exceptions

- **Not a bug hunt or a security review.** Polish will surface bugs it
  trips over (and must report them), but it isn't a systematic search for
  them. For that, run `/code-review` or `/security-review` separately —
  ideally *after* polish, on the cleaned diff. Claude Code's built-in
  `/simplify` overlaps with Step 4/6; polish is the broader, pre-commit
  superset that also covers debris, root-cause, docs, and verification.
- **A bug found during polish is not silently fixed.** If it's inside the
  change and the fix is small and obvious, fix it and call it out
  explicitly in the report as a behavior change. If it's outside the
  change, or non-trivial, report it and leave it.
- **Prototypes, spikes, and throwaway scripts** don't need the full pass —
  at most Step 2 (debris) if they're being committed at all.
- **Generated code** (ORM clients, OpenAPI clients, migrations, lockfiles)
  is never hand-polished; regenerate it from its source if it's wrong.
- **Large, already-reviewed diffs / shared branches** — if the change was
  already reviewed by someone, a polish pass that rewrites it invalidates
  their review. Keep polish to debris removal and ask before anything
  structural.
- **Established project conventions win**, even where they differ from
  what's described here (e.g. a codebase that deliberately uses
  exceptions-as-control-flow, a house abbreviation style, a no-doc-comment
  policy). Polish towards the project, not towards this file.
- **Unclear intent.** If Step 1 can't answer "what was this change
  supposed to do" from the diff and conversation, ask the user before
  deleting anything that might be load-bearing.

## See also

- [extract-reusable-primitives](../extract-reusable-primitives/SKILL.md) — Step 4 hunts the change for reusable practices (near-duplicates, repeated parameter bundles, general mechanics later work will need) and extracts a primitive the future can call; Step 6 still forbids speculative frameworks for a single caller.
- [git-commit-requires-approval](../git/git-commit-requires-approval/SKILL.md) — polish ends at "ready for review"; the commit is a separate, explicitly requested step.
- [git-commit-messages](../git/git-commit-messages/SKILL.md) — the format for the commit that follows a polish pass.
