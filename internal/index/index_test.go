package index

import (
	"context"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// fixture returns a vault with a few notes and an index over it.
func fixture(t *testing.T) (*vault.Vault, *Store, embed.Embedder) {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open vault: %v", err)
	}

	notes := []struct{ title, body string }{
		{
			title: "Cache Invalidation",
			body: "# Cache Invalidation\n\n## Strategy\n\nEntries expire after a fixed window " +
				"and are refreshed lazily on the next read.\n\n## Pitfalls\n\nA stampede occurs when many " +
				"readers miss at once.\n",
		},
		{
			title: "Database Migrations",
			body: "# Database Migrations\n\n## Ordering\n\nMigrations run before the service accepts " +
				"traffic, and refuse to start when one cannot be applied.\n",
		},
		{
			title: "Error Handling",
			body: "# Error Handling\n\n## Wrapping\n\nErrors travel up with context and are logged once, " +
				"at the boundary.\n",
		},
	}

	for _, n := range notes {
		note, err := v.NewNote(vault.NewNoteRequest{
			Type: schema.NoteType("standard"), Title: n.title,
			Tags: []string{"standards"}, Body: n.body,
		})
		if err != nil {
			t.Fatalf("NewNote(%q): %v", n.title, err)
		}
		if err := v.Write(note); err != nil {
			t.Fatalf("Write(%q): %v", n.title, err)
		}
	}

	embedder := embed.Fake{}
	store, err := Open(v.Root(), embedder)
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return v, store, embedder
}

// namer addresses notes the way the vault does.
func namer(v *vault.Vault) Namer {
	return func(path string) string { return v.RefFor(path).String() }
}

