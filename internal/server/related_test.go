package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tags were symmetric from the start and related links were not: a link could
// be added and never removed. The entries most worth removing are the ones that
// no longer resolve, which is also the case add_related cannot express, since it
// refuses a link it cannot resolve.

func TestNoteUpdateRemovesARelatedLink(t *testing.T) {
	h := newHarness(t)

	var design NoteOutput
	h.call("note_new", NoteNewInput{
		Kind: "project", Facet: "design", Scope: "blitter", Title: "Blitter",
	}, &design)

	var todo NoteOutput
	h.call("note_new", NoteNewInput{
		Kind: "project", Facet: "todo", Scope: "blitter", Title: "Blitter",
		Related: []string{design.Ref},
	}, &todo)

	if len(todo.Related) != 1 {
		t.Fatalf("related = %v, want the design note", todo.Related)
	}

	var updated NoteOutput
	h.call("note_update", NoteUpdateInput{
		Ref: todo.Ref, RemoveRelated: []string{design.Ref},
	}, &updated)

	if len(updated.Related) != 0 {
		t.Errorf("related = %v, want it emptied", updated.Related)
	}

	// The other side's maintained block goes with it: an inbound link that no
	// longer exists must not be left listed as one.
	var read NoteOutput
	h.call("note_read", NoteReadInput{Ref: design.Ref}, &read)
	if strings.Contains(read.Content, todo.Ref) {
		t.Errorf("%s still lists %s as a backlink:\n%s", design.Ref, todo.Ref, read.Content)
	}
}

// TestNoteUpdateRemovesADeadRelatedLink is the case the parameter exists for: a
// note imported from elsewhere, carrying links to notes this vault never had.
func TestNoteUpdateRemovesADeadRelatedLink(t *testing.T) {
	h := newHarness(t)

	// Planted rather than created, because no tool will write this: a link to a
	// note that does not exist is refused on the way in.
	const imported = "---\ntype: standard\ncreated: 2026-01-01\nupdated: 2026-01-01\n" +
		"tags: [imported]\nrelated:\n  - Agent/OS/Memory\nauthor: user\n---\n\n# Imported\n"
	abs := filepath.Join(h.vault.Root(), "Standards", "imported.md")
	if err := os.WriteFile(abs, []byte(imported), 0o644); err != nil {
		t.Fatalf("plant the note: %v", err)
	}

	var updated NoteOutput
	h.call("note_update", NoteUpdateInput{
		Ref: "standard:imported", RemoveRelated: []string{"Agent/OS/Memory"},
	}, &updated)

	if len(updated.Related) != 0 {
		t.Errorf("related = %v, want the dead entry gone", updated.Related)
	}

	var read NoteOutput
	h.call("note_read", NoteReadInput{Ref: "standard:imported"}, &read)
	if len(read.Related) != 0 {
		t.Errorf("related = %v after rereading, want the dead entry gone", read.Related)
	}
}
