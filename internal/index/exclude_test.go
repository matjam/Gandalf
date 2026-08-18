package index

import (
	"context"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// write puts a note in the vault and returns its path.
func write(t *testing.T, v *vault.Vault, title, body string, indexed *bool) string {
	t.Helper()

	note, err := v.NewNote(vault.NewNoteRequest{
		Type: schema.NoteType("standard"), Title: title,
		Tags: []string{"standards"}, Body: body,
	})
	if err != nil {
		t.Fatalf("NewNote(%q): %v", title, err)
	}
	note.FM.Index = indexed

	if err := v.Write(note); err != nil {
		t.Fatalf("Write(%q): %v", title, err)
	}
	return note.Path
}

// TestExcludedNotesStayOutOfTheIndex covers the frontmatter flag: a note can be
// kept out of search without being hidden from anything else.
func TestExcludedNotesStayOutOfTheIndex(t *testing.T) {
	ctx := context.Background()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open vault: %v", err)
	}

	no := false
	write(t, v, "Searchable", "# Searchable\n\nQuorum reconciliation across replicas.\n", nil)
	write(t, v, "Hidden", "# Hidden\n\nQuorum reconciliation across replicas.\n", &no)

	embedder := embed.Fake{}
	store, err := Open(v.Root(), embedder)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer store.Close()

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	results, err := store.Search(ctx, embedder, Query{Text: "quorum reconciliation"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results at all")
	}
	for _, r := range results {
		if strings.Contains(r.Ref, "hidden") {
			t.Errorf("an excluded note was indexed: %+v", r)
		}
	}
}

// TestExcludingLaterRemovesFromTheIndex checks the flag applies to notes that
// were already indexed, not only to new ones.
func TestExcludingLaterRemovesFromTheIndex(t *testing.T) {
	ctx := context.Background()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open vault: %v", err)
	}
	path := write(t, v, "Regrettable", "# Regrettable\n\nQuorum reconciliation across replicas.\n", nil)

	embedder := embed.Fake{}
	store, err := Open(v.Root(), embedder)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	defer store.Close()

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if count, _ := store.Count(); count == 0 {
		t.Fatal("the note was never indexed")
	}

	note, err := v.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	no := false
	note.FM.Index = &no
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("second Reindex: %v", err)
	}

	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("%d chunks remain after the note was excluded", count)
	}
}

// TestExclusionSurvivesARoundTrip checks the flag is written and read back,
// since a flag that vanished on the next write would silently re-index.
func TestExclusionSurvivesARoundTrip(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open vault: %v", err)
	}

	no := false
	path := write(t, v, "Hidden", "# Hidden\n", &no)

	note, err := v.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if note.Indexed() {
		t.Error("the exclusion did not survive being written and read")
	}

	if err := v.Write(note); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	again, err := v.Read(path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if again.Indexed() {
		t.Error("the exclusion was lost on rewrite")
	}
}

func TestIndexedDefaultsToTrue(t *testing.T) {
	note := &vault.Note{}
	if !note.Indexed() {
		t.Error("a note with no flag is not indexed; searchable should be the default")
	}
}
