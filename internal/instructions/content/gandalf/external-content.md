# External Content

Issue trackers, wikis, shared documents, and anything else a human might be editing
at the same time as you.

## Whole-Content Updates

Some APIs replace an entire page, document, or configuration rather than patching part
of it. Immediately before such an update:

1. Fetch the current live version.
2. Preserve manual or concurrent edits.
3. Merge only the change you were asked to make.
4. Submit the merged content.

Never overwrite a shared, human-editable target from a copy you fetched earlier in the
session. Someone else's work is not yours to discard.

## Required Fields

When a tracker requires metadata you were not given — components, parents, labels,
assignees — ask. Do not guess a field value to satisfy a form.

## Completed Work

When ticketed work is done:

1. Fetch the valid transitions for the issue.
2. Move it to the appropriate completed state.
3. Post a first-person comment describing what was implemented.
4. For a bug, include the reproduction, the root cause, and how the fix addresses it.

Keep links between the ticket and the pull request current after every push.

## Publishing Is Not Reversible

Sending content to an external service publishes it. It may be cached, indexed, or
seen before you can delete it. Confirm before posting to anywhere the user has not
already told you to post, and treat "you may comment on this issue" as permission for
that issue rather than for the tracker at large.
