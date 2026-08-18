package server

import (
	"context"
	"strings"
	"testing"

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
