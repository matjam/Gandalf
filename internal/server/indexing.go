package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/index"
)

// IndexState is how far the search index has got.
type IndexState string

const (
	// IndexUnavailable means no embedding model is configured, so search
	// cannot work at all. Every other tool is unaffected.
	IndexUnavailable IndexState = "unavailable"

	// IndexBuilding means the first pass over the vault is still running.
	// Search works against what has landed so far.
	IndexBuilding IndexState = "building"

	// IndexReady means a full pass has completed.
	IndexReady IndexState = "ready"

	// IndexFailed means the pass stopped on an error. The error is reported
	// alongside, because a search that silently returns nothing looks like a
	// vault with nothing in it.
	IndexFailed IndexState = "failed"
)

// IndexStatus is what the index is doing, in a shape a model can act on.
type IndexStatus struct {
	State IndexState `json:"state"`

	// Done and Total track the first pass. They are note counts rather than
	// chunk counts because a note is the unit a caller recognises.
	Done  int `json:"notes_done,omitempty"`
	Total int `json:"notes_total,omitempty"`

	// Backend names the compute path search is running on, once the model has
	// loaded. The difference between the native runtime and the pure-Go
	// fallback is more than an order of magnitude, so it belongs where someone
	// wondering why indexing is slow will see it.
	Backend string `json:"backend,omitempty"`

	Error string `json:"error,omitempty"`

	// Note explains what the state means for the caller's next move, so a
	// model does not have to infer whether to wait, retry, or use another tool.
	Note string `json:"note,omitempty"`
}

// indexer runs the reindex pass in the background and reports where it is.
//
// A first index of a large vault is minutes of embedding. Doing that inside
// the first search means the tool the memory protocol tells a model to call
// before starting work is the one that appears to hang: no output, no
// progress, and no way to tell slow from broken. Running it in the background
// makes the cost visible and lets an early search return what is ready.
type indexer struct {
	mu    sync.Mutex
	state IndexState
	done  int
	total int
	err   error

	started bool

	// ready closes when the first full pass finishes, successfully or not, so
	// a caller can wait for it with a deadline rather than polling.
	ready chan struct{}
}

func newIndexer() *indexer {
	return &indexer{ready: make(chan struct{})}
}

// start launches the first pass, once. Later calls are no-ops: subsequent
// freshness is handled by reindexing on demand inside search, which is cheap
// once the expensive first pass is done.
func (s *Server) startIndexing(ctx context.Context) {
	if s.embedder == nil {
		return
	}

	s.indexer.mu.Lock()
	if s.indexer.started {
		s.indexer.mu.Unlock()
		return
	}
	s.indexer.started = true
	s.indexer.state = IndexBuilding
	s.indexer.mu.Unlock()

	go func() {
		defer close(s.indexer.ready)

		store, err := s.index()
		if err != nil {
			s.indexer.finish(err)
			return
		}

		_, err = index.ReindexWith(ctx, s.vault, store, s.embedder,
			func(path string) string { return s.canonical(path).String() },
			func(ev index.Event) {
				s.indexer.mu.Lock()
				s.indexer.done, s.indexer.total = ev.Done, ev.Total
				s.indexer.mu.Unlock()
			})

		s.indexer.finish(err)
	}()
}

// finish records the outcome of the first pass.
func (i *indexer) finish(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.err = err
	if err != nil {
		i.state = IndexFailed
		return
	}
	i.state = IndexReady
}

// status reports what the index is doing.
func (s *Server) indexStatus() IndexStatus {
	if s.embedder == nil {
		return IndexStatus{
			State: IndexUnavailable,
			Note: "No embedding model is configured, so search will not work. " +
				"Find notes with list instead.",
		}
	}

	s.indexer.mu.Lock()
	defer s.indexer.mu.Unlock()

	out := IndexStatus{
		State:   s.indexer.state,
		Done:    s.indexer.done,
		Total:   s.indexer.total,
		Backend: s.backendName(),
	}
	if out.State == "" {
		out.State = IndexBuilding
	}
	if s.indexer.err != nil {
		out.Error = s.indexer.err.Error()
	}

	switch out.State {
	case IndexBuilding:
		out.Note = fmt.Sprintf(
			"The search index is still being built (%d of %d notes). search works "+
				"now but only sees what has been indexed so far; a note it misses may exist. "+
				"Use list when you need certainty before it finishes.",
			out.Done, out.Total)
	case IndexFailed:
		out.Note = "The search index failed to build, so search results are incomplete " +
			"or empty. Find notes with list instead."
	}

	return out
}

// indexWait bounds how long a search will wait for a cold index before
// answering from what has been built so far.
//
// Long enough that an already-warm vault finishes its freshness pass and the
// caller sees complete results; short enough that a first run against a large
// vault returns something to work with instead of appearing to hang. A
// variable so tests can exercise the partial-result path without waiting on a
// real clock.
var indexWait = 10 * time.Second

// awaitIndex waits for the first pass, giving up after the deadline so a
// search returns partial results rather than blocking a session on a cold
// index. Reporting a partial answer honestly beats a call that never returns.
func (s *Server) awaitIndex(ctx context.Context) {
	// The background pass outlives the request that triggered it: a caller
	// giving up on one search must not throw away the indexing work done so
	// far, or every search restarts the same cold build.
	s.startIndexing(context.WithoutCancel(ctx))

	timer := time.NewTimer(indexWait)
	defer timer.Stop()

	select {
	case <-s.indexer.ready:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// backendName reports the embedder's compute path when it exposes one. Not
// every embedder has the notion — a remote endpoint's engine is its own
// business — so this is asked for rather than required.
func (s *Server) backendName() string {
	type backended interface{ Backend() embed.Backend }

	if b, ok := s.embedder.(backended); ok {
		return string(b.Backend())
	}
	return ""
}
