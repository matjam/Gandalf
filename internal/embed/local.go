package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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
	err      error
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
}

// NewLocal returns an in-process embedder. The model is not loaded until the
// first call, so constructing one is free and a session that never searches
// never pays for it.
func NewLocal(dir string) *Local { return &Local{Dir: dir} }

// Model identifies the embedding model.
func (l *Local) Model() string { return LocalModel }

// Dims is the vector length.
func (l *Local) Dims() int { return localDims }

// Window is the model's context in tokens.
func (l *Local) Window() int { return localWindow }

// Embed returns one vector per input.
func (l *Local) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	if err := l.load(ctx); err != nil {
		return nil, err
	}

	embeddings, err := l.run(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed with %s: %w", l.Model(), err)
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("model returned %d embeddings for %d inputs", len(embeddings), len(texts))
	}

	vectors := make([]Vector, len(embeddings))
	for i, e := range embeddings {
		if len(e) != localDims {
			return nil, fmt.Errorf("model returned %d dimensions, expected %d", len(e), localDims)
		}
		// Normalised on the way in, so cosine similarity is a dot product and
		// stored vectors are comparable regardless of input length.
		vectors[i] = Normalise(Vector(e))
	}

	return vectors, nil
}

// Close releases the model.
func (l *Local) Close() error {
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

		session, err := hugot.NewGoSession(ctx)
		if err != nil {
			l.err = fmt.Errorf("start the embedding runtime: %w", err)
			return
		}

		path, err := hugot.DownloadModel(ctx, LocalModel, dir, hugot.NewDownloadOptions())
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
			out, err := pipeline.RunPipeline(ctx, texts)
			if err != nil {
				return nil, err
			}
			return out.Embeddings, nil
		}
	})

	return l.err
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
