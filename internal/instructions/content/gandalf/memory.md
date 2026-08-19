# Memory Protocol

How this vault is used during a session: what to read before starting, what to write as
you go, and what shape notes take. The tools own note metadata and filing — you supply
prose.

What each tool does is on the tool itself, and `boot` returns the rest of the mechanics:
how this vault's notes are addressed, which of them may be rewritten, and the standing
conventions. None of that is repeated here, because this document cannot be corrected by
a release once it is in your vault — what belongs here is what stays true as the tools
change.

## Session Startup

Before substantive work:

1. Call `boot`. It returns this protocol, the operating contract, the topics available on
   demand, how notes are addressed in this vault, and any session note still open today.
2. If a session note is already open and this is the same unit of work, continue it
   rather than starting another.
3. Look for prior work on the same topic before re-deciding anything. Searching finds
   notes by meaning, so it turns up work described in words you did not think to use;
   listing shows what exists by name when you already know what you are after. For a
   known project, read its design, decisions, and todo notes.
4. Distil what you found into a few bullets, surface them to the user, and confirm
   anything stale or consequential before relying on it.
5. Open the session note before proposing or writing code. One note per logical unit of
   work — not one per conversation, not one per day. Hold the ref it returns; if you lose
   it, `boot` hands it back.

Read-only work creates no notes. Read freely, write nothing.

A search answered while the index is still building is a partial answer: it can miss a
note that exists. `boot` says whether the index is ready, and so does every result, so
confirm against a listing before concluding the vault is silent on something.

Never write a frontmatter block by hand, never choose a filename, never open a vault file
in an editor.

## What May Be Rewritten

Each category declares whether its notes may be rewritten in place, and `boot` reports
which is which. The distinction is what the note is for, not how careful you are being.

A chronological record — a session, a decisions log, a meeting — is worth exactly what it
recorded at the time, so add to it and never tidy it. A current-state document — a design
note, a backlog, a standard — fails by going stale rather than by losing anything, so
rewrite the part that is wrong.

A tool that would rewrite a chronological record refuses by default. That refusal is a
default and not a seal: force it when you are repairing a defect — a missing title, a
broken link, prose mangled on import — rather than revising what the note says happened.
The question is whether the note is wrong about its own subject or merely inconvenient to
read now. Every change is committed as it is made, so a forced rewrite is one revert
away; the reason to be careful is the record, not the recoverability. Repairing a note
through the tools is always better than editing the file behind their back, which is what
the alternative actually is.

Even where replacement is allowed, prefer the narrowest edit that does the job: a section
rather than the whole body, a span rather than a section. Read the note first, and check
the text the tool reports removing against what you meant to remove.

Frontmatter is never yours to edit, in any category.

## Session Notes

Update the session note as the work happens, not at the end. Capture:

- What the work is trying to achieve, and why.
- Decisions and the reasoning behind them.
- Alternatives considered and why they were rejected.
- Assumptions that may need revisiting.
- Context that would be hard to reconstruct from the code alone.
- For environment-specific changes, the difference between what the change is intended to
  do generally and what was actually verified where. Record the environment and machine.
  Do not imply a change is live somewhere it was not verified.

Do not compact session notes. They are the record of what was known at the time.

## Project Notes

A project keeps up to three notes, and each is used differently:

- **Design** describes the current state only. Rewrite the section that has gone stale
  rather than appending a correction beneath it; keep the note short enough to stay read.
- **Decisions** is append-only. Record significant decisions as they are made, with their
  context and the tradeoffs accepted. Supersede an old entry with a new one rather than
  rewriting it.
- **Todo** is the durable backlog. Add concrete items whenever the user asks to come back
  to something, or when the work turns up something needing a later revisit. Move
  finished work into a completed section instead of leaving status to be inferred.

Before starting work on a project, check its design note for claims that have gone stale.

## Categories

A category is a kind of note: what it is called, where its notes are filed, and how they
are addressed. The vault declares its own, so what kinds of note it keeps is the user's
decision rather than Gandalf's.

Declaring a new one changes how the vault is organised: ask first. Do not invent a
category to avoid deciding where a note belongs. Retiring a category stops new notes being
filed there while leaving the existing ones fully usable, which is the answer whenever
notes exist.

## Metadata

Every note carries a type, created and updated dates, tags, related links, an author, and
an optional status. Keep tags lowercase and hyphenated. Pass refs when linking notes.
Frontmatter keys Gandalf does not manage are left alone.

## The Standards Are The User's

The seeded standards are defaults. The user may rewrite them, add their own, or delete
ones they disagree with, and a deleted standard stays deleted.

Follow what the vault says today, not what you remember a standard saying. A missing
standard is missing on purpose.

## Vault Hygiene

- Do not create empty notes just to satisfy a link.
- Look for an existing answer before asking the user a factual question that earlier work
  may already have settled.
- Each folder's README defines what belongs in it. Keep those definitions there rather
  than restating them elsewhere.
- Active rules belong in the operating contract or a topic. The reasoning behind a rule
  belongs in the correction history, which is not read at startup.
