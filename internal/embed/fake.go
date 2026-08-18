package embed

import (
	"context"
	"hash/fnv"
	"strings"
	"unicode"
)

// FakeModel is the model name recorded by the deterministic embedder.
const FakeModel = "fake-hashed-bag-of-words"

// Fake embeds text by hashing its words into a fixed number of buckets.
//
// It is deterministic, needs no network, and — because it is lexical rather
// than arbitrary — text that shares words scores as similar. That last part
// matters: a fake returning random vectors would let a search test pass while
// ranking nonsense, so the tests would prove only that the code ran.
type Fake struct {
	// Dimensions is the vector length. Zero means the default.
	Dimensions int
}

// Embed returns one vector per input.
func (f Fake) Embed(_ context.Context, texts []string) ([]Vector, error) {
	out := make([]Vector, 0, len(texts))
	for _, text := range texts {
		out = append(out, f.vector(text))
	}
	return out, nil
}

// Model identifies the fake.
func (f Fake) Model() string { return FakeModel }

// Dims is the vector length.
func (f Fake) Dims() int {
	if f.Dimensions > 0 {
		return f.Dimensions
	}
	return 128
}

// vector hashes each word into a bucket and counts them, then normalises so
// that length does not dominate similarity.
func (f Fake) vector(text string) Vector {
	v := make(Vector, f.Dims())

	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		v[int(h.Sum32())%len(v)]++
	}

	return Normalise(v)
}
