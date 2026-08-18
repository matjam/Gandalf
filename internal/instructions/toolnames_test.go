package instructions

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// TestRenameToolsRewritesAnEditedDocument is the case the whole migration
// exists for. Update will not touch a document the user has changed, so
// without this an edited contract keeps naming tools the binary dropped.
func TestRenameToolsRewritesAnEditedDocument(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	// Edit the contract the way a correction would, using the old names.
	note, err := v.Read("Gandalf/Operating.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	note.Append("House Rules", "- Record it with `gandalf_correct`, then read it back with `gandalf_note_read`.")
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// It must now be a document Update refuses to adopt, or this test is
	// exercising the easy path.
	statuses, err := Doctor(v)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var edited bool
	for _, s := range statuses {
		if s.Doc.Path == "Gandalf/Operating.md" {
			edited = s.State == StateModified || s.State == StateDiverged
		}
	}
	if !edited {
		t.Fatal("the edited contract is not in a state Update declines to handle")
	}

	renamed, err := RenameTools(v, schema.Today())
	if err != nil {
		t.Fatalf("RenameTools: %v", err)
	}
	if len(renamed) == 0 {
		t.Fatal("nothing was renamed")
	}

	after, err := v.Read("Gandalf/Operating.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(after.Body, "gandalf_") {
		t.Error("an old tool name survived the rename")
	}
	if !strings.Contains(after.Body, "`correct`") || !strings.Contains(after.Body, "`note_read`") {
		t.Error("the rename did not produce the current names")
	}

	// The user's own words are untouched: only identifiers move.
	if !strings.Contains(after.Body, "House Rules") {
		t.Error("the rename disturbed the user's edit")
	}

	// Running it again changes nothing.
	again, err := RenameTools(v, schema.Today())
	if err != nil {
		t.Fatalf("RenameTools: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("rename is not idempotent: %v", again)
	}
}

// TestRenameToolsLeavesUserNotesAlone keeps the migration to the documents
// that function as instructions. A session note naming an old tool is a record
// of what happened, and editing history buys nothing.
func TestRenameToolsLeavesUserNotesAlone(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	const body = "# A Session\n\nCalled `gandalf_note_append` and it worked.\n"
	note := &vault.Note{
		Path: "Sessions/2026/08/2026-08-18-a-session.md",
		FM: schema.Frontmatter{
			Type:    "session",
			Created: schema.Today(),
			Updated: schema.Today(),
			Tags:    []string{"session"},
			Author:  "agent",
		},
		Body: body,
	}
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := RenameTools(v, schema.Today()); err != nil {
		t.Fatalf("RenameTools: %v", err)
	}

	after, err := v.Read(note.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(after.Body, "gandalf_note_append") {
		t.Error("the migration rewrote a user's session note")
	}
}

// TestRenamedToolsCoversTheToolSurface guards against the map going stale: a
// tool renamed later without an entry here leaves old vaults pointing at
// nothing.
func TestRenamedToolsCoversTheToolSurface(t *testing.T) {
	for old, current := range renamedTools {
		if !strings.HasPrefix(old, "gandalf_") {
			t.Errorf("%q is not an old-style name", old)
		}
		if strings.HasPrefix(current, "gandalf_") {
			t.Errorf("%q still carries the prefix", current)
		}
		if strings.TrimPrefix(old, "gandalf_") != current {
			t.Errorf("%q -> %q is not a prefix drop", old, current)
		}
	}
}
