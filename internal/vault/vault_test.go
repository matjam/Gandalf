package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts raw content at a vault-relative path, bypassing the note API so
// tests can plant malformed files.
func write(t *testing.T, v *Vault, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func TestOpenCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new", "vault")

	v, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if info, err := os.Stat(v.Root()); err != nil || !info.IsDir() {
		t.Fatalf("root was not created: %v", err)
	}
}

func TestOpenRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Error("Open on a regular file succeeded, want error")
	}
}

func TestWriteThenRead(t *testing.T) {
	v := newVault(t)

	n, err := v.NewNote(NewNoteRequest{
		Type:  typeSession,
		Title: "Round Trip",
		Tags:  []string{"test"},
		On:    mustDate(t, "2026-08-17"),
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if err := v.Write(n); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !v.Exists(n.Path) {
		t.Fatalf("Exists(%q) = false after write", n.Path)
	}

	got, err := v.Read(n.Path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got.Render()) != string(n.Render()) {
		t.Error("note read back differs from the note written")
	}

	// The parent directories the layout implies are created on demand.
	if _, err := os.Stat(filepath.Join(v.Root(), "Sessions", "2026", "08")); err != nil {
		t.Errorf("layout directories were not created: %v", err)
	}
}

func TestWriteRefusesNotesWithIssues(t *testing.T) {
	v := newVault(t)
	write(t, v, "broken.md", "---\ntype: session\ncreated: yesterday\nauthor: agent\n---\n\n# Broken\n")

	n, err := v.Read("broken.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(n.Issues) == 0 {
		t.Fatal("expected the planted note to have issues")
	}
	if err := v.Write(n); err == nil {
		t.Error("Write succeeded on a note with unresolved issues, want refusal")
	}

	// The file on disk is untouched, so nothing was lost.
	data, err := os.ReadFile(filepath.Join(v.Root(), "broken.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "created: yesterday") {
		t.Error("the original value was modified despite the refusal")
	}
}

func TestPathsMayNotEscapeTheVault(t *testing.T) {
	v := newVault(t)

	for _, rel := range []string{"../outside.md", "Sessions/../../outside.md", "/etc/passwd", ""} {
		t.Run(rel, func(t *testing.T) {
			if _, err := v.Read(rel); !errors.Is(err, ErrOutsideVault) {
				t.Errorf("Read(%q) err = %v, want ErrOutsideVault", rel, err)
			}
			if v.Exists(rel) {
				t.Errorf("Exists(%q) = true", rel)
			}
			err := v.Write(&Note{Path: rel})
			if !errors.Is(err, ErrOutsideVault) {
				t.Errorf("Write(%q) err = %v, want ErrOutsideVault", rel, err)
			}
		})
	}
}

func TestList(t *testing.T) {
	v := newVault(t)

	write(t, v, "Glossary.md", "---\n---\n")
	write(t, v, "Sessions/2026/08/a.md", "---\n---\n")
	write(t, v, "Standards/b.MD", "---\n---\n")
	write(t, v, "Standards/notes.txt", "not a note")
	write(t, v, ".obsidian/workspace.md", "---\n---\n")
	write(t, v, ".git/hooks/sample.md", "---\n---\n")

	got, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []string{"Glossary.md", "Sessions/2026/08/a.md", "Standards/b.MD"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("List() = %v, want %v", got, want)
	}
}

func TestWriteIsAtomic(t *testing.T) {
	v := newVault(t)

	n, err := v.NewNote(NewNoteRequest{Type: typeGlossary, Title: "Glossary", Tags: []string{"glossary"}})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if err := v.Write(n); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := v.Write(n); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	entries, err := os.ReadDir(v.Root())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temporary file %q was left behind", e.Name())
		}
	}
}
