package server

import (
	"strings"
	"testing"
)

// design creates a project design note, which is the replaceable case.
func design(h *harness, content string) string {
	h.t.Helper()

	var out NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind:    "project",
		Scope:   "gandalf",
		Facet:   "design",
		Title:   "Gandalf — Design",
		Content: content,
		Tags:    []string{"gandalf"},
	}, &out)

	return out.Ref
}

const designBody = `## Shape

Old shape.

## Verification

Old verification.

## Open Questions

Still open.`

func TestNoteReplaceSection(t *testing.T) {
	h := newHarness(t)
	ref := design(h, designBody)

	var out NoteReplaceOutput
	h.call("gandalf_note_replace", NoteReplaceInput{
		Ref:     ref,
		Section: "Verification",
		Content: "New verification.",
	}, &out)

	if strings.TrimSpace(out.Removed) != "Old verification." {
		t.Errorf("removed = %q, want %q", out.Removed, "Old verification.")
	}
	if !strings.Contains(out.Content, "## Verification\n\nNew verification.") {
		t.Errorf("content:\n%s", out.Content)
	}
	for _, keep := range []string{"Old shape.", "Still open."} {
		if !strings.Contains(out.Content, keep) {
			t.Errorf("replacement disturbed %q:\n%s", keep, out.Content)
		}
	}
}

func TestNoteReplaceAnchored(t *testing.T) {
	h := newHarness(t)
	ref := design(h, designBody)

	var out NoteReplaceOutput
	h.call("gandalf_note_replace", NoteReplaceInput{
		Ref:     ref,
		From:    "## Shape",
		To:      "## Verification",
		Content: "\n\nBounded.\n\n",
	}, &out)

	if !strings.Contains(out.Removed, "Old shape.") {
		t.Errorf("removed = %q", out.Removed)
	}
	if !strings.Contains(out.Content, "## Shape\n\nBounded.\n\n## Verification") {
		t.Errorf("content:\n%s", out.Content)
	}
}

func TestNoteReplaceWholeBody(t *testing.T) {
	h := newHarness(t)
	ref := design(h, designBody)

	var out NoteReplaceOutput
	h.call("gandalf_note_replace", NoteReplaceInput{Ref: ref, Content: "# Gandalf\n\nStarted over."}, &out)

	if !strings.Contains(out.Removed, "Old shape.") {
		t.Errorf("removed = %q", out.Removed)
	}
	if strings.Contains(out.Content, "Old shape.") {
		t.Errorf("content still holds the old body:\n%s", out.Content)
	}
}

// TestNoteReplaceRefusesAppendOnly is the rule the tool exists to enforce: a
// record of what was known at the time cannot be tidied up.
func TestNoteReplaceRefusesAppendOnly(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Some Work", Tags: []string{"work"}}, &session)
	h.call("gandalf_note_append", NoteAppendInput{Ref: session.Ref, Content: "What I knew at the time."}, nil)

	msg := h.callErr("gandalf_note_replace", NoteReplaceInput{
		Ref:     session.Ref,
		Content: "# Tidier\n\nA better account.",
	})
	if !strings.Contains(msg, "append-only") || !strings.Contains(msg, "gandalf_note_append") {
		t.Errorf("error does not explain the rule or the alternative: %s", msg)
	}

	var note NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: session.Ref}, &note)
	if !strings.Contains(note.Content, "What I knew at the time.") {
		t.Error("the session note was modified anyway")
	}
}

// TestNoteReplaceRefusesDecisions checks the facet override: a project's
// design note is replaceable while its decisions log, in the same category,
// is not.
func TestNoteReplaceRefusesDecisions(t *testing.T) {
	h := newHarness(t)

	var out NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "project", Scope: "gandalf", Facet: "decisions",
		Title: "Gandalf — Decisions", Content: "## 2026-08-18 — Something\n\nDecided.",
	}, &out)

	if msg := h.callErr("gandalf_note_replace", NoteReplaceInput{
		Ref: out.Ref, Section: "2026-08-18 — Something", Content: "Undecided.",
	}); !strings.Contains(msg, "append-only") {
		t.Errorf("decisions log was not protected: %s", msg)
	}
}

