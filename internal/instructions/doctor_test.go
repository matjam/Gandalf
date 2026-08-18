package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/vault"
)

// stateOf returns the reported state for one document path.
func stateOf(t *testing.T, v *vault.Vault, path string) State {
	t.Helper()

	statuses, err := Doctor(v)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	for _, s := range statuses {
		if s.Doc.Path == path {
			return s.State
		}
	}
	t.Fatalf("no status reported for %q", path)
	return ""
}

func TestDoctorOnFreshVault(t *testing.T) {
	v := seeded(t)

	statuses, err := Doctor(v)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if got := Count(statuses, StateCurrent); got != len(Docs()) {
		t.Errorf("%d current, want %d: %+v", got, len(Docs()), statuses)
	}
	for _, s := range statuses {
		if s.SeededVersion != Version {
			t.Errorf("%s seeded version = %d, want %d", s.Doc.Path, s.SeededVersion, Version)
		}
	}
}

func TestDoctorOnEmptyVault(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	statuses, err := Doctor(v)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if got := Count(statuses, StateAbsent); got != len(Docs()) {
		t.Errorf("%d absent, want %d", got, len(Docs()))
	}
}

func TestDoctorDetectsLocalEdits(t *testing.T) {
	v := seeded(t)

	note, err := v.Read("Gandalf/Shipping.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	note.Body += "\n## Local Addition\n\nAlways rebase.\n"
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := stateOf(t, v, "Gandalf/Shipping.md"); got != StateModified {
		t.Errorf("state = %q, want %q", got, StateModified)
	}
}

// TestDoctorIgnoresWhitespaceOnlyChanges guards the normalisation that stops a
// trailing newline from being reported as a user edit.
func TestDoctorIgnoresWhitespaceOnlyChanges(t *testing.T) {
	v := seeded(t)

	abs := filepath.Join(v.Root(), filepath.FromSlash("Gandalf/Memory.md"))
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(abs, []byte(string(data)+"\n\n\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := stateOf(t, v, "Gandalf/Memory.md"); got != StateCurrent {
		t.Errorf("state = %q, want %q", got, StateCurrent)
	}
}

func TestDoctorDetectsUnmanagedFiles(t *testing.T) {
	v := seeded(t)

	// The user's own file, with no seed stamp, at a path the manifest claims.
	writeRaw(t, v, "Standards/security.md",
		"---\ntype: standard\ncreated: 2026-01-01\nupdated: 2026-01-01\ntags: [mine]\nauthor: user\n---\n\n# Mine\n")

	if got := stateOf(t, v, "Standards/security.md"); got != StateUnmanaged {
		t.Errorf("state = %q, want %q", got, StateUnmanaged)
	}
}

func TestDoctorDetectsDeletedFiles(t *testing.T) {
	v := seeded(t)

	if err := os.Remove(filepath.Join(v.Root(), filepath.FromSlash("Standards/database.md"))); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if got := stateOf(t, v, "Standards/database.md"); got != StateAbsent {
		t.Errorf("state = %q, want %q", got, StateAbsent)
	}
}

// TestDoctorDetectsUpstreamChanges simulates a vault seeded from a different
// release. The stamp records what the body looked like when it was written, so
// a body matching its own stamp means the user changed nothing, however far
// the shipped version has since moved.
func TestDoctorDetectsUpstreamChanges(t *testing.T) {
	tests := []struct {
		name string

		// body is what the vault copy contains; stamp is what it records
		// having been seeded from.
		body  string
		stamp func(body string) string
		want  State
	}{
		{
			name:  "seeded from an older release, never edited",
			body:  "# Diagnostics\n\nAn older release said this.\n",
			stamp: HashBody,
			want:  StateOutdated,
		},
		{
			name:  "seeded from an older release, then edited",
			body:  "# Diagnostics\n\nAn older release said this.\n\nAnd the user added this.\n",
			stamp: func(string) string { return HashBody("# Diagnostics\n\nAn older release said this.\n") },
			want:  StateDiverged,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := seeded(t)

			note, err := v.Read("Gandalf/Diagnostics.md")
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			note.Body = tc.body
			note.FM.Extra[KeySeed] = tc.stamp(tc.body)
			if err := v.Write(note); err != nil {
				t.Fatalf("Write: %v", err)
			}

			if got := stateOf(t, v, "Gandalf/Diagnostics.md"); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDoctorNeverWrites(t *testing.T) {
	v := seeded(t)

	before := snapshot(t, v)
	if _, err := Doctor(v); err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if snapshot(t, v) != before {
		t.Error("Doctor modified the vault")
	}
}

func TestStatusString(t *testing.T) {
	s := Status{Doc: Doc{Path: "Gandalf/Operating.md"}, State: StateModified}
	if got := s.String(); !strings.Contains(got, "modified") || !strings.Contains(got, "Gandalf/Operating.md") {
		t.Errorf("String() = %q", got)
	}
}
