package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
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

func TestHTTPEmptyInput(t *testing.T) {
	h := HTTP{BaseURL: "http://127.0.0.1:1", ModelName: "test-model"}

	vectors, err := h.Embed(context.Background(), nil)
	if err != nil || vectors != nil {
		t.Errorf("Embed(nil) = %v, %v; want no work and no error", vectors, err)
	}
}
