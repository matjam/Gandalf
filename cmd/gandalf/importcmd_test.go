package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceVault writes a small vault to import from.
func sourceVault(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	notes := map[string]string{
		"Sessions/2026/05/2026-05-01-work.md": "---\ntype: session\ncreated: 2026-05-01\nupdated: 2026-05-01\n" +
			"tags: [work]\nauthor: user\n---\n\n# Work\n\nProse.\n",
		"Apps/Egg/Design.md": "---\ntype: design\ncreated: 2026-05-01\nupdated: 2026-05-01\n" +
			"tags: [egg]\nauthor: user\n---\n\n# Egg\n\nProse.\n",
	}

	for rel, body := range notes {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	return root
}

// rulesFile writes a rules file and returns its path.
func rulesFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	return path
}

// TestImportWritesNothingWithoutApply is the safeguard on a bulk operation: a
// migration you cannot inspect first is one nobody should run.
func TestImportWritesNothingWithoutApply(t *testing.T) {
	silence(t)

	src := sourceVault(t)
	dst := filepath.Join(t.TempDir(), "vault")
	if err := run([]string{"init", "-vault", dst}); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := run([]string{"import", "-from", src, "-vault", dst}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Sessions", "2026", "05")); err == nil {
		t.Error("a plan-only run wrote notes")
	}

	if err := run([]string{"import", "-from", src, "-vault", dst, "-apply"}); err != nil {
		t.Fatalf("import -apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Sessions", "2026", "05", "2026-05-01-work.md")); err != nil {
		t.Errorf("the note was not imported: %v", err)
	}
}

func TestImportRejects(t *testing.T) {
	silence(t)

	dst := filepath.Join(t.TempDir(), "vault")
	if err := run([]string{"init", "-vault", dst}); err != nil {
		t.Fatalf("init: %v", err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no source",
			args: []string{"import", "-vault", dst},
			want: "needs -from",
		},
		{
			name: "importing a vault into itself",
			args: []string{"import", "-from", dst, "-vault", dst},
			want: "same vault",
		},
		{
			name: "a rules file that is not there",
			args: []string{"import", "-from", sourceVault(t), "-vault", dst, "-rules", "/nonexistent.json"},
			want: "read import rules",
		},
		{
			name: "a rules file that is not JSON",
			args: []string{"import", "-from", sourceVault(t), "-vault", dst, "-rules", rulesFile(t, "{not json")},
			want: "parse import rules",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args)
			if err == nil {
				t.Fatalf("import succeeded, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestImportWithRules(t *testing.T) {
	silence(t)

	src := sourceVault(t)
	dst := filepath.Join(t.TempDir(), "vault")
	if err := run([]string{"init", "-vault", dst}); err != nil {
		t.Fatalf("init: %v", err)
	}

	rules := rulesFile(t, `{"rules":[
		{"match":"Apps/*/Design.md","category":"project","scope":"$1","facet":"design"},
		{"match":"Sessions/**","skip":true}
	]}`)

	if err := run([]string{"import", "-from", src, "-vault", dst, "-rules", rules, "-apply"}); err != nil {
		t.Fatalf("import: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "Projects", "Egg", "Design.md")); err != nil {
		t.Errorf("the mapped note is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Sessions", "2026")); err == nil {
		t.Error("a skipped note was imported")
	}

	// The vault must still satisfy its own linter.
	if err := run([]string{"lint", "-vault", dst}); err != nil {
		t.Errorf("the imported vault has errors: %v", err)
	}
}

func TestImportWithNothingToDo(t *testing.T) {
	silence(t)

	dst := filepath.Join(t.TempDir(), "vault")
	if err := run([]string{"init", "-vault", dst}); err != nil {
		t.Fatalf("init: %v", err)
	}

	empty := t.TempDir()
	if err := run([]string{"import", "-from", empty, "-vault", dst, "-apply"}); err == nil {
		t.Error("importing an empty vault reported success")
	}
}
