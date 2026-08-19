package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func TestEnsureCreatesRepoAndCommits(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Vault\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !repo.IsRepo() {
		t.Fatal("expected a git repository")
	}
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatalf("root .gitignore: %v", err)
	}

	out, err := repo.run("log", "--oneline")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out, "initialise vault") {
		t.Errorf("log = %q, want an initialise commit", out)
	}
}

func TestCommitRecordsChanges(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit("gandalf: note new note", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	out, err := repo.run("log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if out != "gandalf: note new note" {
		t.Errorf("message = %q", out)
	}
}

func TestCommitIsNoopWhenClean(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := repo.Commit("nothing", ""); err != nil {
		t.Fatalf("Commit on clean tree: %v", err)
	}
}

func TestSetRemotePersistsConfig(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	cfg, err := repo.SetRemote("https://example.com/vault.git")
	if err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	if cfg.URL != "https://example.com/vault.git" {
		t.Errorf("url = %q", cfg.URL)
	}

	loaded, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.URL != cfg.URL {
		t.Errorf("persisted url = %q", loaded.URL)
	}

	remotes, err := repo.run("remote", "-v")
	if err != nil {
		t.Fatalf("remote -v: %v", err)
	}
	if !strings.Contains(remotes, "https://example.com/vault.git") {
		t.Errorf("remotes = %q", remotes)
	}
}

func TestDisabledSkipsCommit(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	repo := Open(root)
	repo.Disable()
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if repo.IsRepo() {
		t.Fatal("disabled Ensure should not create a repo")
	}
}
