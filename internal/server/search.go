package server

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/index"
)

// SearchInput describes what to look for.
type SearchInput struct {
	Query string `json:"query" jsonschema:"what you are looking for, in your own words"`

	Kinds []string `json:"kinds,omitempty" jsonschema:"restrict to these categories, by either singular or plural name, such as session or standards"`

	Limit int `json:"limit,omitempty" jsonschema:"maximum results; defaults to 10"`

	Vault string `json:"vault,omitempty" jsonschema:"search only one vault: a team slug, or personal for your own; omit to search every vault you can reach"`
}

// SearchHit is one match.
type SearchHit struct {
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Heading string `json:"heading,omitempty"`
	Snippet string `json:"snippet"`

	Score    float64 `json:"score"`
	Semantic float64 `json:"semantic"`
	Keyword  float64 `json:"keyword"`
}

// SearchOutput is the result of a search.
type SearchOutput struct {
	Hits []SearchHit `json:"hits"`

	// Indexed reports notes brought up to date before searching, so a caller
	// can see when results reflect edits made moments ago.
	Indexed int `json:"notes_indexed,omitempty"`

	// Index says whether the results are complete. A search against a vault
	// still being indexed is answered from what has landed so far, and saying
	// so is the difference between "no such note" and "not yet indexed". When
	// several vaults were searched it is the combined state: ready only when
	// every one of them is.
	Index IndexStatus `json:"index"`

	// Vaults breaks the state down by vault, and is set only when more than one
	// was searched. The personal vault is named "personal".
	Vaults []VaultIndex `json:"vaults,omitempty"`
}

// VaultIndex is the state of one vault's search index.
type VaultIndex struct {
	Vault string      `json:"vault"`
	Index IndexStatus `json:"index"`
}

// searcher holds the index and the embedder behind it, built on first use.
//
// Search is the one capability that needs a model, so it is the one capability
// allowed to be unavailable: a vault with no embedder still boots, lists,
// writes, and lints. Failing only the search tool is better than refusing to
// start over a feature the session may never use.
type searcher struct {
	once  sync.Once
	store *index.Store
	err   error
}

// personalName is what the personal vault is called where a vault has to be
// named, such as the breakdown of a search across several.
const personalName = "personal"

// name is how this vault is called in results that list several.
func (c *scoped) name() string {
	if c.slug == "" {
		return personalName
	}
	return c.slug
}

// search finds notes by meaning, reindexing first so results include edits
// made during this session.
//
// By default it covers every vault the session can reach, because the point of
// searching is to find what has already been written, and a team standard is
// no less prior work than a personal note. Each vault has its own index; their
// hits are merged by score, which is comparable across them because every
// index is built with the same embedder.
func (s *Server) search(ctx context.Context, _ *sdk.CallToolRequest, in SearchInput) (*sdk.CallToolResult, SearchOutput, error) {
	targets, err := s.vaultsFor(ctx, in.Vault)
	if err != nil {
		return nil, SearchOutput{}, err
	}

	// A vault with no embedder cannot be searched, and is left out of a
	// search across several rather than failing it. If that leaves nothing,
	// search is not configured at all.
	var searchable []*scoped
	for _, sc := range targets {
		if sc.embedder != nil {
			searchable = append(searchable, sc)
		}
	}
	if len(searchable) == 0 {
		return nil, SearchOutput{}, fmt.Errorf(
			"search is not configured: start the server with an embedding model, " +
				"or use list and lint to find notes by name")
	}

	var (
		out      = SearchOutput{Hits: []SearchHit{}}
		statuses []VaultIndex
	)
	for _, sc := range searchable {
		hits, status, indexed, err := sc.searchVault(ctx, in)
		if err != nil {
			// One vault failing must not take the others' results with it, but
			// a search over a single vault has nothing else to return.
			if len(searchable) == 1 {
				return nil, SearchOutput{}, err
			}
			slog.Warn("gandalf: search vault", "vault", sc.name(), "error", err)
			status = IndexStatus{State: IndexFailed, Error: err.Error(),
				Note: "this vault could not be searched, so its notes are missing from the results"}
		}

		out.Hits = append(out.Hits, hits...)
		out.Indexed += indexed
		statuses = append(statuses, VaultIndex{Vault: sc.name(), Index: status})
	}

	// Merged by score, best first, then cut to the limit asked for. The sort is
	// stable so that equal scores keep the order the vaults were searched in,
	// which puts the caller's own notes first.
	sort.SliceStable(out.Hits, func(i, j int) bool { return out.Hits[i].Score > out.Hits[j].Score })
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if len(out.Hits) > limit {
		out.Hits = out.Hits[:limit]
	}

	if len(statuses) == 1 {
		out.Index = statuses[0].Index
		return nil, out, nil
	}
	out.Index = combineStatus(statuses)
	out.Vaults = statuses
	return nil, out, nil
}

