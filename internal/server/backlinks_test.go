package server

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/vault"
)

// TestBacklinksAppearInTheLinkedNote is the headline behaviour: linking from A
// to B leaves a record in B, visible to anyone reading B and to any model that
// reads it later.
func TestBacklinksAppearInTheLinkedNote(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Linking Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]] throughout.",
	}, nil)

	raw := diskOf(t, h, "standard:language-go")
	if !strings.Contains(raw, vault.BacklinksHeading) {
		t.Fatalf("no backlinks block in the linked note:\n%s", raw)
	}
	if !strings.Contains(raw, "[[Sessions/") {
		t.Errorf("the backlink does not point at the session:\n%s", raw)
	}

	// It is a path on disk, so Obsidian resolves it.
	if strings.Contains(raw, "session:") {
		t.Errorf("a ref leaked into the backlinks block:\n%s", raw)
	}
}

// TestBacklinksDoNotRecurse guards the trap: links inside a backlinks block are
// not the note's own outgoing links, so they must not generate backlinks of
// their own.
func TestBacklinksDoNotRecurse(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]].",
	}, nil)

	// The standard now links back at the session. If that counted as an
	// outgoing link, the session would gain a backlink to the standard.
	sessionRaw := diskOf(t, h, session.Ref)
	if strings.Contains(sessionRaw, vault.BacklinksHeading) {
		t.Errorf("the linking note gained a backlinks block:\n%s", sessionRaw)
	}

	// Repeated rebuilds must reach a fixed point rather than growing.
	before := diskOf(t, h, "standard:language-go")
	for range 3 {
		if _, err := h.vault.RebuildBacklinks(); err != nil {
			t.Fatalf("RebuildBacklinks: %v", err)
		}
	}
	if after := diskOf(t, h, "standard:language-go"); after != before {
		t.Errorf("rebuilding changed the vault:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestBacklinksDoNotDisturbSeedFingerprints is the consequence that would
// otherwise make doctor lie: linking to a seeded standard must not report it as
// modified by the user.
func TestBacklinksDoNotDisturbSeedFingerprints(t *testing.T) {
	h := newHarness(t)

	before, err := instructions.Doctor(h.vault)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if got := instructions.Count(before, instructions.StateCurrent); got != len(instructions.Docs()) {
		t.Fatalf("%d current before linking, want all", got)
	}

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]] and [[topic:shipping]].",
	}, nil)

	after, err := instructions.Doctor(h.vault)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	for _, s := range after {
		if s.State != instructions.StateCurrent {
			t.Errorf("%s is %q after being linked to, want current", s.Doc.Path, s.State)
		}
	}
}

// TestAppendGoesAboveTheBacklinks checks content is not filed under a heading
// it has nothing to do with, and is not destroyed by the next rebuild.
func TestAppendGoesAboveTheBacklinks(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]].",
	}, nil)

	// The standard has a backlinks block; append to it and check where the
	// content lands.
	h.call("note_append", NoteAppendInput{
		Ref:     "standard:language-go",
		Heading: "House Rule",
		Content: "Prefer errors.Is over string matching.",
	}, nil)

	raw := diskOf(t, h, "standard:language-go")
	rule := strings.Index(raw, "House Rule")
	block := strings.Index(raw, vault.BacklinksHeading)

	switch {
	case rule < 0:
		t.Fatalf("the appended content is missing:\n%s", raw)
	case block < 0:
		t.Fatalf("the backlinks block is missing:\n%s", raw)
	case rule > block:
		t.Errorf("content was appended below the backlinks block:\n%s", raw)
	}

	// And it survives the next rebuild.
	if _, err := h.vault.RebuildBacklinks(); err != nil {
		t.Fatalf("RebuildBacklinks: %v", err)
	}
	if !strings.Contains(diskOf(t, h, "standard:language-go"), "Prefer errors.Is") {
		t.Error("rebuilding destroyed appended content")
	}
}

// TestBacklinksFollowFrontmatterRelations checks related entries count as
// links, not only prose wikilinks.
func TestBacklinksFollowFrontmatterRelations(t *testing.T) {
	h := newHarness(t)

	h.call("session_start", SessionStartInput{
		Title: "Related Work", Tags: []string{"work"},
		Related: []string{"standard:privacy"},
	}, nil)

	if raw := diskOf(t, h, "standard:privacy"); !strings.Contains(raw, vault.BacklinksHeading) {
		t.Errorf("a frontmatter relation produced no backlink:\n%s", raw)
	}
}

// TestBacklinksAreRemovedWhenLinksGo checks the block shrinks as well as grows.
func TestBacklinksAreRemovedWhenLinksGo(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]].",
	}, nil)

	if !strings.Contains(diskOf(t, h, "standard:language-go"), vault.BacklinksHeading) {
		t.Fatal("no backlink to remove")
	}

	// Delete the linking note; the backlink must go with it.
	h.call("note_delete", NoteDeleteInput{Ref: session.Ref}, nil)

	if raw := diskOf(t, h, "standard:language-go"); strings.Contains(raw, vault.BacklinksHeading) {
		t.Errorf("the backlink outlived the note that made it:\n%s", raw)
	}
}

func TestSeededVaultStaysCleanWithBacklinks(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{
		Title: "Linked Work", Tags: []string{"work"},
		Related: []string{"topic:operating"},
	}, &session)
	h.call("note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Following [[standard:code-quality]] and [[standard:language-go]].",
	}, nil)

	var out LintOutput
	h.call("lint", LintInput{}, &out)
	if !out.Clean {
		t.Errorf("backlinks left the vault unclean: %+v", out.Findings)
	}
}
