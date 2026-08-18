package git

import (
	"fmt"
	"strings"
)

// Commit stages every change in the vault and creates a commit when there is
// something to record. An empty working tree is not an error.
func (r *Repo) Commit(message string) error {
	if r == nil || r.disabled {
		return nil
	}
	if !r.IsRepo() {
		return nil
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.commitLocked(message)
}

// commitLocked assumes r.mu is held.
func (r *Repo) commitLocked(message string) error {
	if _, err := r.run("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	status, err := r.run("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if status == "" {
		return nil
	}

	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "gandalf: update vault"
	}

	if _, err := r.run("commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}
