package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b Vector
		want float64
	}{
		{name: "identical", a: Vector{1, 0, 0}, b: Vector{1, 0, 0}, want: 1},
		{name: "opposite", a: Vector{1, 0}, b: Vector{-1, 0}, want: -1},
		{name: "orthogonal", a: Vector{1, 0}, b: Vector{0, 1}, want: 0},
		{name: "scale does not matter", a: Vector{2, 0}, b: Vector{9, 0}, want: 1},

		// A length mismatch means the index was built by another model.
		// Scoring zero lets the caller notice and rebuild rather than crash.
		{name: "different lengths", a: Vector{1, 0}, b: Vector{1, 0, 0}, want: 0},
		{name: "empty", a: Vector{}, b: Vector{}, want: 0},
		{name: "zero vector", a: Vector{0, 0}, b: Vector{1, 0}, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Cosine(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Cosine() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := Vector{0, 1, -1, 0.5, math.MaxFloat32, math.SmallestNonzeroFloat32}

	out, err := Decode(Encode(in))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d values, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("value %d = %v, want %v", i, out[i], in[i])
		}
	}

	if _, err := Decode([]byte{1, 2, 3}); err == nil {
		t.Error("Decode accepted a truncated vector")
	}
}

// TestFakeIsLexical matters because the search tests rely on it: a fake
// returning arbitrary vectors would let them pass while ranking nonsense.
func TestFakeIsLexical(t *testing.T) {
	f := Fake{}
	ctx := context.Background()

	vectors, err := f.Embed(ctx, []string{
		"cache invalidation and stampede protection",
		"cache stampede protection",
		"database migrations run before traffic",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	related := Cosine(vectors[0], vectors[1])
	unrelated := Cosine(vectors[0], vectors[2])
	if related <= unrelated {
		t.Errorf("related texts scored %v, unrelated %v; want related to score higher", related, unrelated)
	}

	// Deterministic across calls, or an index would be invalidated by a restart.
	again, err := f.Embed(ctx, []string{"cache invalidation and stampede protection"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if Cosine(vectors[0], again[0]) < 1-1e-9 {
		t.Error("the same text produced different vectors")
	}

	if got := len(vectors[0]); got != f.Dims() {
		t.Errorf("vector length = %d, want %d", got, f.Dims())
	}
}

// stubEndpoint serves OpenAI-compatible embedding responses.
func stubEndpoint(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHTTPEmbed(t *testing.T) {
	var gotModel string
	var gotInput []string

	url := stubEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}

		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel, gotInput = req.Model, req.Input

		// Answer out of order, to prove the index field is honoured.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0, 1}},
				{"index": 0, "embedding": []float32{1, 0}},
			},
		})
	})

	h := HTTP{BaseURL: url, ModelName: "test-model", Dimensions: 2, APIKey: "secret"}

	vectors, err := h.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if gotModel != "test-model" || strings.Join(gotInput, ",") != "first,second" {
		t.Errorf("request sent model %q input %v", gotModel, gotInput)
	}
	if vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Errorf("vectors came back in the wrong order: %v", vectors)
	}
}

func TestHTTPEmbedErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		dims    int
		want    string
	}{
		{
			name: "an error payload",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"message": "model not found"},
				})
			},
			want: "model not found",
		},
		{
			name: "a wrong number of vectors",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"index": 0, "embedding": []float32{1, 0}}},
				})
			},
			want: "asked for 2 embeddings",
		},
		{
			name: "a dimension mismatch",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{
						{"index": 0, "embedding": []float32{1, 0, 0}},
						{"index": 1, "embedding": []float32{0, 1, 0}},
					},
				})
			},
			dims: 2,
			want: "reindex",
		},
		{
			name: "an unparseable response",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("not json"))
			},
			want: "decode embedding response",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dims := tc.dims
			if dims == 0 {
				dims = 2
			}
			h := HTTP{BaseURL: stubEndpoint(t, tc.handler), ModelName: "test-model", Dimensions: dims}

			_, err := h.Embed(context.Background(), []string{"first", "second"})
			if err == nil {
				t.Fatalf("Embed() = nil, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestHTTPUnreachableExplainsItself covers the first error a new user meets.
func TestHTTPUnreachableExplainsItself(t *testing.T) {
	h := HTTP{BaseURL: "http://127.0.0.1:1", ModelName: "test-model", Dimensions: 2}

	_, err := h.Embed(context.Background(), []string{"anything"})
	if err == nil {
		t.Fatal("Embed succeeded against a closed port")
	}
	for _, want := range []string{"unreachable", "-embed none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestBudgetTracksTheWindow covers the conversion that keeps chunks inside a
// model's context, where overrunning loses text silently.
func TestBudgetTracksTheWindow(t *testing.T) {
	tests := []struct {
		name     string
		embedder Embedder
		check    func(*testing.T, int)
	}{
		{
			name:     "a small sentence encoder",
			embedder: Fake{Tokens: 256},
			check: func(t *testing.T, budget int) {
				if budget > 256*RunesPerToken {
					t.Errorf("budget %d exceeds the window in characters", budget)
				}
				if budget < 200 {
					t.Errorf("budget %d is too small to be useful", budget)
				}
			},
		},
		{
			name:     "a large model",
			embedder: Fake{Tokens: 8192},
			check: func(t *testing.T, budget int) {
				if budget <= Budget(Fake{Tokens: 256}) {
					t.Error("a larger window did not produce a larger budget")
				}
			},
		},
		{
			name:     "an unspecified window falls back to something safe",
			embedder: Fake{Tokens: -1},
			check: func(t *testing.T, budget int) {
				if budget <= 0 {
					t.Errorf("budget = %d", budget)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Budget(tc.embedder))
		})
	}
}

// TestLocalDescribesItself covers what the index depends on before any model
// is downloaded: the vectors' shape and the window chunks are sized against.
// Running the model needs a network fetch, so it is left to the end-to-end
// checks rather than the test suite.
func TestLocalDescribesItself(t *testing.T) {
	l := NewLocal(t.TempDir())

	if l.Model() != LocalModel {
		t.Errorf("model = %q, want %q", l.Model(), LocalModel)
	}
	if l.Dims() != localDims {
		t.Errorf("dims = %d, want %d", l.Dims(), localDims)
	}
	if l.Window() != localWindow {
		t.Errorf("window = %d, want %d", l.Window(), localWindow)
	}

	// Constructing must not load anything: a session that never searches
	// should never wait for a model.
	if l.pipeline != nil || l.session != nil {
		t.Error("the model was loaded before it was needed")
	}

	// No input means no work, and so no download.
	vectors, err := l.Embed(context.Background(), nil)
	if err != nil || vectors != nil {
		t.Errorf("Embed(nil) = %v, %v; want no work and no error", vectors, err)
	}
	if l.pipeline != nil {
		t.Error("an empty call loaded the model")
	}

	if err := l.Close(); err != nil {
		t.Errorf("Close on an unloaded embedder: %v", err)
	}
}

// TestLocalValidatesWhatTheModelReturns exercises the orchestration around the
// model — validation, normalisation, error wrapping — with the model itself
// stubbed, since running the real one needs a download.
func TestLocalValidatesWhatTheModelReturns(t *testing.T) {
	ctx := context.Background()

	t.Run("vectors are normalised", func(t *testing.T) {
		l := &Local{run: func(_ context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, len(texts))
			for i := range out {
				// Unnormalised, and longer than unit length.
				out[i] = make([]float32, localDims)
				out[i][0] = 3
				out[i][1] = 4
			}
			return out, nil
		}}

		vectors, err := l.Embed(ctx, []string{"one", "two"})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		if len(vectors) != 2 {
			t.Fatalf("got %d vectors", len(vectors))
		}
		if got := Cosine(vectors[0], vectors[0]); math.Abs(got-1) > 1e-6 {
			t.Errorf("self-similarity = %v, want 1", got)
		}
		if got := float64(vectors[0][0]); math.Abs(got-0.6) > 1e-6 {
			t.Errorf("first component = %v, want the vector scaled to unit length", got)
		}
	})

	tests := []struct {
		name string
		run  func(context.Context, []string) ([][]float32, error)
		want string
	}{
		{
			name: "a short batch",
			run: func(_ context.Context, _ []string) ([][]float32, error) {
				return [][]float32{make([]float32, localDims)}, nil
			},
			want: "1 embeddings for 2 inputs",
		},
		{
			name: "the wrong dimensions",
			run: func(_ context.Context, texts []string) ([][]float32, error) {
				out := make([][]float32, len(texts))
				for i := range out {
					out[i] = make([]float32, 7)
				}
				return out, nil
			},
			want: "7 dimensions",
		},
		{
			name: "the model failing",
			run: func(_ context.Context, _ []string) ([][]float32, error) {
				return nil, errStub
			},
			want: LocalModel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := &Local{run: tc.run}

			_, err := l.Embed(ctx, []string{"one", "two"})
			if err == nil {
				t.Fatalf("Embed() = nil, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// errStub stands in for a failure inside the model.
var errStub = fmt.Errorf("model exploded")

func TestLocalCacheDir(t *testing.T) {
	t.Run("an explicit directory is created", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "models", "nested")

		got, err := (&Local{Dir: dir}).cacheDir()
		if err != nil {
			t.Fatalf("cacheDir: %v", err)
		}
		if got != dir {
			t.Errorf("cacheDir = %q, want %q", got, dir)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("directory was not created: %v", err)
		}
	})

	t.Run("the default lives outside any vault", func(t *testing.T) {
		got, err := (&Local{}).cacheDir()
		if err != nil {
			t.Fatalf("cacheDir: %v", err)
		}
		// The model is a shared artifact of this machine, not part of anyone's
		// notes, so it must not land in a vault.
		if !strings.Contains(got, "gandalf") {
			t.Errorf("cacheDir = %q, want it under a gandalf directory", got)
		}
		if strings.Contains(got, ".gandalf") {
			t.Errorf("cacheDir = %q, which is inside a vault", got)
		}
	})
}

// TestEmbeddersDescribeThemselves covers the accessors the index relies on to
// decide whether a stored vector is comparable with a fresh one.
func TestEmbeddersDescribeThemselves(t *testing.T) {
	embedders := []Embedder{
		Fake{},
		Fake{Dimensions: 64, Tokens: 256},
		HTTP{ModelName: "nomic-embed-text", Dimensions: 768, Tokens: 8192},
		NewLocal(t.TempDir()),
	}

	for _, e := range embedders {
		t.Run(e.Model(), func(t *testing.T) {
			if e.Model() == "" {
				t.Error("no model name, so a changed model could not be detected")
			}
			if e.Dims() <= 0 {
				t.Errorf("dims = %d", e.Dims())
			}
			if Budget(e) <= 0 {
				t.Errorf("budget = %d", Budget(e))
			}
		})
	}
}

func TestHTTPWindow(t *testing.T) {
	if got := (HTTP{}).Window(); got != 512 {
		t.Errorf("default window = %d, want a conservative 512", got)
	}
	if got := (HTTP{Tokens: 8192}).Window(); got != 8192 {
		t.Errorf("configured window = %d", got)
	}
}

func TestHTTPEmptyInput(t *testing.T) {
	h := HTTP{BaseURL: "http://127.0.0.1:1", ModelName: "test-model"}

	vectors, err := h.Embed(context.Background(), nil)
	if err != nil || vectors != nil {
		t.Errorf("Embed(nil) = %v, %v; want no work and no error", vectors, err)
	}
}
