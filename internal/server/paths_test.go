package server

import (
	"strings"
	"testing"
)

// TestPathsAreRejectedWithGuidance covers the mistake this whole addressing
// scheme exists to prevent. Rejecting a path is not enough on its own: the
// message has to teach the scheme, or a model will retry variations of the
// path instead of switching to refs.
func TestPathsAreRejectedWithGuidance(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Real Work", Tags: []string{"work"}}, &session)

	tests := []struct {
		name    string
		ref     string
		wantRef string // the ref the message should suggest, when it can name one
	}{
		{
			name:    "seeded document by path",
			ref:     "Gandalf/Operating.md",
			wantRef: "topic:operating",
		},
		{
			name:    "standard by path",
			ref:     "Standards/language-go.md",
			wantRef: "standard:language-go",
		},
		{
			name:    "standard by path without extension",
			ref:     "Standards/language-go",
			wantRef: "standard:language-go",
		},
		{
			name:    "session by path",
			ref:     "Sessions/2026/08/" + strings.TrimPrefix(session.Ref, "session:") + ".md",
			wantRef: session.Ref,
		},
		{
			name: "path to a note that does not exist",
			ref:  "Standards/imaginary.md",
		},
		{
			name: "bare filename",
			ref:  "Design.md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := h.callErr("gandalf_note_read", NoteReadInput{Ref: tc.ref})

			if !strings.Contains(msg, "file path") {
				t.Errorf("message does not say the input was a path: %q", msg)
			}
			if !strings.Contains(msg, "addressed by ref") {
				t.Errorf("message does not explain refs: %q", msg)
			}
			if tc.wantRef != "" && !strings.Contains(msg, tc.wantRef) {
				t.Errorf("message does not suggest %q: %q", tc.wantRef, msg)
			}
		})
	}
}

func TestUnknownKindListsTheValidOnes(t *testing.T) {
	h := newHarness(t)

	msg := h.callErr("gandalf_note_read", NoteReadInput{Ref: "diary:today"})
	for _, want := range []string{"unknown kind", "session", "project", "standard", "topic"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q: %q", want, msg)
		}
	}
}

// TestWriteToolsRejectPathsToo checks the guidance is not confined to reads.
func TestWriteToolsRejectPathsToo(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		tool string
		args any
	}{
		{tool: "gandalf_note_append", args: NoteAppendInput{Ref: "Standards/language-go.md", Content: "More."}},
		{tool: "gandalf_note_update", args: NoteUpdateInput{Ref: "Standards/language-go.md", Status: "complete"}},
		{tool: "gandalf_lint", args: LintInput{Ref: "Standards/language-go.md"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			msg := h.callErr(tc.tool, tc.args)
			if !strings.Contains(msg, "standard:language-go") {
				t.Errorf("message does not suggest the ref: %q", msg)
			}
		})
	}
}
