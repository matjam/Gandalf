package index

import (
	"context"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/vault"
)

// emptyStore returns an index over a vault with no notes.
func emptyStore(t *testing.T) (*vault.Vault, *Store) {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open vault: %v", err)
	}

	store, err := Open(v.Root(), embed.Fake{})
	if err != nil {
		t.Fatalf("Open index: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	return v, store
}

func TestReplaceAndForget(t *testing.T) {
	ctx := context.Background()
	_, store := emptyStore(t)

	chunks := []Chunk{
		{Ref: "standard:x", Path: "Standards/x.md", Heading: "One", Title: "X", Text: "first", Hash: "h1"},
		{Ref: "standard:x", Path: "Standards/x.md", Heading: "Two", Title: "X", Text: "second", Hash: "h2"},
	}
	vectors := []embed.Vector{{1, 0}, {0, 1}}

	if err := store.Replace(ctx, "Standards/x.md", chunks, vectors); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	count, err := store.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	hashes, err := store.Hashes("Standards/x.md")
	if err != nil {
		t.Fatalf("Hashes: %v", err)
	}
	if hashes["h1"] != 1 || hashes["h2"] != 1 || len(hashes) != 2 {
		t.Errorf("hashes = %v", hashes)
	}

	// Replacing swaps rather than accumulates.
	if err := store.Replace(ctx, "Standards/x.md", chunks[:1], vectors[:1]); err != nil {
		t.Fatalf("second Replace: %v", err)
	}
	if count, _ = store.Count(); count != 1 {
		t.Errorf("count after replace = %d, want 1", count)
	}

	paths, err := store.Paths()
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if !paths["Standards/x.md"] || len(paths) != 1 {
		t.Errorf("paths = %v", paths)
	}

	if err := store.Forget("Standards/x.md"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if count, _ = store.Count(); count != 0 {
		t.Errorf("count after forget = %d, want 0", count)
	}
}

func TestReplaceRejectsMismatchedVectors(t *testing.T) {
	_, store := emptyStore(t)

	err := store.Replace(context.Background(), "Standards/x.md",
		[]Chunk{{Hash: "h1"}, {Hash: "h2"}},
		[]embed.Vector{{1, 0}})
	if err == nil {
		t.Fatal("Replace accepted a vector for every other chunk")
	}
	if !strings.Contains(err.Error(), "2 chunks and 1 vectors") {
		t.Errorf("error = %q", err)
	}
}

// TestReopenKeepsTheIndex checks the index survives a restart, which is the
// point of storing it rather than rebuilding per session.
func TestReopenKeepsTheIndex(t *testing.T) {
	ctx := context.Background()
	v, store := emptyStore(t)

	if err := store.Replace(ctx, "Standards/x.md",
		[]Chunk{{Ref: "standard:x", Path: "Standards/x.md", Title: "X", Text: "first", Hash: "h1"}},
		[]embed.Vector{{1, 0}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(v.Root(), embed.Fake{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	count, err := reopened.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want the stored chunk", count)
	}
}

func TestChunks(t *testing.T) {
	note, err := vault.ParseNote("Standards/x.md", []byte(
		"---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [standards]\nauthor: agent\n---\n\n"+
			"# Title\n\nPreamble text.\n\n## First\n\nFirst body.\n\n## Second\n\nSecond body.\n\n"+
			"## Backlinks\n\n- [[Sessions/2026/08/a]]\n"))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	chunks := Chunks("standard:x", "Title", note, embed.Budget(embed.Fake{}))
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3: %+v", len(chunks), chunks)
	}

	headings := make([]string, 0, len(chunks))
	for _, c := range chunks {
		headings = append(headings, c.Heading)
		if c.Ref != "standard:x" || c.Title != "Title" {
			t.Errorf("chunk = %+v", c)
		}
		if c.Hash == "" {
			t.Error("chunk has no fingerprint")
		}
		// The maintained block lists other notes' names; indexing it would
		// return this note for searches about them.
		if strings.Contains(c.Text, "Sessions/2026/08/a") {
			t.Errorf("the backlinks block was indexed: %+v", c)
		}
	}
	if strings.Join(headings, ",") != "Title,First,Second" {
		t.Errorf("headings = %v", headings)
	}
}

// TestChunksFitTheModelsWindow is why the window is part of the embedder
// interface: a small model handed an oversized chunk truncates it silently,
// embedding the start of a section as though it were the whole thing.
func TestChunksFitTheModelsWindow(t *testing.T) {
	var body strings.Builder
	body.WriteString("---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [standards]\nauthor: agent\n---\n\n# Long\n\n")
	for range 40 {
		body.WriteString(strings.Repeat("word ", 40))
		body.WriteString("\n\n")
	}

	note, err := vault.ParseNote("Standards/long.md", []byte(body.String()))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	// A small sentence encoder against a larger model: the same note has to
	// chunk differently for each.
	small := embed.Budget(embed.Fake{Tokens: 256})
	large := embed.Budget(embed.Fake{Tokens: 8192})

	smallChunks := Chunks("standard:long", "Long", note, small)
	largeChunks := Chunks("standard:long", "Long", note, large)

	if len(smallChunks) <= len(largeChunks) {
		t.Errorf("small window produced %d chunks and large produced %d; want more for the smaller model",
			len(smallChunks), len(largeChunks))
	}

	for _, c := range smallChunks {
		if n := len([]rune(c.Text)); n > small {
			t.Errorf("chunk of %d runes exceeds the %d-rune budget", n, small)
		}
	}
}

// TestChunksSplitOversizedParagraphs covers text with no paragraph break to
// split on, which would otherwise be handed over whole and truncated.
func TestChunksSplitOversizedParagraphs(t *testing.T) {
	body := "---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [standards]\nauthor: agent\n---\n\n# Wall\n\n" +
		strings.Repeat("unbroken ", 2000)

	note, err := vault.ParseNote("Standards/wall.md", []byte(body))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	budget := embed.Budget(embed.Fake{Tokens: 256})
	chunks := Chunks("standard:wall", "Wall", note, budget)

	if len(chunks) < 2 {
		t.Fatalf("a single long paragraph produced %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if n := len([]rune(c.Text)); n > budget {
			t.Errorf("chunk of %d runes exceeds the %d-rune budget", n, budget)
		}
	}
}

func TestChunksIgnoresHeadingsInCode(t *testing.T) {
	note, err := vault.ParseNote("Standards/x.md", []byte(
		"---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [standards]\nauthor: agent\n---\n\n"+
			"# Title\n\n```\n# not a heading\n```\n\nBody.\n"))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	chunks := Chunks("standard:x", "Title", note, embed.Budget(embed.Fake{}))
	if len(chunks) != 1 {
		t.Errorf("got %d chunks, want 1: %+v", len(chunks), chunks)
	}
}
