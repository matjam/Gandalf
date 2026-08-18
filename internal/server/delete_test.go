package server

import (
	"strings"
	"testing"
)

// TestDeleteRefusesWhenReferenced is the safeguard: deleting a linked note
// would leave dead links scattered through notes nobody is currently reading.
func TestDeleteRefusesWhenReferenced(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)

	var standard NoteOutput
	h.call("note_new", NoteNewInput{
		Kind: "standard", Title: "House Style", Tags: []string{"standards"},
	}, &standard)

	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Wrote it up as [[standard:house-style]].",
	}, nil)

	msg := h.callErr("note_delete", NoteDeleteInput{Ref: standard.Ref})
	if !strings.Contains(msg, session.Ref) {
		t.Errorf("error does not name the referrer %q: %q", session.Ref, msg)
	}
	if !strings.Contains(msg, "dead links") {
		t.Errorf("error does not explain why: %q", msg)
	}

	// The note is still there.
	var read NoteOutput
	h.call("note_read", NoteReadInput{Ref: standard.Ref}, &read)
	if read.Title != "House Style" {
		t.Error("the refused delete removed the note anyway")
	}
}

// TestDeleteListsEveryReferrer keeps the fix to one round trip.
func TestDeleteListsEveryReferrer(t *testing.T) {
	h := newHarness(t)

	var standard NoteOutput
	h.call("note_new", NoteNewInput{
		Kind: "standard", Title: "House Style", Tags: []string{"standards"},
	}, &standard)

	var refs []string
	for _, title := range []string{"First Work", "Second Work"} {
		var session SessionStartOutput
		h.call("session_start", SessionStartInput{Title: title, Tags: []string{"work"}}, &session)
		h.call("note_append", NoteAppendInput{
			Ref:     session.Ref,
			Content: "See [[standard:house-style]].",
		}, nil)
		refs = append(refs, session.Ref)
	}

	msg := h.callErr("note_delete", NoteDeleteInput{Ref: standard.Ref})
	for _, ref := range refs {
		if !strings.Contains(msg, ref) {
			t.Errorf("error omits referrer %q: %q", ref, msg)
		}
	}
}

func TestDeleteUnreferencedNote(t *testing.T) {
	h := newHarness(t)

	var standard NoteOutput
	h.call("note_new", NoteNewInput{
		Kind: "standard", Title: "Throwaway", Tags: []string{"standards"},
	}, &standard)

	var out NoteDeleteOutput
	h.call("note_delete", NoteDeleteInput{Ref: standard.Ref}, &out)

	if !out.Deleted {
		t.Error("deleted = false")
	}
	if h.vault.Exists("Standards/throwaway.md") {
		t.Error("the file is still on disk")
	}
	if msg := h.callErr("note_read", NoteReadInput{Ref: standard.Ref}); msg == "" {
		t.Error("the deleted note is still readable")
	}
}

// TestDeletingALinkerTrimsBacklinks checks the other direction: removing a note
// that pointed at things must clean up what it left behind.
func TestDeletingALinkerTrimsBacklinks(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]].",
	}, nil)

	var out NoteDeleteOutput
	h.call("note_delete", NoteDeleteInput{Ref: session.Ref}, &out)

	if len(out.Rebuilt) == 0 {
		t.Error("no notes reported as having their backlinks updated")
	}
	for _, ref := range out.Rebuilt {
		if strings.Contains(ref, "/") {
			t.Errorf("a path leaked into the result: %q", ref)
		}
	}
}

func TestDeleteRejects(t *testing.T) {
	h := newHarness(t)

	writeUnmanaged(t, h, "Personal/Something.md")

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "a note outside the filing conventions",
			ref:  "path:Personal/Something",
			want: "filing conventions",
		},
		{
			name: "a note that does not exist",
			ref:  "standard:imaginary",
			want: "does not exist",
		},
		{
			name: "a file path",
			ref:  "Standards/language-go.md",
			want: "file path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if msg := h.callErr("note_delete", NoteDeleteInput{Ref: tc.ref}); !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tc.want)
			}
		})
	}
}

// TestVaultStaysCleanAfterDeletion checks deletion does not leave the vault in
// a state its own linter rejects.
func TestVaultStaysCleanAfterDeletion(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]].",
	}, nil)
	h.call("note_delete", NoteDeleteInput{Ref: session.Ref}, nil)

	var out LintOutput
	h.call("lint", LintInput{}, &out)
	if !out.Clean {
		t.Errorf("deletion left findings: %+v", out.Findings)
	}
}
