# Projects

One folder per project, work and personal alike. Each holds up to three notes,
addressed as `project:<name>:<facet>`:

| Facet | Purpose |
|---|---|
| `design` | Current state only. Delete superseded history rather than accumulating it. |
| `decisions` | Append-only. Significant decisions with their context and accepted tradeoffs. |
| `todo` | The durable backlog. Concrete items, with finished work moved to a completed section. |

Which filenames those become is set by the project category in
`.gandalf/categories.json`.

Design notes go stale silently. Check one against reality before trusting it, and fix
it when it is wrong.

Decisions notes are allowed to be out of date: they record what was decided at the time.
Do not rewrite an old entry when the decision changes; add one that supersedes it.
