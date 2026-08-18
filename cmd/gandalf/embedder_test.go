package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/embed"
)

func TestEmbedderFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantNil bool
		check   func(*testing.T, embed.Embedder)
		wantErr string
	}{
		{
			// The default runs in this process, so search works on a fresh
			// install without anybody standing up an inference server first.
			name: "defaults need no external service",
			args: nil,
			check: func(t *testing.T, e embed.Embedder) {
				if _, ok := e.(*embed.Local); !ok {
					t.Fatalf("embedder = %T, want the in-process model", e)
				}
				if e.Window() <= 0 || e.Dims() <= 0 {
					t.Errorf("embedder reports window %d and dims %d", e.Window(), e.Dims())
				}
			},
		},
		{
			name: "an existing endpoint is one flag away",
			args: []string{"-embed", "http"},
			check: func(t *testing.T, e embed.Embedder) {
				h, ok := e.(embed.HTTP)
				if !ok {
					t.Fatalf("embedder = %T, want HTTP", e)
				}
				if h.BaseURL != defaultEmbedURL || h.ModelName != defaultEmbedModel {
					t.Errorf("endpoint = %s %s", h.BaseURL, h.ModelName)
				}
				if h.Dims() != defaultEmbedDims || h.Window() != defaultEmbedWindow {
					t.Errorf("dims = %d, window = %d", h.Dims(), h.Window())
				}
			},
		},
		{
			name: "a homelab endpoint is fully configurable",
			args: []string{
				"-embed", "http",
				"-embed-url", "http://192.168.1.5:8081/v1",
				"-embed-model", "bge-small", "-embed-dims", "384", "-embed-window", "512",
			},
			check: func(t *testing.T, e embed.Embedder) {
				h := e.(embed.HTTP)
				if h.BaseURL != "http://192.168.1.5:8081/v1" || h.ModelName != "bge-small" {
					t.Errorf("embedder = %+v", h)
				}
				if h.Dims() != 384 || h.Window() != 512 {
					t.Errorf("dims = %d, window = %d", h.Dims(), h.Window())
				}
			},
		},
		{name: "search can be switched off", args: []string{"-embed", "none"}, wantNil: true},
		{name: "off is accepted too", args: []string{"-embed", "off"}, wantNil: true},

		{name: "an unknown backend", args: []string{"-embed", "magic"}, wantErr: "unknown embedding backend"},
		{name: "http with no url", args: []string{"-embed", "http", "-embed-url", ""}, wantErr: "-embed-url is required"},
		{name: "http with no model", args: []string{"-embed", "http", "-embed-model", ""}, wantErr: "-embed-model is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var flags embedderFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.register(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("Parse: %v", err)
			}

			embedder, err := flags.build()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("build() = %v, want an error mentioning %q", embedder, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("build(): %v", err)
			}

			if tc.wantNil {
				if embedder != nil {
					t.Errorf("embedder = %v, want none", embedder)
				}
				return
			}
			if embedder == nil {
				t.Fatal("embedder is nil")
			}
			if tc.check != nil {
				tc.check(t, embedder)
			}
		})
	}
}

func TestReindexRequiresAnEmbedder(t *testing.T) {
	silence(t)

	root := t.TempDir()
	if err := run([]string{"init", "-vault", root}); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := run([]string{"reindex", "-vault", root, "-embed", "none"})
	if err == nil {
		t.Fatal("reindex with no embedder succeeded")
	}
	if !strings.Contains(err.Error(), "needs an embedding model") {
		t.Errorf("error = %q", err)
	}
}

// TestReindexReportsAnUnreachableModel checks the first failure a new user
// meets explains itself rather than dumping a dial error.
func TestReindexReportsAnUnreachableModel(t *testing.T) {
	silence(t)

	root := t.TempDir()
	if err := run([]string{"init", "-vault", root}); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := run([]string{"reindex", "-vault", root, "-embed", "http", "-embed-url", "http://127.0.0.1:1/v1"})
	if err == nil {
		t.Fatal("reindex succeeded against a closed port")
	}
	for _, want := range []string{"unreachable", "-embed none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestServeAcceptsEmbeddingFlags(t *testing.T) {
	silence(t)

	// Bad embedding configuration must fail before the server starts, not
	// halfway through a session.
	if err := run([]string{"serve", "-vault", t.TempDir(), "-embed", "magic"}); err == nil {
		t.Error("serve accepted an unknown embedding backend")
	}
}
