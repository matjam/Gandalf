package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/embed"
)

// embedderFlags configures how text is turned into vectors.
type embedderFlags struct {
	backend string
	url     string
	model   string
	dims    int
	tokens  int
	dir     string
}

// Defaults point at Ollama on this machine, because that is the most common
// way to have an embedding model available locally and it costs nothing when
// it is absent: search reports the endpoint is unreachable and every other
// tool carries on working.
const (
	defaultEmbedURL   = "http://localhost:11434/v1"
	defaultEmbedModel = "nomic-embed-text"
	defaultEmbedDims  = 768

	// nomic-embed-text accepts 8192 tokens. The window cannot be discovered
	// over the API, so it is configured alongside the model it belongs to.
	defaultEmbedWindow = 8192
)

// register adds the embedding flags to a flag set.
func (e *embedderFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&e.backend, "embed", "local", "embedding backend: local, http, or none to disable search")
	fs.StringVar(&e.dir, "embed-cache", "", "where the local model is cached (default: user cache directory)")
	fs.StringVar(&e.url, "embed-url", defaultEmbedURL, "OpenAI-compatible embeddings endpoint")
	fs.StringVar(&e.model, "embed-model", defaultEmbedModel, "embedding model name")
	fs.IntVar(&e.dims, "embed-dims", defaultEmbedDims, "vector length the model returns")
	fs.IntVar(&e.tokens, "embed-window", defaultEmbedWindow, "model context window in tokens; notes are chunked to fit")
}

// build returns the configured embedder, or nil when search is disabled.
func (e *embedderFlags) build() (embed.Embedder, error) {
	switch strings.ToLower(strings.TrimSpace(e.backend)) {
	case "none", "off", "":
		return nil, nil

	case "local":
		// Nothing is downloaded here. The model is fetched on the first
		// search, so a session that never searches never waits for it.
		return embed.NewLocal(e.dir), nil

	case "http":
		if e.url == "" {
			return nil, fmt.Errorf("-embed-url is required for the http backend")
		}
		if e.model == "" {
			return nil, fmt.Errorf("-embed-model is required for the http backend")
		}
		return embed.HTTP{
			BaseURL:    e.url,
			ModelName:  e.model,
			Dimensions: e.dims,
			Tokens:     e.tokens,
		}, nil

	default:
		return nil, fmt.Errorf("unknown embedding backend %q (want local, http, or none)", e.backend)
	}
}
