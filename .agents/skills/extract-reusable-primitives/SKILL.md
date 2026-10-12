---
name: extract-reusable-primitives
description: >-
  Don't let reusable logic stay as a one-off or accumulate as
  near-duplicate functions, methods, classes, hooks, or components.
  Before writing a new one of these — and again when polishing a
  change that just added one — search the codebase and the current
  change for a structural sibling, a repeated parameter bundle, or a
  general practice later work will need. If you find it, build ONE
  properly designed primitive instead: parameterized over what varies,
  or a class/hook/factory that owns shared state or config and exposes
  the operations as methods, so future call sites and future fixes
  have one place to go. Applies everywhere — UI components/hooks,
  backend functions/methods/classes, utility snippets, API wrappers.
  Pairs with code-polish (the hunt over a change set) and
  website-nextjs-dashboard-project-structure (where the extracted file
  goes).
metadata:
  type: practice
  category: architecture
---

## How to apply

1. **Before writing a new function, method, class, hook, or component
   — and again over the current diff during polish — search first.**
   Grep the repo *and* the change itself for similarly-named or
   similarly-shaped code: same operation on a different type, same
   parameter bundle repeated across several functions, same steps in
   the same order. Also ask whether what you're writing is a *practice
   later work will need* — a general mechanic (height animation, name
   formatting, an API client that always carries the same key + base
   URL), not a one-off tied to this screen. "I don't remember seeing
   one" is not a search. Actually look. A polish pass is not exempt
   because the copies already exist; that's when they're easiest to
   still fold together into something the future can call. See
   [code-polish](../code-polish/SKILL.md).
2. **If something structurally similar already exists, you're about to
   write (or have just written) more than one of these in the same
   task, or the logic is a general practice this codebase will keep
   doing — stop.** Don't add another near-duplicate, don't leave two
   in a commit-ready diff, and don't ship a narrow helper that the
   next similar feature will have to fork. Build the general form
   once, designed so later call sites can use it:
   - Varies only by a value, field, or callback → one function,
     parameterized over the part that varies.
   - Several operations share the same state or config (an API key + base
     URL, a connection + table name, a user + their permissions) → one
     primitive that owns that shared part once and exposes the operations
     as methods taking only what actually varies per call. **The exact
     form is negotiable — a `class`, a factory function returning bound
     methods, a custom hook — pick whichever matches how this codebase
     already builds stateful abstractions elsewhere** (this codebase is
     hooks-first React/TypeScript with almost no hand-written classes, so
     a hook or factory function is usually the native fit here, even
     though "build a class" is the canonical name for this pattern in
     general). What's not negotiable is re-threading the same parameter
     bundle through N separate free functions.
3. **Only write (or keep) the narrow, specific version if the search in
   step 1 comes back empty *and* this isn't a practice later work will
   need** — a genuine one-off, not "I'll extract it when the third copy
   shows up."

## Relationship to "avoid premature abstraction"

"Don't over-abstract" and this skill are not opposites. They fail in
different directions:

- **Don't invent unused machinery.** A plugin system, a factory, a
  `<T>` that's only ever `User` — that's designing for a call site
  that doesn't exist. Don't.
- **Do extract a practice so the future is supported.** Near-duplicates,
  a repeated parameter bundle, a general mechanic this app will keep
  doing — leaving those as a one-off (or as N copies) is how the next
  feature forks them and the next bugfix misses a copy. The point of
  extracting is not merely collapsing two functions you already typed.
  It's that later call sites and later fixes go through one properly
  designed primitive.

The test is whether the practice is real, not whether a third file has
been created yet. A height-animation helper you're about to paste into
a second screen, an API client that always carries the same key + base
URL, a person-name format used on three types — those are practices
the future will need, even if you're looking at the first or second
instance. A `NotificationSenderFactory` for the one email the app
sends is not.

## Examples

**Near-duplicate functions → one parameterized function:**

```ts
// Don't: three functions doing the same steps on different fields.
function formatUserName(u: User) { return `${u.firstName} ${u.lastName}`.trim() || "Unbekannt"; }
function formatContactName(c: Contact) { return `${c.firstName} ${c.lastName}`.trim() || "Unbekannt"; }
function formatCompanyContactName(c: CompanyContact) { return `${c.firstName} ${c.lastName}`.trim() || "Unbekannt"; }

// Do: one function, parameterized over the part that actually varies.
function formatPersonName(person: { firstName: string; lastName: string }) {
  return `${person.firstName} ${person.lastName}`.trim() || "Unbekannt";
}
```

**Repeated parameter bundle across free functions → one owner of the shared state:**

```ts
// Don't: every function re-threads the same shared arguments.
async function fetchCase(apiKey: string, baseUrl: string, id: string) { /* ... */ }
async function updateCase(apiKey: string, baseUrl: string, id: string, patch: Patch) { /* ... */ }
async function listCases(apiKey: string, baseUrl: string, filter: Filter) { /* ... */ }

// Do, e.g. as a class:
class CasesClient {
  constructor(private apiKey: string, private baseUrl: string) {}
  async fetch(id: string) { /* ... */ }
  async update(id: string, patch: Patch) { /* ... */ }
  async list(filter: Filter) { /* ... */ }
}

// ...or, in a hooks-first codebase like this one, a factory/hook that
// closes over the shared part instead — same principle, native idiom:
function createCasesClient(apiKey: string, baseUrl: string) {
  return {
    fetch: (id: string) => { /* ... */ },
    update: (id: string, patch: Patch) => { /* ... */ },
    list: (filter: Filter) => { /* ... */ },
  };
}
```

## Why

The point of extracting is that **the next time this logic is needed,
or the next time it has to change, there is one primitive to call or
fix** — not eight near-copies and a ninth that gets missed. Code that
was cheap to write as a one-off (or cheap to paste into the next
screen) turns into several slightly-divergent copies that all have to
be found individually. A codebase with a handful of well-designed,
properly parameterized primitives is easier to extend than one with a
hundred similar-but-not-quite-shared snippets, because the future is
already supported.

The correction that first prompted this skill was UI-specific (a
height animation helper duplicated across a sign-in flow and the
properties tabs before being extracted into the shared
`AnimateDimensions` primitive), but the same shape of mistake happens
identically with backend functions, validators, and classes — the fix
generalizes exactly the same way, which is why step 1 above is
"search," not "remember whether you've seen this before."

## Exceptions

- Two pieces of code that *look* similar but encode genuinely different
  domain rules (two validators that happen to both check string length,
  for unrelated reasons that will diverge the moment one domain's rule
  changes) are not the same thing — merging them just because they're
  shaped alike creates a false shared abstraction that has to be un-merged
  later. Consolidate on shared *behavior*, not shared *appearance*.
- A single trivial one-off with no realistic later use (a one-line
  formatter used exactly once, tied to this screen, with nothing else
  like it anywhere in the codebase) doesn't need its own primitive yet
  — this rule targets practices the future will need, not "wrap every
  function in an abstraction pre-emptively."
- Don't force unrelated operations into one class/hook/factory just
  because they touch the same data type — a shared primitive consolidates
  operations that share *state or config*, not every function that
  happens to take a `User` as its first argument.

## See also

- [code-polish](../code-polish/SKILL.md) — a polish pass runs this same hunt over the current change set; a one-off that later work will have to fork is still debris.
- [website-nextjs-dashboard-project-structure](../website/nextjs/website-nextjs-dashboard-project-structure/SKILL.md) — where the extracted primitive goes once you've decided to pull it out.
