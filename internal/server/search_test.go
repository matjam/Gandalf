package server

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// searchHarness is a harness whose server can search, using the deterministic
// embedder so tests never touch a network.
func searchHarness(t *testing.T) *harness {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := instructions.Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	s := WithSearch(v, "test", embed.Fake{})
	t.Cleanup(func() { s.Close() })

	go func() { _ = s.MCP().Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return &harness{t: t, client: session, vault: v, context: ctx}
}

func TestSearch(t *testing.T) {
	h := searchHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{
		Title: "Cache Invalidation", Tags: []string{"caching"},
	}, &session)
	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     session.Ref,
		Heading: "Stampede",
		Content: "Many readers missing at once overwhelm the origin. Fixed with a single-flight lock.",
	}, nil)

	var out SearchOutput
	h.call("gandalf_search", SearchInput{Query: "readers overwhelming the origin"}, &out)

	if len(out.Hits) == 0 {
		t.Fatal("no hits")
	}

	var found bool
	for _, hit := range out.Hits {
		if hit.Ref == session.Ref {
			found = true
			if hit.Snippet == "" {
				t.Error("hit has no snippet")
			}
			if hit.Score <= 0 {
				t.Errorf("score = %v", hit.Score)
			}
		}
		if strings.Contains(hit.Ref, "/") {
			t.Errorf("a path leaked into a hit: %q", hit.Ref)
		}
	}
	if !found {
		t.Errorf("the session note is missing from %+v", out.Hits)
	}
}

// TestSearchSeesRecentWrites is why search reindexes before running: a note
// written moments ago is the one most likely to be searched for.
func TestSearchSeesRecentWrites(t *testing.T) {
	h := searchHarness(t)

	var out SearchOutput
	h.call("gandalf_search", SearchInput{Query: "quorum reconciliation"}, &out)
	for _, hit := range out.Hits {
		if strings.Contains(hit.Snippet, "quorum reconciliation") {
			t.Fatal("found the note before it was written")
		}
	}

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Quorum Work", Tags: []string{"work"}}, &session)
	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Settled on quorum reconciliation across the replicas.",
	}, nil)

	h.call("gandalf_search", SearchInput{Query: "quorum reconciliation"}, &out)

	for _, hit := range out.Hits {
		if hit.Ref == session.Ref {
			return
		}
	}
	t.Errorf("the note written this session is not searchable: %+v", out.Hits)
}

func TestSearchFiltersByCategory(t *testing.T) {
	h := searchHarness(t)

	h.call("gandalf_session_start", SessionStartInput{
		Title: "Migration Work", Tags: []string{"work"},
	}, nil)

	var out SearchOutput
	h.call("gandalf_search", SearchInput{Query: "migrations run before traffic", Kinds: []string{"standard"}}, &out)

	for _, hit := range out.Hits {
		if !strings.HasPrefix(hit.Ref, "standard:") {
			t.Errorf("filtered search returned %q", hit.Ref)
		}
	}
}

func TestSearchFindsSeededStandards(t *testing.T) {
	h := searchHarness(t)

	var out SearchOutput
	h.call("gandalf_search", SearchInput{Query: "migrations run before the service accepts traffic"}, &out)

	if len(out.Hits) == 0 {
		t.Fatal("no hits over the seeded vault")
	}
	if out.Hits[0].Ref != "standard:database" {
		t.Errorf("top hit = %q, want standard:database; hits: %+v", out.Hits[0].Ref, out.Hits)
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	h := searchHarness(t)

	if msg := h.callErr("gandalf_search", SearchInput{Query: "   "}); !strings.Contains(msg, "look for") {
		t.Errorf("error = %q", msg)
	}
}

func TestSearchLimit(t *testing.T) {
	h := searchHarness(t)

	var out SearchOutput
	h.call("gandalf_search", SearchInput{Query: "the", Limit: 3}, &out)
	if len(out.Hits) > 3 {
		t.Errorf("got %d hits, want at most 3", len(out.Hits))
	}
}

// TestSearchUnavailableDegradesOnlySearch checks a vault with no embedder is
// still fully usable for everything else. Refusing to start over a capability
// the session may never use would be the wrong trade.
func TestSearchUnavailableDegradesOnlySearch(t *testing.T) {
	h := newHarness(t) // built with New, so no embedder

	msg := h.callErr("gandalf_search", SearchInput{Query: "anything"})
	for _, want := range []string{"not configured", "gandalf_list"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to mention %q", msg, want)
		}
	}

	// Everything else works.
	var boot BootOutput
	h.call("gandalf_boot", BootInput{}, &boot)
	if len(boot.Contract) == 0 {
		t.Error("boot broke without an embedder")
	}

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)
	h.call("gandalf_note_append", NoteAppendInput{Ref: session.Ref, Content: "Still writing."}, nil)

	var lint LintOutput
	h.call("gandalf_lint", LintInput{}, &lint)
	if !lint.Clean {
		t.Errorf("lint broke without an embedder: %+v", lint.Findings)
	}
}

