# Memory Protocol

How this vault is used during a session: what to read before starting, what to write
as you go, and what shape notes take. The tools own note metadata and filing — you
supply prose.

## Addressing Notes

Notes are addressed by ref: what a note is, not where it lives. Never a file path.

| Ref | Addresses |
|---|---|
| `session:2026-08-17-cache-invalidation` | a session note |
| `project:blitter:design` | a project's design note |
| `project:blitter:decisions` | its decisions log |
| `project:blitter:todo` | its backlog |
| `standard:language-go` | an engineering standard |
| `topic:shipping` | an operating topic |
| `glossary` | the glossary |

Refs come from the tools — `gandalf_boot`, `gandalf_lint`, or whichever tool created
the note. Do not construct one from a filename you saw somewhere; where a note lives
is Gandalf's problem, and it will move things if the vault's conventions change.

`session:latest` resolves to the most recent session note. Ask for it explicitly when
you mean it; nothing defaults to it.

A note that follows none of the conventions — something written by hand, outside the
vault's structure — gets a `path:` ref. Those can be read but not written.

## Session Startup

Before substantive work:

1. Call `gandalf_boot`. It returns this protocol, the operating contract, the topics
   available on demand, and any session note still open today.
2. If a session note is already open and this is the same unit of work, continue it
   rather than starting another.
3. Look for prior work on the same topic before re-deciding anything. For a known
   project, read its design, decisions, and todo notes.
4. Distil what you found into a few bullets, surface them to the user, and confirm
   anything stale or consequential before relying on it.
5. Create the session note with `gandalf_session_start` before proposing or writing
   code. One note per logical unit of work — not one per conversation, not one per day.

Read-only work creates no notes. Read freely, write nothing.

## Writing

- `gandalf_session_start` opens the session note and returns its ref. Hold that ref;
  if you lose it, `gandalf_boot` will hand it back.
- `gandalf_note_new` creates a note of a given kind and returns its ref. Gandalf
  decides where it goes.
- `gandalf_note_append` is the only way to change a note's body. Nothing already
  written can be destroyed by it, which is why decisions logs and session notes are
  safe to keep adding to.
- `gandalf_note_update` changes metadata only — tags, related links, status. The
  updated date is maintained for you.
- `gandalf_lint` reports schema violations and links pointing nowhere, addressed by
  ref so a finding can be fed straight back in.

You never write a frontmatter block by hand, and you never choose a filename.

## Session Notes

Update the session note as the work happens, not at the end. Capture:

- What the work is trying to achieve, and why.
- Decisions and the reasoning behind them.
- Alternatives considered and why they were rejected.
- Assumptions that may need revisiting.
- Context that would be hard to reconstruct from the code alone.
- For environment-specific changes, the difference between what the change is intended
  to do generally and what was actually verified where. Record the environment and
  machine. Do not imply a change is live somewhere it was not verified.

Do not compact session notes. They are the historical record, and their value is that
they were written at the time.

## Project Notes

Each project has up to three notes, addressed as `project:<name>:design`,
`project:<name>:decisions`, and `project:<name>:todo`.

- **Design** describes the current state only. Delete superseded history rather than
  letting it accumulate; keep it short enough to stay read.
- **Decisions** is append-only. Record significant decisions as they are made, with
  their context and the tradeoffs accepted. Supersede an old entry with a new one
  rather than rewriting it.
- **Todo** is the durable backlog. Add concrete items whenever the user asks to come
  back to something, or when the work turns up something needing a later revisit. Move
  finished work to a completed section instead of leaving status to be inferred.

Before starting work on a project, check its design note for claims that have gone
stale.

## Metadata

Every note carries a type, created and updated dates, tags, related links, an author,
and an optional status. Keep tags lowercase and hyphenated so they stay usable as
filters. Pass refs when linking notes; Gandalf converts them to links the vault's
editor understands.

Frontmatter keys Gandalf does not manage are left alone, so anything your editor adds
survives untouched.

## Vault Hygiene

- Do not create empty notes just to satisfy a link.
- Look for an existing answer before asking the user a factual question that earlier
  work may already have settled.
- Each folder's README defines what belongs in it. Keep those definitions there rather
  than restating them elsewhere.
- Active rules belong in the operating contract or a topic. The reasoning behind a
  rule belongs in the correction history, which is not read at startup.