// defaultSearchLimit is how many hits a search returns when it is not told.
// It matches the index's own default, so a search over several vaults returns
// as many as one over a single vault would.
const defaultSearchLimit = 10

// combineStatus folds the states of several indexes into one a caller can act
// on: ready only if every one is, building if any is still working, failed
// only when something failed and nothing is still on its way.
func combineStatus(vaults []VaultIndex) IndexStatus {
	out := IndexStatus{State: IndexReady}

	rank := map[IndexState]int{IndexReady: 0, IndexUnavailable: 1, IndexFailed: 2, IndexBuilding: 3}
	for _, v := range vaults {
		out.Done += v.Index.Done
		out.Total += v.Index.Total
		if rank[v.Index.State] > rank[out.State] {
			out.State = v.Index.State
		}
		if out.Backend == "" {
			out.Backend = v.Index.Backend
		}
	}

	switch out.State {
	case IndexBuilding:
		out.Note = fmt.Sprintf(
			"The search index is still being built for some vaults (%d of %d notes). search works "+
				"now but only sees what has been indexed so far; a note it misses may exist. "+
				"See vaults for which. Use list when you need certainty before it finishes.",
			out.Done, out.Total)
	case IndexFailed:
		out.Note = "The search index failed to build for some vaults, so results are incomplete. " +
			"See vaults for which. Find notes with list instead."
	}
	return out
}

// searchVault searches one vault and returns its hits with their refs
// qualified for the caller.
func (c *scoped) searchVault(ctx context.Context, in SearchInput) ([]SearchHit, IndexStatus, int, error) {
	// Wait for the background pass, but only so long. A cold vault takes
	// minutes to embed, and a search that never returns is worse than one that
	// returns what exists so far and says the index is still building.
	c.awaitIndex(ctx)

	store, err := c.index()
	if err != nil {
		return nil, IndexStatus{}, 0, err
	}

	status := c.indexStatus()

	// Once the first pass is done, a freshness pass before searching is cheap:
	// unchanged notes are skipped by fingerprint, so a note written a moment
	// ago is findable without re-embedding the vault. While the first pass is
	// still running, the background goroutine already owns that work.
	var report index.Report
	if status.State == IndexReady {
		report, err = index.Reindex(ctx, c.vault, store, c.embedder, func(path string) string {
			return c.canonical(path).String()
		})
		if err != nil {
			return nil, IndexStatus{}, 0, err
		}
	}

	results, err := store.Search(ctx, c.embedder, index.Query{
		Text:  in.Query,
		Kinds: c.canonicalKinds(in.Kinds),
		Limit: in.Limit,
	})
	if err != nil {
		return nil, IndexStatus{}, 0, err
	}

	hits := make([]SearchHit, 0, len(results))
	for _, r := range results {
		hits = append(hits, SearchHit{
			Ref:      c.qualifyString(r.Ref),
			Title:    r.Title,
			Heading:  r.Heading,
			Snippet:  snippet(r.Snippet),
			Score:    round(r.Score),
			Semantic: round(r.Semantic),
			Keyword:  round(r.Keyword),
		})
	}

	return hits, status, report.Indexed, nil
}

// index opens the store once, reusing it for the session.
func (c *Core) index() (*index.Store, error) {
	c.searcher.once.Do(func() {
		c.searcher.store, c.searcher.err = index.Open(c.vault.Root(), c.embedder)
	})
	return c.searcher.store, c.searcher.err
}

// closeIndex releases the index, if one was opened.
func (c *Core) closeIndex() error {
	if c.searcher.store == nil {
		return nil
	}
	return c.searcher.store.Close()
}

// maxSnippetRunes bounds what a result returns. A search result is for
// deciding what to read, not for reading.
const maxSnippetRunes = 400

// snippet trims a chunk to something worth putting in a result.
func snippet(text string) string {
	text = strings.TrimSpace(text)

	runes := []rune(text)
	if len(runes) <= maxSnippetRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxSnippetRunes])) + "…"
}

// round trims a score to three decimals, which is as much precision as a
// ranking decision ever needs.
func round(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
