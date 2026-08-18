package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/index"
)

// SearchInput describes what to look for.
type SearchInput struct {
	Query string `json:"query" jsonschema:"what you are looking for, in your own words"`

	Kinds []string `json:"kinds,omitempty" jsonschema:"restrict to these categories, such as session or standard"`

	Limit int `json:"limit,omitempty" jsonschema:"maximum results; defaults to 10"`
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
	// so is the difference between "no such note" and "not yet indexed".
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

// search finds notes by meaning, reindexing first so results include edits
// made during this session.
func (s *Server) search(ctx context.Context, _ *sdk.CallToolRequest, in SearchInput) (*sdk.CallToolResult, SearchOutput, error) {
	if s.embedder == nil {
		return nil, SearchOutput{}, fmt.Errorf(
			"search is not configured: start the server with an embedding model, " +
				"or use gandalf_list and gandalf_lint to find notes by name")
	}

	// Wait for the background pass, but only so long. A cold vault takes
	// minutes to embed, and a search that never returns is worse than one that
	// returns what exists so far and says the index is still building.
	s.awaitIndex(ctx)

	store, err := s.index()
	if err != nil {
		return nil, SearchOutput{}, err
	}

	status := s.indexStatus()

	// Once the first pass is done, a freshness pass before searching is cheap:
	// unchanged notes are skipped by fingerprint, so a note written a moment
	// ago is findable without re-embedding the vault. While the first pass is
	// still running, the background goroutine already owns that work.
	var report index.Report
	if status.State == IndexReady {
		report, err = index.Reindex(ctx, s.vault, store, s.embedder, func(path string) string {
			return s.canonical(path).String()
		})
		if err != nil {
			return nil, SearchOutput{}, err
		}
	}

	results, err := store.Search(ctx, s.embedder, index.Query{
		Text:  in.Query,
		Kinds: in.Kinds,
		Limit: in.Limit,
	})
	if err != nil {
		return nil, SearchOutput{}, err
	}

	out := SearchOutput{
		Hits:    make([]SearchHit, 0, len(results)),
		Indexed: report.Indexed,
		Index:   status,
	}
	for _, r := range results {
		out.Hits = append(out.Hits, SearchHit{
			Ref:      r.Ref,
			Title:    r.Title,
			Heading:  r.Heading,
			Snippet:  snippet(r.Snippet),
			Score:    round(r.Score),
			Semantic: round(r.Semantic),
			Keyword:  round(r.Keyword),
		})
	}

	return nil, out, nil
}

// index opens the store once, reusing it for the session.
func (s *Server) index() (*index.Store, error) {
	s.searcher.once.Do(func() {
		s.searcher.store, s.searcher.err = index.Open(s.vault.Root(), s.embedder)
	})
	return s.searcher.store, s.searcher.err
}

// closeIndex releases the index, if one was opened.
func (s *Server) closeIndex() error {
	if s.searcher.store == nil {
		return nil
	}
	return s.searcher.store.Close()
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
