package embed

import (
	"context"
	"math"
	"os"
	"testing"
)

// TestLocalAgainstTheRealModel downloads and runs the embedding model.
//
// It is opt-in because it fetches roughly 130MB and takes a while: a test
// suite that needs the network is a test suite people stop running. Set
// GANDALF_MODEL_TESTS=1 to include it, which is also how a release check
// should confirm the shipped model still resolves and still produces the
// dimensions the index expects.
func TestLocalAgainstTheRealModel(t *testing.T) {
	if os.Getenv("GANDALF_MODEL_TESTS") == "" {
		t.Skip("set GANDALF_MODEL_TESTS=1 to download and run the embedding model")
	}

	l := NewLocal(t.TempDir())
	defer l.Close()

	ctx := context.Background()
	// Deliberately unambiguous. An earlier version compared two infrastructure
	// sentences against a third, and the model scored them within noise of
	// each other — reasonably, since they shared a vocabulary. A test of
	// "similar meaning scores higher" needs a pair that is similar in meaning
	// and different in words, against something plainly unrelated.
	vectors, err := l.Embed(ctx, []string{
		"the cat sat on the mat",
		"a feline rested upon the rug",
		"database migrations run before the service accepts traffic",
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	for i, v := range vectors {
		if len(v) != l.Dims() {
			t.Fatalf("vector %d has %d dimensions, want %d", i, len(v), l.Dims())
		}
		if math.Abs(Cosine(v, v)-1) > 1e-5 {
			t.Errorf("vector %d is not unit length", i)
		}
	}

	// The point of a real model rather than the lexical fake: these two share
	// almost no words but mean nearly the same thing.
	related := Cosine(vectors[0], vectors[1])
	unrelated := Cosine(vectors[0], vectors[2])
	if related <= unrelated {
		t.Errorf("related texts scored %.3f, unrelated %.3f; want related higher", related, unrelated)
	}
}
