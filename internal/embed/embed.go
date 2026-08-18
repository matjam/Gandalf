// Package embed turns text into vectors, behind an interface with more than
// one implementation.
//
// Which model produces the vectors is a deployment question, not a design one:
// a laptop with no setup, a homelab endpoint, and a test that must not touch a
// network all want different answers. The index does not care, so long as the
// vectors it stores and the vectors it queries with came from the same model —
// which is why Model and Dims are part of the interface rather than
// configuration read separately.
package embed

import (
	"context"
	"fmt"
	"math"
)

// Vector is an embedding.
type Vector []float32

// Embedder turns text into vectors.
type Embedder interface {
	// Embed returns one vector per input, in the same order.
	Embed(ctx context.Context, texts []string) ([]Vector, error)

	// Model identifies what produced the vectors. Stored alongside them, so a
	// changed model is detected rather than silently compared against.
	Model() string

	// Dims is the length of the vectors this embedder produces.
	Dims() int

	// Window is the most tokens the model accepts in one input.
	//
	// It is part of the interface because the alternative is silent
	// truncation: a small model given a long chunk embeds the beginning and
	// discards the rest, producing a vector that confidently represents half a
	// section. Declaring the limit lets the caller split text to fit rather
	// than find out by getting worse results.
	Window() int
}

// RunesPerToken is the conversion used when turning a token window into a
// character budget.
//
// English averages around four characters per token; three is deliberately
// pessimistic, because overrunning the window loses text silently while
// undershooting only costs an extra chunk.
const RunesPerToken = 3

// Budget returns how many runes of text fit in a model's window, leaving room
// for the heading and title prepended to each chunk.
func Budget(e Embedder) int {
	window := e.Window()
	if window <= 0 {
		window = 512
	}

	budget := window * RunesPerToken * 4 / 5
	if budget < 200 {
		budget = 200
	}
	return budget
}

// Cosine returns the cosine similarity of two vectors, from -1 to 1.
//
// Vectors of different lengths score zero rather than panicking: that
// mismatch means the index was built by a different model, which the caller
// should detect and rebuild rather than crash over.
func Cosine(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Normalise scales a vector to unit length, leaving a zero vector alone.
func Normalise(v Vector) Vector {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return v
	}

	norm = math.Sqrt(norm)
	out := make(Vector, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

// Encode serialises a vector for storage.
func Encode(v Vector) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		bits := math.Float32bits(f)
		out[4*i] = byte(bits)
		out[4*i+1] = byte(bits >> 8)
		out[4*i+2] = byte(bits >> 16)
		out[4*i+3] = byte(bits >> 24)
	}
	return out
}

// Decode reads a stored vector.
func Decode(data []byte) (Vector, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("stored vector has %d bytes, which is not a whole number of floats", len(data))
	}

	out := make(Vector, len(data)/4)
	for i := range out {
		bits := uint32(data[4*i]) |
			uint32(data[4*i+1])<<8 |
			uint32(data[4*i+2])<<16 |
			uint32(data[4*i+3])<<24
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
