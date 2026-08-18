package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

func on(t *testing.T) schema.Date {
	t.Helper()
	d, err := schema.ParseDate("2026-08-17")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	return d
}

// seeded returns a vault with the full instruction set written into it.
func seeded(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Seed(v, on(t)); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return v
}

func TestSeedWritesEveryDocument(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	results, err := Seed(v, on(t))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(results) != len(Docs()) {
		t.Fatalf("got %d results, want %d", len(results), len(Docs()))
	}
	if got := Created(results); got != len(Docs()) {
		t.Errorf("created %d documents, want %d", got, len(Docs()))
	}

	for _, doc := range Docs() {
		if !v.Exists(doc.Path) {
			t.Errorf("%s was not written", doc.Path)
		}
	}
}

// TestSeededVaultPassesItsOwnLinter is the one that matters: a vault seeded
// from the shipped defaults must satisfy the schema those defaults describe,
// warnings included.
func TestSeededVaultPassesItsOwnLinter(t *testing.T) {
	v := seeded(t)

	findings, err := v.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) > 0 {
		lines := make([]string, len(findings))
		for i, f := range findings {
			lines[i] = "  " + f.String()
		}
		t.Errorf("seeded vault is not clean:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	v := seeded(t)

	before := snapshot(t, v)

	results, err := Seed(v, on(t))
	if err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	if got := Created(results); got != 0 {
		t.Errorf("second seed created %d documents, want 0", got)
	}
	for _, r := range results {
		if r.Reason != "already present" {
			t.Errorf("%s skipped with reason %q", r.Doc.Path, r.Reason)
		}
	}

	if after := snapshot(t, v); after != before {
		t.Error("second seed changed the vault")
	}
}

func TestSeedNeverOverwrites(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// A file the user wrote first, at a path the instruction set also wants.
	const mine = "---\ntype: standard\ncreated: 2026-01-01\nupdated: 2026-01-01\ntags: [mine]\nauthor: user\n---\n\n# My Own Standard\n"
	writeRaw(t, v, "Standards/database.md", mine)

	if _, err := Seed(v, on(t)); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	got := readRaw(t, v, "Standards/database.md")
	if got != mine {
		t.Errorf("seeding overwrote a user's file:\n%s", got)
	}
}

func TestSeedStampsProvenance(t *testing.T) {
	v := seeded(t)

	doc, ok := Lookup("operating")
	if !ok {
		t.Fatal("operating document is missing from the manifest")
	}

	note, err := v.Read(doc.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := note.FM.Extra[KeyVersion]; got != Version && got != uint64(Version) {
		t.Errorf("%s = %v (%T), want %d", KeyVersion, got, got, Version)
	}

	want, err := doc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got := note.FM.Extra[KeySeed]; got != want {
		t.Errorf("%s = %v, want %q", KeySeed, got, want)
	}

	// The stamp must survive a round trip, or doctor reports drift that never
	// happened.
	if err := v.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}
	again, err := v.Read(doc.Path)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if again.FM.Extra[KeySeed] != want {
		t.Errorf("seed stamp did not survive a rewrite: %v", again.FM.Extra[KeySeed])
	}
}

func TestSeedDefaultsToToday(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Seed(v, schema.Date{}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	note, err := v.Read("README.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !note.FM.Created.Equal(schema.Today()) {
		t.Errorf("created = %s, want today", note.FM.Created)
	}
}

// snapshot returns a stable representation of every file in the vault.
func snapshot(t *testing.T, v *vault.Vault) string {
	t.Helper()

	paths, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p)
		b.WriteString("\n")
		b.WriteString(readRaw(t, v, p))
	}
	return b.String()
}

func writeRaw(t *testing.T, v *vault.Vault, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readRaw(t *testing.T, v *vault.Vault, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}
