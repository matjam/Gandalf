package index

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/matjam/gandalf/internal/embed"
)

// Result is one match.
type Result struct {
	Ref     string
	Path    string
	Title   string
	Heading string
	Snippet string

	// Score combines semantic and keyword agreement; higher is better.
	Score float64

	// Semantic and Keyword are the contributing scores, reported so a
	// surprising ranking can be understood rather than guessed at.
	Semantic float64
	Keyword  float64
}

// Query describes a search.
type Query struct {
	Text string

	// Kinds restricts results to these categories, by ref prefix.
	Kinds []string

	// Limit caps the results returned.
	Limit int
}

// Search finds the chunks most like the query.
//
// Semantic and keyword scores are combined rather than chosen between, because
// they fail differently: embeddings miss exact identifiers — an error string, a
// function name — while keywords miss anything phrased differently from the
// note. Running both and fusing costs one extra query over a table small enough
// not to notice.
func (s *Store) Search(ctx context.Context, embedder embed.Embedder, q Query) ([]Result, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("a search needs something to look for")
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}

	vectors, err := embedder.Embed(ctx, []string{q.Text})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for one query", len(vectors))
	}

	semantic, err := s.semantic(vectors[0])
	if err != nil {
		return nil, err
	}

	keyword, err := s.keyword(ctx, q.Text)
	if err != nil {
		return nil, err
	}

	return rank(semantic, keyword, q), nil
}

// scored is an intermediate result keyed by chunk.
type scored struct {
	Result
	id int64
}

// semantic scores every chunk against the query vector.
//
// Every vector is read and compared. At a vault's scale — thousands of chunks
// at most — this is milliseconds, and it avoids an approximate index that
// would need its own tuning, its own failure modes, and a C extension.
func (s *Store) semantic(query embed.Vector) (map[int64]scored, error) {
	rows, err := s.db.Query(`SELECT id, ref, path, heading, title, text, embedding FROM chunks`)
	if err != nil {
		return nil, fmt.Errorf("read chunks: %w", err)
	}
	defer rows.Close()

	out := map[int64]scored{}
	for rows.Next() {
		var (
			id   int64
			r    Result
			blob []byte
		)
		if err := rows.Scan(&id, &r.Ref, &r.Path, &r.Heading, &r.Title, &r.Snippet, &blob); err != nil {
			return nil, err
		}

		vector, err := embed.Decode(blob)
		if err != nil {
			return nil, err
		}

		r.Semantic = embed.Cosine(query, vector)
		out[id] = scored{Result: r, id: id}
	}

	return out, rows.Err()
}

// keyword scores chunks by full-text match, returning a value from 0 to 1.
func (s *Store) keyword(ctx context.Context, text string) (map[int64]float64, error) {
	query := ftsQuery(text)
	if query == "" {
		return map[int64]float64{}, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT rowid, bm25(chunks_fts) FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts) LIMIT 200`,
		query)
	if err != nil {
		// A malformed query is the user's words, not a fault: fall back to
		// semantic results rather than failing the search.
		return map[int64]float64{}, nil
	}
	defer rows.Close()

	raw := map[int64]float64{}
	best := 0.0
	for rows.Next() {
		var (
			id    int64
			score float64
		)
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}

		// bm25 returns lower-is-better, and negative; flip it.
		value := -score
		if value > best {
			best = value
		}
		raw[id] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if best <= 0 {
		return map[int64]float64{}, nil
	}
	for id, value := range raw {
		raw[id] = value / best
	}

	return raw, nil
}

// ftsQuery turns a natural-language query into an FTS5 OR query, quoting each
// term so punctuation cannot be read as operator syntax.
func ftsQuery(text string) string {
	var terms []string
	for _, word := range strings.Fields(text) {
		word = strings.Trim(word, `"'.,;:!?()[]{}`)
		if len(word) < 2 {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(word, `"`, "")+`"`)
	}
	return strings.Join(terms, " OR ")
}

// semanticWeight is how much of the combined score comes from embeddings.
//
// Weighted towards semantics because that is the capability being added —
// keyword search was always possible with grep — while keeping enough keyword
// influence to pull up exact identifiers.
const semanticWeight = 0.7

// rank fuses the two scores and returns the best matches.
func rank(semantic map[int64]scored, keyword map[int64]float64, q Query) []Result {
	out := make([]Result, 0, len(semantic))

	for id, s := range semantic {
		r := s.Result
		r.Keyword = keyword[id]
		r.Score = semanticWeight*r.Semantic + (1-semanticWeight)*r.Keyword

		if !matchesKind(r.Ref, q.Kinds) || r.Score <= 0 {
			continue
		}
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Ref < out[j].Ref
	})

	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

// matchesKind reports whether a ref belongs to one of the requested
// categories.
func matchesKind(ref string, kinds []string) bool {
	if len(kinds) == 0 {
		return true
	}

	kind, _, _ := strings.Cut(ref, ":")
	for _, want := range kinds {
		if strings.EqualFold(kind, want) {
			return true
		}
	}
	return false
}
