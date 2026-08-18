package server

import (
	"strings"
	"testing"
)

// TestOneRefPerNote is the invariant that keeps results usable: whatever ref a
// caller passes, the ref that comes back is the same one every other tool will
// report for that note. Two names for one note would have boot, lint, and
// search disagreeing about what to call it.
func TestOneRefPerNote(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name     string
		aliases  []string
		wantRef  string
		wantSame bool
	}{
		{
			name:    "a seeded standard reached by topic id or by kind",
			aliases: []string{"topic:code-quality", "standard:code-quality", "Standards/code-quality.md"},
			wantRef: "standard:code-quality",
		},
		{
			name:    "an operating topic, which the layout cannot name",
			aliases: []string{"topic:shipping", "Gandalf/Shipping.md"},
			wantRef: "topic:shipping",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, alias := range tc.aliases {
				ref, _, err := h.serverRef(alias)
				if err != nil {
					// Paths are rejected, but the suggestion they carry must
					// still be the canonical ref.
					if !strings.Contains(err.Error(), tc.wantRef) {
						t.Errorf("%q: error %q does not name %q", alias, err, tc.wantRef)
					}
					continue
				}
				if ref != tc.wantRef {
					t.Errorf("%q resolved to %q, want %q", alias, ref, tc.wantRef)
				}
			}
		})
	}
}

// TestBootTopicsUseCanonicalRefs checks the table boot hands out is addressed
// the same way as everything else.
func TestBootTopicsUseCanonicalRefs(t *testing.T) {
	h := newHarness(t)

	var boot BootOutput
	h.call("boot", BootInput{}, &boot)

	for _, topic := range boot.Topics {
		ref, _, err := h.serverRef(topic.Ref)
		if err != nil {
			t.Errorf("%q: %v", topic.Ref, err)
			continue
		}
		if ref != topic.Ref {
			t.Errorf("boot advertised %q but it canonicalises to %q", topic.Ref, ref)
		}

		// Standards live where the layout can name them; operating topics do not.
		if strings.HasPrefix(topic.Ref, "standard:") || strings.HasPrefix(topic.Ref, "topic:") {
			continue
		}
		t.Errorf("unexpected topic ref kind: %q", topic.Ref)
	}
}

// serverRef resolves a ref through the server, returning its canonical form.
func (h *harness) serverRef(raw string) (string, string, error) {
	h.t.Helper()

	s := New(h.vault, "test")
	ref, path, err := s.resolve(raw)
	if err != nil {
		return "", "", err
	}
	return ref.String(), path, nil
}
