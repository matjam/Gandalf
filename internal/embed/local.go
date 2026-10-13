package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

// LocalModel is the model the in-process embedder downloads and runs.
//
// bge-small-en-v1.5 rather than the more common all-MiniLM-L6-v2: both are
// small enough to run on a laptop CPU, but MiniLM's context window is 256
// tokens against bge-small's 512, which halves how much of a note fits in one
// chunk.
const (
	LocalModel = "BAAI/bge-small-en-v1.5"

	// localOnnx is the weights file within the model repository. It is named
	// explicitly because this repository keeps it in a subdirectory.
	localOnnx = "model.onnx"

	localDims   = 384
	localWindow = 512
)

// Local runs an embedding model in this process, with no daemon and no network
// beyond a one-off model download.
//
// It exists so that search works on a fresh install. Asking someone to stand up
// an inference server before their notes become searchable is a good way to
// have them never find out whether the searching was worth it.
type Local struct {
	// Dir is where the model is cached. Empty means the user's cache
	// directory.
	Dir string

	// run embeds a batch. It is a field so the orchestration around the model
	// — caching, validation, normalisation, error messages — can be tested
	// without downloading one.
	run func(ctx context.Context, texts []string) ([][]float32, error)

	once     sync.Once
	mu       sync.Mutex // One inference at a time bounds working memory.
	closed   bool
	err      error
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	backend  atomic.Value // Backend status remains readable during inference.
}

// NewLocal returns an in-process embedder. The model is not loaded until the
// first call, so constructing one is free and a session that never searches
// never pays for it.
func NewLocal(dir string) *Local { return &Local{Dir: dir} }

// Model identifies the embedding model.
func (l *Local) Model() string { return LocalModel }

// Backend reports which compute path the model is running on. It is empty
// until the model has been loaded, since that is when the choice is made.
func (l *Local) Backend() Backend {
	if backend := l.backend.Load(); backend != nil {
		return backend.(Backend)
	}
	return ""
}

// Dims is the vector length.
func (l *Local) Dims() int { return localDims }

// Window is the model's context in tokens.
func (l *Local) Window() int { return localWindow }

// Embed returns one vector per input.
func (l *Local) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, fmt.Errorf("local embedder is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := l.load(ctx); err != nil {
		return nil, err
	}

	// A note can contain hundreds of chunks. Passing all of them to the
	// transformer together makes its attention tensors scale with note size.
	vectors := make([]Vector, len(texts))
	for i := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		embeddings, err := l.run(ctx, texts[i:i+1])
		if err != nil {
			return nil, fmt.Errorf("embed with %s: %w", l.Model(), err)
		}
		if len(embeddings) != 1 {
			return nil, fmt.Errorf("model returned %d embeddings for 1 inputs", len(embeddings))
		}
		e := embeddings[0]
		if len(e) != localDims {
			return nil, fmt.Errorf("model returned %d dimensions, expected %d", len(e), localDims)
		}
		// Normalised on the way in, so cosine similarity is a dot product and
		// stored vectors are comparable regardless of input length.
		vectors[i] = Normalise(Vector(e))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return vectors, nil
}

// Close releases the model.
func (l *Local) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.session == nil {
		return nil
	}
	return l.session.Destroy()
}

// load downloads and initialises the model once.
func (l *Local) load(ctx context.Context) error {
	l.once.Do(func() {
		// Already wired, by a test or a previous caller.
		if l.run != nil {
			return
		}

		dir, err := l.cacheDir()
		if err != nil {
			l.err = err
			return
		}

		// A request ending must not cancel the shared runtime's lifetime.
		// Embed checks cancellation between completed single-input calls.
		session, backend, err := newBestSession(context.WithoutCancel(ctx))
		if err != nil {
			l.err = fmt.Errorf("start the embedding runtime: %w", err)
			return
		}
		l.backend.Store(backend)

		path, err := modelPath(ctx, dir)
		if err != nil {
			_ = session.Destroy()
			l.err = fmt.Errorf(
				"download %s into %s: %w. The first search needs to fetch the model; "+
					"use -embed http to point at a model you already run, or -embed none to work without search",
				LocalModel, dir, err)
			return
		}

		pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
			ModelPath:    path,
			OnnxFilename: localOnnx,
			Name:         "gandalf-embeddings",
		})
		if err != nil {
			_ = session.Destroy()
			l.err = fmt.Errorf("prepare the embedding pipeline: %w", err)
			return
		}

		l.session, l.pipeline = session, pipeline
		l.run = func(ctx context.Context, texts []string) ([][]float32, error) {
			return runLocalPipeline(ctx, pipeline, texts)
		}
	})

	return l.err
}

// localFiles are the files the pipeline reads from the model directory. When
// all of them are present the model is used as it is.
var localFiles = []string{localOnnx, "tokenizer.json", "config.json"}

// modelPath returns the directory holding the model, downloading it only when
// the cache under dir is incomplete.
//
// hugot.DownloadModel asks Hugging Face for the repository's file list on
// every call, even when every file is already on disk, so going through it
// unconditionally made search depend on the network, and on the health of
// Hugging Face's own cache, at every start.
func modelPath(ctx context.Context, dir string) (string, error) {
	path := filepath.Join(dir, strings.ReplaceAll(LocalModel, "/", "_"))
	if complete(path) {
		return path, nil
	}
	return hugot.DownloadModel(ctx, LocalModel, dir, hugot.NewDownloadOptions())
}

// complete reports whether every file the pipeline needs is present and
// non-empty in path.
func complete(path string) bool {
	for _, name := range localFiles {
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

// cacheDir returns where the model is stored, creating it if needed.
//
// The model is cached outside the vault: it is a shared, re-downloadable
// artifact of this machine, not part of anybody's notes.
func (l *Local) cacheDir() (string, error) {
	dir := l.Dir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("find a cache directory for the embedding model: %w", err)
		}
		dir = filepath.Join(base, "gandalf", "models")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create model cache %s: %w", dir, err)
	}
	return dir, nil
}