// TestNoteReplaceRequiresRead covers a note that exists but whose text the
// caller has not seen. A seeded standard is exactly that on a fresh session.
func TestNoteReplaceRequiresRead(t *testing.T) {
	h := newHarness(t)

	msg := h.callErr("gandalf_note_replace", NoteReplaceInput{
		Ref:     "standard:language-go",
		Section: "Tooling",
		Content: "Run gofmt.",
	})
	if !strings.Contains(msg, "gandalf_note_read") {
		t.Fatalf("error does not say to read the note first: %s", msg)
	}

	// Reading it is enough to unblock the same call.
	var note NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "standard:language-go"}, &note)

	var out NoteReplaceOutput
	h.call("gandalf_note_replace", NoteReplaceInput{
		Ref:     "standard:language-go",
		Section: "Tooling",
		Content: "Run gofmt.",
	}, &out)

	if !strings.Contains(out.Content, "## Tooling\n\nRun gofmt.") {
		t.Errorf("content:\n%s", out.Content)
	}
	if !strings.Contains(out.Removed, "goimports") {
		t.Errorf("removed = %q, want the shipped tooling section", out.Removed)
	}
}

func TestNoteReplaceBadRequests(t *testing.T) {
	h := newHarness(t)
	ref := design(h, designBody)

	tests := []struct {
		name string
		in   NoteReplaceInput
		want string
	}{
		{
			name: "section and anchors together",
			in:   NoteReplaceInput{Ref: ref, Section: "Shape", From: "a", To: "b", Content: "x"},
			want: "not both",
		},
		{
			name: "opening anchor without a closing one",
			in:   NoteReplaceInput{Ref: ref, From: "## Shape", Content: "x"},
			want: "needs both from and to",
		},
		{
			name: "emptying the whole note",
			in:   NoteReplaceInput{Ref: ref, Content: "   "},
			want: "gandalf_note_delete",
		},
		{
			name: "no such section",
			in:   NoteReplaceInput{Ref: ref, Section: "Deployment", Content: "x"},
			want: "no section with that heading",
		},
		{
			name: "ambiguous anchor",
			in:   NoteReplaceInput{Ref: ref, From: "Old", To: "## Open Questions", Content: "x"},
			want: "appears 2 times",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := h.callErr("gandalf_note_replace", tt.in); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tt.want)
			}
		})
	}

	// None of that touched the note.
	var note NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: ref}, &note)
	if !strings.Contains(note.Content, "Old shape.") {
		t.Error("a refused replacement modified the note")
	}
}

// TestNoteReplaceMaintainsBacklinks checks that dropping a link during a
// replacement updates the note it used to point at.
func TestNoteReplaceMaintainsBacklinks(t *testing.T) {
	h := newHarness(t)

	var decisions NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "project", Scope: "gandalf", Facet: "decisions",
		Title: "Gandalf — Decisions", Content: "## A decision\n\nMade.",
	}, &decisions)

	ref := design(h, "## Shape\n\nSee [[project:gandalf:decisions]].\n\n## Verification\n\nTested.")

	var linked NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: decisions.Ref}, &linked)
	if !strings.Contains(linked.Content, "project:gandalf:design") {
		t.Fatalf("the link was never recorded:\n%s", linked.Content)
	}

	h.call("gandalf_note_replace", NoteReplaceInput{
		Ref: ref, Section: "Shape", Content: "No links here.",
	}, nil)

	h.call("gandalf_note_read", NoteReadInput{Ref: decisions.Ref}, &linked)
	if strings.Contains(linked.Content, "project:gandalf:design") {
		t.Errorf("the backlink survived the link being removed:\n%s", linked.Content)
	}
}

// TestNoteReplaceRefusesDeadLinks holds replacement to the same rule as every
// other write: Gandalf does not record a link it knows points at nothing.
func TestNoteReplaceRefusesDeadLinks(t *testing.T) {
	h := newHarness(t)
	ref := design(h, designBody)

	if msg := h.callErr("gandalf_note_replace", NoteReplaceInput{
		Ref: ref, Section: "Verification", Content: "See [[project:nonexistent:design]].",
	}); !strings.Contains(msg, "do not exist") {
		t.Errorf("error = %q", msg)
	}
}
