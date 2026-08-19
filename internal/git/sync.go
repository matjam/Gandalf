package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// SetRemote records a remote URL in config and in the repository, creating the
// remote if needed. An empty URL clears the configured remote URL but leaves
// the local repo and its history alone.
func (r *Repo) SetRemote(url string) (Config, error) {
	if r == nil {
		return Config{}, fmt.Errorf("no vault repository")
	}
	if r.disabled {
		return Config{}, fmt.Errorf("git is disabled for this process")
	}

	url = normalizeURL(url)

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRepoUnlocked() {
		if err := r.ensureLocked(); err != nil {
			return Config{}, err
		}
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return Config{}, err
	}
	cfg.URL = url
	if cfg.Remote == "" {
		cfg.Remote = DefaultRemote
	}
	if cfg.Conflict == "" {
		cfg.Conflict = "remote-wins"
	}
	if cfg.SyncInterval == "" {
		cfg.SyncInterval = DefaultSyncInterval.String()
	}
	enabled := true
	cfg.Enabled = &enabled

	if err := SaveConfig(r.root, cfg); err != nil {
		return Config{}, err
	}

	if url == "" {
		// Drop the remote from git if it exists; ignore failure when absent.
		_, _ = r.run("remote", "remove", cfg.RemoteName())
		_ = r.commitLocked("gandalf: clear git remote", "")
		return cfg, nil
	}

	if err := r.setRemoteLocked(cfg.RemoteName(), url); err != nil {
		return Config{}, err
	}

	_ = r.commitLocked("gandalf: configure git remote", "")
	return cfg, nil
}

// setRemoteLocked adds or updates a remote. Caller holds the mutex.
func (r *Repo) setRemoteLocked(name, url string) error {
	existing, err := r.run("remote")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == name {
			_, err := r.run("remote", "set-url", name, url)
			return err
		}
	}
	_, err = r.run("remote", "add", name, url)
	return err
}

// Sync pulls from the remote with remote-wins conflict resolution, then pushes
// local commits. No remote configured is a no-op.
func (r *Repo) Sync() error {
	if r == nil || r.disabled || !r.IsRepo() {
		return nil
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() || cfg.URL == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.syncLocked(cfg)
}

// syncLocked assumes r.mu is held.
func (r *Repo) syncLocked(cfg Config) error {
	remote := cfg.RemoteName()

	if err := r.setRemoteLocked(remote, cfg.URL); err != nil {
		return fmt.Errorf("set remote: %w", err)
	}

	// Commit anything still dirty so pull does not refuse a dirty tree.
	if err := r.commitLocked("gandalf: sync checkpoint", ""); err != nil {
		return err
	}

	if _, err := r.run("fetch", remote); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	branch, err := r.currentBranch()
	if err != nil {
		return err
	}

	remoteRef := remote + "/" + branch
	if !r.refExists(remoteRef) {
		// First push: no upstream yet. Push and set upstream.
		if _, err := r.run("push", "-u", remote, branch); err != nil {
			return fmt.Errorf("git push: %w", err)
		}
		return nil
	}

	// remote-wins: on conflict, prefer the remote's version of the file.
	if _, err := r.run("merge", "-X", "theirs", "--no-edit", remoteRef); err != nil {
		return fmt.Errorf("git merge (remote-wins): %w", err)
	}

	if _, err := r.run("push", remote, branch); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

// currentBranch returns the checked-out branch name.
func (r *Repo) currentBranch() (string, error) {
	out, err := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" || out == "" {
		return "main", nil
	}
	return out, nil
}

// refExists reports whether a ref resolves.
func (r *Repo) refExists(ref string) bool {
	_, err := r.run("rev-parse", "--verify", ref)
	return err == nil
}

// StartSync runs Sync on an interval until ctx is cancelled. Failures are
// written to stderr; they never stop the loop.
func (r *Repo) StartSync(ctx context.Context) {
	if r == nil || r.disabled {
		return
	}

	go func() {
		for {
			cfg, err := LoadConfig(r.root)
			interval := DefaultSyncInterval
			if err == nil {
				interval = cfg.Interval()
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}

			if err := r.Sync(); err != nil {
				fmt.Fprintf(os.Stderr, "gandalf: git sync: %v\n", err)
			}
		}
	}()
}