// TestIndexIsNotAVaultNote checks the index does not appear as a note, get
// linted, or turn up in listings.
func TestIndexIsNotAVaultNote(t *testing.T) {
	h := searchHarness(t)

	h.call("gandalf_search", SearchInput{Query: "anything at all"}, nil)

	paths, err := h.vault.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range paths {
		if strings.Contains(p, "index.db") || strings.Contains(p, ".gandalf") {
			t.Errorf("machinery is listed as a note: %q", p)
		}
	}

	var lint LintOutput
	h.call("gandalf_lint", LintInput{}, &lint)
	if !lint.Clean {
		t.Errorf("the index produced lint findings: %+v", lint.Findings)
	}
}

// slowEmbedder wraps the deterministic embedder with a delay, so a test can
// observe the window where the index is still being built.
type slowEmbedder struct {
	embed.Fake
	delay time.Duration
}

func (s slowEmbedder) Embed(ctx context.Context, texts []string) ([]embed.Vector, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Fake.Embed(ctx, texts)
}

// TestBootReportsSearchStatus is the signal that was missing: a session had no
// way to know whether search was usable before calling it and waiting.
func TestBootReportsSearchStatus(t *testing.T) {
	t.Run("without an embedder", func(t *testing.T) {
		h := newHarness(t)

		var out BootOutput
		h.call("gandalf_boot", BootInput{}, &out)

		if out.Search.State != IndexUnavailable {
			t.Errorf("state = %q, want %q", out.Search.State, IndexUnavailable)
		}
		if !strings.Contains(out.Search.Note, "gandalf_list") {
			t.Error("an unavailable index should point at the tool that still works")
		}
	})

	t.Run("with an embedder", func(t *testing.T) {
		h := searchHarness(t)

		var out BootOutput
		h.call("gandalf_boot", BootInput{}, &out)

		if out.Search.State == IndexUnavailable {
			t.Error("search is configured but was reported unavailable")
		}
	})
}

// TestSearchAnswersWhileTheIndexIsStillBuilding is the behaviour the whole
// change exists for. A first index of a large vault is minutes of embedding;
// blocking on it turns the tool the protocol says to call first into a stall.
func TestSearchAnswersWhileTheIndexIsStillBuilding(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := instructions.Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	// Slow enough that the seeded vault cannot finish inside the wait, while
	// keeping the single embed of the query itself cheap: that one is on the
	// critical path of every search and is not what this test is about.
	defer swapIndexWait(200 * time.Millisecond)()
	s := WithSearch(v, "test", slowEmbedder{delay: 100 * time.Millisecond})
	t.Cleanup(func() { s.Close() })

	go func() { _ = s.MCP().Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	h := &harness{t: t, client: session, vault: v, context: ctx}

	done := make(chan SearchOutput, 1)
	go func() {
		var out SearchOutput
		h.call("gandalf_search", SearchInput{Query: "shipping"}, &out)
		done <- out
	}()

	select {
	case out := <-done:
		if out.Index.State != IndexBuilding {
			t.Errorf("state = %q, want %q", out.Index.State, IndexBuilding)
		}
		if out.Index.Note == "" {
			t.Error("a partial result must say that it is partial")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("search did not return while the index was still building")
	}
}

// swapIndexWait shortens the partial-result deadline for a test, returning the
// function that puts it back.
func swapIndexWait(d time.Duration) func() {
	previous := indexWait
	indexWait = d
	return func() { indexWait = previous }
}
