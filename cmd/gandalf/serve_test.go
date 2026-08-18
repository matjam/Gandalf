package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareRequiresAVault(t *testing.T) {
	if _, _, err := prepare("", true, true); err == nil {
		t.Error("prepare succeeded without a vault path; the server should not guess")
	}
}

func TestPrepareSeeds(t *testing.T) {
	silence(t)

	root := filepath.Join(t.TempDir(), "vault")

	v, repo, err := prepare(root, true, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !v.Exists("Gandalf/Operating.md") {
		t.Error("prepare did not seed the contract")
	}
	if repo == nil || !repo.IsRepo() {
		t.Error("prepare did not create a git repository")
	}
}

func TestPrepareCanSkipSeeding(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")

	v, _, err := prepare(root, false, false)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if v.Exists("Gandalf/Operating.md") {
		t.Error("prepare seeded despite being told not to")
	}

	// The directory is still created, so a later init has somewhere to write.
	if _, err := os.Stat(v.Root()); err != nil {
		t.Errorf("vault root was not created: %v", err)
	}
}

func TestServeRejectsBadFlags(t *testing.T) {
	silence(t)

	if err := run([]string{"serve"}); err == nil {
		t.Error("serve without -vault succeeded")
	}
	if err := run([]string{"serve", "-nonsense"}); err == nil {
		t.Error("serve with an unknown flag succeeded")
	}
}
