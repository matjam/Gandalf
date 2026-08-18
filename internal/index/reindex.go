package index

import (
	"context"
	"fmt"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/vault"
)

// Report says what a reindex did.
type Report struct {
	// Indexed is the number of notes whose chunks were rewritten.
	Indexed int

	// Unchanged is the number of notes already up to date.
	Unchanged int

	// Removed is the number of notes dropped because they no longer exist.
	Removed int

	// Chunks is the total number of chunks in the index afterwards.
	Chunks int
}

// Namer turns a note path into the ref that addresses it. The index reports
// refs rather than paths, and only the caller knows how a vault addresses
// itself.
type Namer func(path string) string

// Progress reports how far a reindex has got. It is called after each note is
// considered, whether or not that note needed embedding, so a caller can show
// movement through a vault where most notes are already current.
type Progress func(done, total int)

// Reindex brings the index into line with the vault.
//
// Work is skipped per note by comparing chunk fingerprints, because embedding
// is the expensive part and most notes are unchanged between runs. A note whose
// text is identical costs one read.
func Reindex(ctx context.Context, v *vault.Vault, store *Store, embedder embed.Embedder, name Namer) (Report, error) {
	return ReindexWith(ctx, v, store, embedder, name, nil)
}

// ReindexWith is Reindex with progress reporting.
//
// A first index of a large vault is minutes of embedding, and a caller that
// cannot see how far along it is has no way to tell slow from stuck.
func ReindexWith(ctx context.Context, v *vault.Vault, store *Store, embedder embed.Embedder, name Namer, onProgress Progress) (Report, error) {
	paths, err := v.List()
	if err != nil {
		return Report{}, err
	}

	total := len(paths)
	done := 0
	step := func() {
		done++
		if onProgress != nil {
			onProgress(done, total)
		}
	}

	indexed, err := store.Paths()
	if err != nil {
		return Report{}, err
	}

	// Chunks are sized for the model actually in use, so switching to one with
	// a smaller window re-chunks rather than overrunning it.
	budget := embed.Budget(embedder)

	var report Report
	present := map[string]bool{}

	for _, path := range paths {
		present[path] = true

		// A cancelled reindex stops where it is rather than running to the end
		// of the vault. What it already wrote stays valid: the next run picks
		// up from there, because freshness is decided per note.
		if err := ctx.Err(); err != nil {
			return report, err
		}

		note, err := v.Read(path)
		if err != nil {
			// A note too broken to parse is lint's business; leaving it out of
			// the index is better than failing the whole run.
			step()
			continue
		}

		// A note excluded after it was indexed has to be removed, not merely
		// skipped, or the flag would only apply to notes written after it.
		var chunks []Chunk
		if note.Indexed() {
			chunks = Chunks(name(path), note.Title(), note, budget)
		}

		if len(chunks) == 0 {
			if indexed[path] {
				if err := store.Forget(path); err != nil {
					return Report{}, err
				}
			}
			step()
			continue
		}

		fresh, err := unchanged(store, path, chunks)
		if err != nil {
			return Report{}, err
		}
		if fresh {
			report.Unchanged++
			step()
			continue
		}

		texts := make([]string, 0, len(chunks))
		for _, c := range chunks {
			texts = append(texts, embedded(c))
		}

		vectors, err := embedder.Embed(ctx, texts)
		if err != nil {
			return Report{}, fmt.Errorf("embed %q: %w", path, err)
		}
		if len(vectors) != len(chunks) {
			return Report{}, fmt.Errorf("embedder returned %d vectors for %d chunks", len(vectors), len(chunks))
		}

		if err := store.Replace(ctx, path, chunks, vectors); err != nil {
			return Report{}, err
		}
		report.Indexed++
		step()
	}

	for path := range indexed {
		if present[path] {
			continue
		}
		if err := store.Forget(path); err != nil {
			return Report{}, err
		}
		report.Removed++
	}

	if report.Chunks, err = store.Count(); err != nil {
		return Report{}, err
	}

	return report, nil
}

// embedded returns the text handed to the model: the note's title and the
// section heading are prepended so a chunk carries the context a reader would
// have had, which a bare paragraph does not.
func embedded(c Chunk) string {
	prefix := c.Title
	if c.Heading != "" && c.Heading != c.Title {
		prefix += " — " + c.Heading
	}
	if prefix == "" {
		return c.Text
	}
	return prefix + "\n\n" + c.Text
}

// unchanged reports whether the stored chunks for a note match the ones just
// computed.
func unchanged(store *Store, path string, chunks []Chunk) (bool, error) {
	stored, err := store.Hashes(path)
	if err != nil {
		return false, err
	}
	if len(stored) != len(chunks) {
		return false, nil
	}

	for _, c := range chunks {
		if !stored[c.Hash] {
			return false, nil
		}
	}
	return true, nil
}
