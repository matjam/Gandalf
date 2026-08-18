# Memory Protocol

How this vault is used during a session: what to read before starting, what to write
as you go, and what shape notes take. The tools own note metadata — you supply prose.

## Session Startup

Before substantive work:

1. Search prior sessions for work on the same topic with `gandalf_search`.
2. For a known project, read its design, decisions, and todo notes under
   `Projects/<name>/`.
3. Distil what you found into a few bullets, surface them to the user, and confirm
   anything stale or consequential before relying on it.
4. Create the session note with `gandalf_session_start` before proposing or writing
   code. One note per logical unit of work — not one per conversation, not one per day.

Read-only work creates no notes. Search freely, write nothing.

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

Projects live under `Projects/<name>/`.

- **Design** describes the current state only. Delete superseded history rather than
  letting it accumulate; keep it short enough to stay read.
- **Decisions** is append-only. Record significant decisions as they are made, with
  their context and the tradeoffs accepted.
- **Todo** is the durable backlog. Add concrete items whenever the user asks to come
  back to something, or when the work turns up something that needs a later revisit.
  Link to detailed notes rather than duplicating them. Move finished work to a
  completed section instead of leaving status to be inferred.

Before starting work on a project, check its design note for claims that have gone
stale.

## Note Metadata

Every note carries the same frontmatter: a type, created and updated dates, tags,
related links, an author, and an optional status. You do not write this block by hand
— `gandalf_note_new` generates it and `gandalf_note_update` maintains it, including
bumping the updated date.

Keep tags lowercase and hyphenated so they stay usable as filters. Put
cross-references in the related list as wikilinks. Run `gandalf_lint` when something
looks off; it reports schema violations and links pointing nowhere.

Frontmatter keys the tool does not manage are left alone, so anything your editor adds
survives untouched.

## Vault Hygiene

- Do not create empty notes just to satisfy a link.
- Search before asking the user a factual question that earlier work may already
  answer.
- Each folder's README defines what belongs in it. Keep those definitions in the
  README rather than restating them elsewhere.
- Active behavioural rules belong in the operating contract or a topic. The reasoning
  behind a rule belongs in the correction history, where it does not have to be read
  every session.
