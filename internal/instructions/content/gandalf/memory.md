# Memory Protocol

How this vault is used during a session: what to read before starting, what to write
as you go, and what shape notes take. The tools own note metadata and filing — you
supply prose.

## Addressing Notes

Notes are addressed by ref: what a note is, not where it lives. Never a file path.

A ref starts with a category — a kind of note this vault keeps. The categories are
declared by the vault rather than fixed by Gandalf, so `gandalf_category_list` is the
authority on which exist and how each is addressed. With the shipped defaults:

| Ref | Addresses |
|---|---|
| `session:2026-08-17-cache-invalidation` | a session note |
| `project:blitter:design` | a project's design note |
| `project:blitter:decisions` | its decisions log |
| `project:blitter:todo` | its backlog |
| `standard:language-go` | an engineering standard |
| `topic:shipping` | an operating topic |
| `glossary` | the glossary |

Refs come from the tools — `gandalf_boot`, `gandalf_list`, `gandalf_lint`, or whichever
tool created the note. Do not construct one from a filename you saw somewhere; where a
note lives is Gandalf's problem, and it will move things if the vault's conventions
change.

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
3. Look for prior work on the same topic before re-deciding anything. `gandalf_search`
   finds notes by meaning, so it turns up work described in words you did not think to
   use; `gandalf_list` shows what exists by name when you already know what you are
   after. For a known project, read its design, decisions, and todo notes.
4. Distil what you found into a few bullets, surface them to the user, and confirm
   anything stale or consequential before relying on it.
5. Create the session note with `gandalf_session_start` before proposing or writing
   code. One note per logical unit of work — not one per conversation, not one per day.

Read-only work creates no notes. Read freely, write nothing.

## Finding and Writing

- `gandalf_search` finds notes by meaning rather than wording. Use it when you know the
  subject but not how it was written down.
- `gandalf_list` enumerates the vault by category, returning refs and titles but no
  content. Use it when you know what you want by name.
- `gandalf_note_read` reads a note, or one of the operating topics, by ref. Search and
  listings return refs; pass one straight back to read it in full.
- `gandalf_session_start` opens the session note and returns its ref. Hold that ref; if
  you lose it, `gandalf_boot` will hand it back.
- `gandalf_note_new` creates a note of a given kind and returns its ref.
- `gandalf_note_append` is the only way to change a note's body, and cannot destroy what
  is already there.
- `gandalf_note_update` changes metadata only — tags, related links, status.
- `gandalf_note_delete` removes a note, refusing while anything still links to it and
  listing what does.
- `gandalf_lint` reports schema violations and links pointing nowhere.

Never write a frontmatter block by hand, never choose a filename, never open a vault
file in an editor.

## Links and Backlinks

Write links as refs: `[[standard:language-go]]`. They are stored as vault paths and
handed back to you as refs. A link to a note that does not exist is refused — create the
target first, or leave the link out. No empty note is created to satisfy a link.

Each note carries a `## Backlinks` section listing what points at it. Read it to find
what depends on a note before changing it. Do not write into it: it is rewritten
whenever links change.

## Categories

A category is a kind of note: what it is called, where its notes are filed, and how
they are addressed. The vault declares its own.

- `gandalf_category_list` shows what exists, with the ref form for each.
- `gandalf_category_create` declares a new one. Ask before doing this; it changes how
  the vault is organised. Do not invent a category to avoid deciding where a note
  belongs.
- `gandalf_category_retire` stops new notes being filed under one, leaving the existing
  notes readable and writable.
- `gandalf_category_delete` removes one entirely, and only when it holds no notes.

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

Do not compact session notes. They are the record of what was known at the time.

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
and an optional status. Keep tags lowercase and hyphenated. Pass refs when linking
notes. Frontmatter keys Gandalf does not manage are left alone.

## The Standards Are The User's

The seeded standards are defaults. The user may rewrite them, add their own, or delete
ones they disagree with, and a deleted standard stays deleted.

Follow what the vault says today, not what you remember a standard saying. A missing
standard is missing on purpose.

## Vault Hygiene

- Do not create empty notes just to satisfy a link.
- Look for an existing answer before asking the user a factual question that earlier
  work may already have settled.
- Each folder's README defines what belongs in it. Keep those definitions there rather
  than restating them elsewhere.
- Active rules belong in the operating contract or a topic. The reasoning behind a
  rule belongs in the correction history, which is not read at startup.
