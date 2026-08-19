package server

import (
	"strings"
	"testing"
)

// Every tool that changes the vault takes a reason, which becomes the body of
// the commit it makes. The message says which tool touched which note; the
// reason says why, and is the only part of a history that answers the question
// a history is usually opened to answer.

func TestTheReasonForAChangeReachesItsHistory(t *testing.T) {
	h := newGitHarness(t)

	const created = "start the design note now that the shape is settled"
	var note NoteOutput
	h.call("note_new", map[string]any{
		"kind":    "project",
		"scope":   "gandalf",
		"facet":   "design",
		"title":   "Gandalf — Design",
		"content": designBody,
		"reason":  created,
	}, &note)

	const replaced = "verification section described the old test layout"
	h.call("note_replace", map[string]any{
		"ref":     note.Ref,
		"section": "Verification",
		"content": "New verification.",
		"reason":  replaced,
	}, nil)

	got := history(h, note.Ref)
	if len(got.Commits) < 2 {
		t.Fatalf("got %d commits, want at least 2", len(got.Commits))
	}

	// Newest first, so the replacement is the one at the front.
	if got.Commits[0].Reason != replaced {
		t.Errorf("reason = %q, want %q", got.Commits[0].Reason, replaced)
	}
	if got.Commits[1].Reason != created {
		t.Errorf("reason = %q, want %q", got.Commits[1].Reason, created)
	}

	// The message is still what it was: the reason is added to a commit, not
	// substituted for the record of what changed.
	if !strings.Contains(got.Commits[0].Message, "note replace") {
		t.Errorf("message = %q, want it to name the tool", got.Commits[0].Message)
	}
}

func TestAVaultHistoryCarriesReasonsToo(t *testing.T) {
	h := newGitHarness(t)

	const reason = "open the session note for the versioning work"
	h.call("session_start", map[string]any{
		"title":  "Note versioning",
		"reason": reason,
	}, nil)

	var got HistoryOutput
	h.call("history", HistoryInput{}, &got)

	for _, commit := range got.Commits {
		if commit.Reason == reason {
			return
		}
	}
	t.Errorf("no commit in the vault history carries the reason: %+v", got.Commits)
}

// TestChangingTheVaultNeedsAReason is the requirement itself. An optional
// field is the one a model leaves out, which would leave every history saying
// only that a note was changed.
func TestChangingTheVaultNeedsAReason(t *testing.T) {
	h := newGitHarness(t)

	var note NoteOutput
	h.call("note_new", NoteNewInput{
		Kind:    "project",
		Scope:   "gandalf",
		Facet:   "design",
		Title:   "Gandalf — Design",
		Content: designBody,
	}, &note)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"session_start", map[string]any{"title": "Some work"}},
		{"note_new", map[string]any{
			"kind": "project", "scope": "other", "facet": "design",
			"title": "Other — Design", "content": "Body.",
		}},
		{"note_append", map[string]any{"ref": note.Ref, "content": "More."}},
		{"note_replace", map[string]any{"ref": note.Ref, "section": "Verification", "content": "New."}},
		{"note_update", map[string]any{"ref": note.Ref, "add_tags": []string{"versioning"}}},
		{"note_delete", map[string]any{"ref": note.Ref}},
		{"note_restore", map[string]any{"ref": note.Ref, "commit": "HEAD"}},
		{"category_retire", map[string]any{"name": "meeting"}},
		{"category_delete", map[string]any{"name": "meeting"}},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			// The schema marks the field required, so a call with no reason at
			// all is refused before the handler sees it. What matters is that
			// it is refused and that the refusal names the field.
			msg := h.callErr(c.tool, c.args)
			if !strings.Contains(msg, "reason") {
				t.Errorf("error = %q, want it to ask for a reason", msg)
			}
		})
	}
}

// TestABlankReasonIsNotAReason covers a caller that sends the field but leaves
// it empty, which a required schema alone does not prevent.
func TestABlankReasonIsNotAReason(t *testing.T) {
	h := newGitHarness(t)

	msg := h.callErr("session_start", map[string]any{
		"title":  "Some work",
		"reason": "   \n\t ",
	})
	if !strings.Contains(msg, "reason is required") {
		t.Errorf("error = %q, want it to ask for a reason", msg)
	}
}

// TestAVaultWithoutGitStillTakesReasons keeps the requirement from depending on
// where the reason ends up: a vault with no repository accepts the change and
// simply has nowhere to record why.
func TestAVaultWithoutGitStillTakesReasons(t *testing.T) {
	h := newHarness(t)

	var out SessionStartOutput
	h.call("session_start", map[string]any{
		"title":  "Some work",
		"reason": "check the tools work without a repository",
	}, &out)

	if !out.Created {
		t.Error("session note was not created")
	}
}
