package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Repo is a vault root that Gandalf maintains as a git repository.
type Repo struct {
	root string
	mu   sync.Mutex

	// disabled skips every operation. Set by serve -no-git for one process.
	disabled bool
}

// Open returns a Repo for a vault root. It does not create the repository;
// call Ensure for that.
func Open(root string) *Repo {
	return &Repo{root: root}
}

// Disable turns off automatic git for this process without rewriting config.
func (r *Repo) Disable() {
	if r == nil {
		return
	}
	r.disabled = true
}

// Root returns the vault root.
func (r *Repo) Root() string {
	if r == nil {
		return ""
	}
	return r.root
}

// Enabled reports whether this repo should commit and sync.
func (r *Repo) Enabled() bool {
	if r == nil || r.disabled {
		return false
	}
	cfg, err := LoadConfig(r.root)
	if err != nil {
		return false
	}
	return cfg.IsEnabled() && r.IsRepo()
}

// IsRepo reports whether root is already a git working tree.
func (r *Repo) IsRepo() bool {
	if r == nil {
		return false
	}
	info, err := os.Stat(filepath.Join(r.root, ".git"))
	return err == nil && info.IsDir()
}

// Ensure creates a git repository if one is missing, writes the root ignore
// file, configures a local identity for commits, and makes an initial commit
// when the tree has content and no commits yet.
func (r *Repo) Ensure() error {
	if r == nil || r.disabled {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.ensureLocked()
}

// ensureLocked is Ensure with the mutex already held.
func (r *Repo) ensureLocked() error {
	if err := ensureRootIgnore(r.root); err != nil {
		return err
	}

	if !r.isRepoUnlocked() {
		if _, err := r.run("init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	// Commits must not depend on the user's global git identity: this repo is
	// machinery, and a missing name would make every note write fail.
	if _, err := r.run("config", "user.name", "Gandalf"); err != nil {
		return err
	}
	if _, err := r.run("config", "user.email", "gandalf@localhost"); err != nil {
		return err
	}

	// Line endings are not the user's global setting to decide either. With
	// core.autocrlf on — the default from Git for Windows' installer — every
	// note is rewritten to CRLF on checkout, so the bytes a seed fingerprint
	// was taken over change, a note no longer round-trips through git
	// byte-identically, and git warns about the conversion on every command.
	if _, err := r.run("config", "core.autocrlf", "false"); err != nil {
		return err
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		return nil
	}

	if cfg.URL != "" {
		if err := r.setRemoteLocked(cfg.RemoteName(), cfg.URL); err != nil {
			return err
		}
	}

	return r.commitLocked("gandalf: initialise vault", "")
}

// isRepoUnlocked is IsRepo without taking the mutex.
func (r *Repo) isRepoUnlocked() bool {
	info, err := os.Stat(filepath.Join(r.root, ".git"))
	return err == nil && info.IsDir()
}

// runRaw executes git in the vault root and returns stdout untouched, keeping
// stderr separate. File content comes back through here rather than through
// run: a note's trailing newline is content, and trimming it would make every
// restored note differ from the one that was committed.
func (r *Repo) runRaw(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root

	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs

	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errs.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

// run executes git in the vault root and returns what it wrote to stdout,
// trimmed. stderr is kept out of the result and folded into the error instead.
//
// Every caller here parses what it gets back — a diff, a status, a branch
// name, a log — and git writes advice and warnings to stderr whenever it feels
// like it. Returning the two streams merged put a line-ending conversion
// warning inside a diff and made an unchanged note look modified.
func (r *Repo) run(args ...string) (string, error) {
	out, err := r.runRaw(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
