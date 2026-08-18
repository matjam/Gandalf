package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// silence redirects stdout for the duration of a test. The commands print
// their results, and a test run should not.
func silence(t *testing.T) {
	t.Helper()

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	saved := os.Stdout
	os.Stdout = devnull

	t.Cleanup(func() {
		os.Stdout = saved
		devnull.Close()
	})
}

func TestRunDispatch(t *testing.T) {
	silence(t)

	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "no command", args: nil, wantErr: true},
		{name: "unknown command", args: []string{"summon"}, wantErr: true},
		{name: "help", args: []string{"help"}},
		{name: "help flag", args: []string{"--help"}},
		{name: "bad flag", args: []string{"lint", "-nonsense"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args)
			if tc.wantErr != (err != nil) {
				t.Errorf("run(%v) err = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
		})
	}
}

// TestInitDoctorLint walks the sequence a new user actually performs.
func TestInitDoctorLint(t *testing.T) {
	silence(t)

	root := filepath.Join(t.TempDir(), "vault")

	if err := run([]string{"init", "-vault", root}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Gandalf", "Operating.md")); err != nil {
		t.Fatalf("init did not seed the contract: %v", err)
	}

	if err := run([]string{"doctor", "-vault", root}); err != nil {
		t.Errorf("doctor: %v", err)
	}

	// A freshly seeded vault must be clean even with warnings promoted.
	if err := run([]string{"lint", "-vault", root, "-strict"}); err != nil {
		t.Errorf("lint -strict: %v", err)
	}
}

func TestLintReportsErrorsAsExitStatus(t *testing.T) {
	silence(t)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.md"), []byte("# No frontmatter\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := run([]string{"lint", "-vault", root})
	if err == nil {
		t.Fatal("lint returned nil for a vault with errors")
	}
	if !strings.Contains(err.Error(), "error") {
		t.Errorf("err = %v, want it to mention errors", err)
	}
}

func TestLintStrictPromotesWarnings(t *testing.T) {
	silence(t)

	root := t.TempDir()
	// Valid frontmatter, but no tags and no heading: warnings only.
	const note = "---\ntype: glossary\ncreated: 2026-08-17\nupdated: 2026-08-17\nauthor: agent\n---\n\nProse.\n"
	if err := os.WriteFile(filepath.Join(root, "warn.md"), []byte(note), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := run([]string{"lint", "-vault", root}); err != nil {
		t.Errorf("warnings alone should not fail: %v", err)
	}
	if err := run([]string{"lint", "-vault", root, "-strict"}); err == nil {
		t.Error("-strict did not fail on warnings")
	}
}
