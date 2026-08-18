package instructions

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/vault"
)

// outdate rewrites a document's body and stamps it with a fingerprint of that
// same body, which is what a vault seeded from an earlier release looks like:
// untouched by the user, behind the binary.
func outdate(t *testing.T, v *vault.Vault, path, body string) {
	t.Helper()

	note, err := v.Read(path)
	if err != nil {
		t.Fatalf("Read %q: %v", path, err)
	}
	note.Body = body
	note.FM.Extra[KeySeed] = HashBody(body)
	if err := v.Write(note); err != nil {
		t.Fatalf("Write %q: %v", path, err)
	}
}

// edit changes a document's body without touching its stamp, which is what a
// user's own correction looks like.
func edit(t *testing.T, v *vault.Vault, path, body string) {
	t.Helper()

	note, err := v.Read(path)
	if err != nil {
		t.Fatalf("Read %q: %v", path, err)
	}
	note.Body = body
	if err := v.Write(note); err != nil {
		t.Fatalf("Write %q: %v", path, err)
	}
}

func TestUpdateAdoptsOutdatedDocuments(t *testing.T) {
	v := seeded(t)
	outdate(t, v, "Gandalf/Diagnostics.md", "# Diagnostics\n\nAn older release said this.\n")

	if got := stateOf(t, v, "Gandalf/Diagnostics.md"); got != StateOutdated {
		t.Fatalf("state before = %q, want %q", got, StateOutdated)
	}

	results, err := Update(v, on(t))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := Updated(results); got != 1 {
		t.Errorf("updated = %d, want 1", got)
	}
	if got := stateOf(t, v, "Gandalf/Diagnostics.md"); got != StateCurrent {
		t.Errorf("state after = %q, want %q", got, StateCurrent)
	}

	note, err := v.Read("Gandalf/Diagnostics.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(note.Content(), "An older release said this.") {
		t.Error("the old text survived")
	}
}

// TestUpdateLeavesEditedDocumentsAlone is the whole constraint: a release must
// never revert a correction, which is why seeding refuses to overwrite in the
// first place.
func TestUpdateLeavesEditedDocumentsAlone(t *testing.T) {
	v := seeded(t)

	const corrected = "# Operating Contract\n\nA rule the user added.\n"
	edit(t, v, "Gandalf/Operating.md", corrected)

	// Changed on both sides: an old release's text plus a local edit.
	const diverged = "# Diagnostics\n\nAn older release said this.\n\nAnd the user added this.\n"
	note, err := v.Read("Gandalf/Diagnostics.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	note.Body = diverged
	note.FM.Extra[KeySeed] = HashBody("# Diagnostics\n\nAn older release said this.\n")
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := Update(v, on(t))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := Updated(results); got != 0 {
		t.Errorf("updated = %d, want 0", got)
	}

	held := HeldBack(results)
	if len(held) != 2 {
		t.Fatalf("held back %d, want 2: %+v", len(held), held)
	}

	for path, want := range map[string]string{
		"Gandalf/Operating.md":   "A rule the user added.",
		"Gandalf/Diagnostics.md": "And the user added this.",
	} {
		note, err := v.Read(path)
		if err != nil {
			t.Fatalf("Read %q: %v", path, err)
		}
		if !strings.Contains(note.Content(), want) {
			t.Errorf("%s lost its local edit:\n%s", path, note.Content())
		}
	}
}

// TestUpdatePreservesMetadata checks that adopting new text does not reset a
// note's identity or discard tags the user added.
func TestUpdatePreservesMetadata(t *testing.T) {
	v := seeded(t)

	note, err := v.Read("Gandalf/Diagnostics.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	created := note.FM.Created
	note.FM.Tags = append(note.FM.Tags, "mine")
	note.Body = "# Diagnostics\n\nAn older release said this.\n"
	note.FM.Extra[KeySeed] = HashBody(note.Body)
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := Update(v, on(t)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	after, err := v.Read("Gandalf/Diagnostics.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.FM.Created != created {
		t.Errorf("created = %v, want %v", after.FM.Created, created)
	}
	if !contains(after.FM.Tags, "mine") {
		t.Errorf("tags = %v, want the user's tag kept", after.FM.Tags)
	}
	if got := version(after); got != Version {
		t.Errorf("version stamp = %d, want %d", got, Version)
	}
}

// TestUpdateOnCurrentVaultChangesNothing keeps the command safe to run twice.
func TestUpdateOnCurrentVaultChangesNothing(t *testing.T) {
	v := seeded(t)

	before := snapshot(t, v)
	results, err := Update(v, on(t))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := Updated(results); got != 0 {
		t.Errorf("updated = %d on a current vault, want 0", got)
	}
	if snapshot(t, v) != before {
		t.Error("Update modified a vault that was already current")
	}
}

// TestUpdateKeepsBacklinks checks that the maintained block survives the note
// being rewritten, since adopting text goes through the same replacement path
// the tools use.
func TestUpdateKeepsBacklinks(t *testing.T) {
	v := seeded(t)
	outdate(t, v, "Gandalf/Memory.md", "# Memory Protocol\n\nAn older release said this.\n")

	if _, err := Update(v, on(t)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	note, err := v.Read("Gandalf/Memory.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(note.Backlinks()) == 0 {
		t.Error("the maintained backlinks block was lost")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
