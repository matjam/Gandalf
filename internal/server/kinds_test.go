package server

import (
	"slices"
	"strings"
	"testing"
)

// A category answers to two names: the singular a ref carries, and the plural a
// listing asks for. Which one a tool wanted used to differ per tool, with
// nothing in either schema to say so, so the only way to find out was to get it
// wrong. Every tool taking a kind now takes both.

func TestListAcceptsSingularOrPluralKind(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var plural, singular ListOutput
	h.call("list", ListInput{Kind: "sessions"}, &plural)
	h.call("list", ListInput{Kind: "session"}, &singular)

	if plural.Total == 0 {
		t.Fatal("no sessions listed; the fixture is wrong")
	}
	if singular.Total != plural.Total {
		t.Errorf("singular listed %d, plural listed %d", singular.Total, plural.Total)
	}

	// Whichever was asked for, the answer names the plural, so a caller storing
	// the result gets one spelling rather than an echo of what it sent.
	if singular.Kind != "sessions" {
		t.Errorf("kind = %q, want the plural", singular.Kind)
	}
}

func TestNoteNewAcceptsSingularOrPluralKind(t *testing.T) {
	h := newHarness(t)

	var note NoteOutput
	h.call("note_new", NoteNewInput{Kind: "standards", Title: "Language Rust"}, &note)

	if note.Type != "standard" {
		t.Errorf("type = %q, want the singular the category is named by", note.Type)
	}
	if !strings.HasPrefix(note.Ref, "standard:") {
		t.Errorf("ref = %q, want a standard ref", note.Ref)
	}
}

// TestSearchKindsAreNormalised covers the third spelling: the index stores refs,
// so its filter matches the singular. A plural reaching it unchanged matched
// nothing and reported no error, which is the worst of the three failures.
func TestSearchKindsAreNormalised(t *testing.T) {
	h := newHarness(t)
	s := New(h.vault, "test")

	got := s.canonicalKinds([]string{"sessions", "standard", "Projects", "nonesuch"})
	want := []string{"session", "standard", "project", "nonesuch"}

	if !slices.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}