func TestReindexAndSearch(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	report, err := Reindex(ctx, v, store, embedder, namer(v))
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if report.Indexed != 3 {
		t.Errorf("indexed %d notes, want 3", report.Indexed)
	}
	if report.Chunks == 0 {
		t.Fatal("no chunks indexed")
	}

	results, err := store.Search(ctx, embedder, Query{Text: "cache stampede on read"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !strings.HasPrefix(results[0].Ref, "standard:cache-invalidation") {
		t.Errorf("top result = %q, want the cache note", results[0].Ref)
	}
	if results[0].Heading == "" {
		t.Error("the result does not say which section matched")
	}
	if results[0].Score <= 0 {
		t.Errorf("score = %v", results[0].Score)
	}
}

// TestReindexIsIncremental checks the expensive part is skipped for notes that
// have not changed.
func TestReindexIsIncremental(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	report, err := Reindex(ctx, v, store, embedder, namer(v))
	if err != nil {
		t.Fatalf("second Reindex: %v", err)
	}
	if report.Indexed != 0 {
		t.Errorf("re-indexed %d unchanged notes", report.Indexed)
	}
	if report.Unchanged != 3 {
		t.Errorf("unchanged = %d, want 3", report.Unchanged)
	}

	// Changing one note re-indexes that note only.
	note, err := v.ReadRef(mustRef(t, v, "standard:error-handling"))
	if err != nil {
		t.Fatalf("ReadRef: %v", err)
	}
	note.Append("Panics", "Avoid panicking in library code.")
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if report, err = Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("third Reindex: %v", err)
	}
	if report.Indexed != 1 || report.Unchanged != 2 {
		t.Errorf("indexed %d, unchanged %d; want 1 and 2", report.Indexed, report.Unchanged)
	}
}

func TestReindexForgetsDeletedNotes(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	before, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}

	if err := v.Delete("Standards/error-handling.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	report, err := Reindex(ctx, v, store, embedder, namer(v))
	if err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if report.Removed != 1 {
		t.Errorf("removed = %d, want 1", report.Removed)
	}
	if report.Chunks >= before {
		t.Errorf("chunks = %d, want fewer than %d", report.Chunks, before)
	}

	results, err := store.Search(ctx, embedder, Query{Text: "errors travel up with context"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if strings.HasPrefix(r.Ref, "standard:error-handling") {
			t.Error("a deleted note is still searchable")
		}
	}
}

func TestSearchFiltersByKind(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	results, err := store.Search(ctx, embedder, Query{Text: "migrations", Kinds: []string{"session"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results for a category with no notes", len(results))
	}

	results, err = store.Search(ctx, embedder, Query{Text: "migrations", Kinds: []string{"standard"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("no results when filtering to the right category")
	}
}

func TestSearchLimit(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	results, err := store.Search(ctx, embedder, Query{Text: "the", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("got %d results, want at most 2", len(results))
	}

	if _, err := store.Search(ctx, embedder, Query{Text: "   "}); err == nil {
		t.Error("an empty query was accepted")
	}
}

// TestKeywordFindsExactTerms is why the search is hybrid: an identifier that
// appears verbatim should surface even when the phrasing around it differs.
func TestKeywordFindsExactTerms(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	results, err := store.Search(ctx, embedder, Query{Text: "stampede"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].Keyword <= 0 {
		t.Errorf("keyword score = %v, want the exact term to contribute", results[0].Keyword)
	}
}

// TestChangingModelRebuilds checks vectors from one model are never compared
// against vectors from another.
func TestChangingModelRebuilds(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A different vector length means a different model.
	other := embed.Fake{Dimensions: 64}
	reopened, err := Open(v.Root(), other)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	count, err := reopened.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("index kept %d chunks from another model", count)
	}
}

func mustRef(t *testing.T, v *vault.Vault, s string) vault.Ref {
	t.Helper()

	ref, err := v.ParseRef(s)
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", s, err)
	}
	return ref
}

// TestReindexReportsProgress covers what a caller needs to distinguish a slow
// pass from a stuck one: which note is being worked on, how far through it is,
// and what each note cost.
func TestReindexReportsProgress(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	var events []Event
	report, err := ReindexWith(ctx, v, store, embedder, namer(v), func(ev Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("ReindexWith: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("no progress was reported")
	}
	if got := events[len(events)-1]; got.Done != got.Total {
		t.Errorf("last event is %d of %d; the count should finish where it started", got.Done, got.Total)
	}

	var embedded int
	for i, ev := range events {
		if ev.Done != i+1 {
			t.Errorf("event %d reports Done = %d; progress must not skip or repeat", i, ev.Done)
		}
		if ev.Total != len(events) {
			t.Errorf("event %d reports Total = %d, want %d", i, ev.Total, len(events))
		}
		if ev.Ref == "" || strings.HasSuffix(ev.Ref, ".md") {
			t.Errorf("event %d names %q; progress should carry a ref", i, ev.Ref)
		}
		if ev.Outcome == Embedded {
			embedded++
			if ev.Chunks == 0 {
				t.Errorf("event %d embedded a note but reported no chunks", i)
			}
		}
	}
	if embedded != report.Indexed {
		t.Errorf("%d embedded events for %d indexed notes", embedded, report.Indexed)
	}

	// A second pass reports the same notes as unchanged, which is what makes
	// the common case cheap and worth distinguishing in the output.
	events = nil
	if _, err := ReindexWith(ctx, v, store, embedder, namer(v), func(ev Event) {
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("second ReindexWith: %v", err)
	}
	for _, ev := range events {
		if ev.Outcome == Embedded {
			t.Errorf("%s was re-embedded although it had not changed", ev.Ref)
		}
	}
}

// TestReindexStopsWhenCancelled keeps a cancelled pass from running the whole
// vault. Work already committed stays valid, so the next pass resumes.
func TestReindexStopsWhenCancelled(t *testing.T) {
	v, store, embedder := fixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err == nil {
		t.Error("a cancelled reindex reported success")
	}
}

// TestReindexIsIncrementalWithRepeatedChunks covers a note that produces the
// same chunk more than once.
//
// Comparing stored hashes as a set against the chunk slice made such a note
// look changed on every pass, so it was re-embedded forever: never wrong, just
// paying for a first index over and over. Six notes in a real 431-note vault
// hit this, which is how it was found.
func TestReindexIsIncrementalWithRepeatedChunks(t *testing.T) {
	ctx := context.Background()
	v, store, embedder := fixture(t)

	// Two sections with identical bodies chunk to identical text, and so to
	// identical hashes.
	repeated := "# Repeated\n\n## One\n\nThe very same words in both sections.\n\n" +
		"## Two\n\nThe very same words in both sections.\n"
	note := &vault.Note{
		Path: "Standards/repeated.md",
		FM: schema.Frontmatter{
			Type:    "standard",
			Created: schema.Today(),
			Updated: schema.Today(),
			Tags:    []string{"standards"},
			Author:  "agent",
		},
		Body: repeated,
	}
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	// Nothing has changed, so a second pass must embed nothing at all.
	report, err := Reindex(ctx, v, store, embedder, namer(v))
	if err != nil {
		t.Fatalf("second Reindex: %v", err)
	}
	if report.Indexed != 0 {
		t.Errorf("re-indexed %d unchanged note(s); a repeated chunk should not look like a change", report.Indexed)
	}

	// A third pass, to be sure the first re-index did not merely converge.
	if report, err = Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("third Reindex: %v", err)
	}
	if report.Indexed != 0 {
		t.Errorf("still re-indexing %d note(s) on the third pass", report.Indexed)
	}

	// Changing the note must still be noticed.
	note.Body = repeated + "\n## Three\n\nSomething new.\n"
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if report, err = Reindex(ctx, v, store, embedder, namer(v)); err != nil {
		t.Fatalf("fourth Reindex: %v", err)
	}
	if report.Indexed != 1 {
		t.Errorf("indexed = %d, want 1; an edit was missed", report.Indexed)
	}
}
