package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// networkTimeout bounds each git command that talks to the remote.
//
// A fetch or a push can stall for as long as the far end likes: a link that
// drops packets rather than refusing them, an SSH agent waiting for someone
// to approve a key, a proxy that accepts and then says nothing. Git itself
// waits indefinitely. Two minutes is long enough to push a vault's history to
// a slow host and short enough that a sync loop wedged on one call is back
// before its next interval.
//
// A variable so tests can stall a remote without waiting two minutes on it.
var networkTimeout = 2 * time.Minute

// Unguarded is the guard to pass to Sync when nothing else can be changing
// the working tree, or when the caller already holds the lock that protects
// it and would deadlock taking it again.
var Unguarded sync.Locker = nopLocker{}

type nopLocker struct{}

func (nopLocker) Lock()   {}
func (nopLocker) Unlock() {}

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
		_ = r.commitTrackedLocked("gandalf: clear git remote", "")
		return cfg, nil
	}

	if err := r.setRemoteLocked(cfg.RemoteName(), url); err != nil {
		return Config{}, err
	}

	_ = r.commitTrackedLocked("gandalf: configure git remote", "")
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

// Sync brings the vault and its remote into line: it commits anything still
// dirty, fetches, merges the remote's branch with remote-wins conflict
// resolution, and pushes. No remote configured is a no-op.
//
// The phases that change the working tree or the index — the checkpoint
// commit and the merge — run under guard and under the repository mutex. The
// phases that talk to the network — fetch and push — run under neither, and
// are bounded by networkTimeout.
//
// That split is the point. Every note write commits through the same mutex,
// and a sync that held it across the network held every writing tool behind
// a stalled push: an append to a session note waited minutes, unbounded, for
// a remote that had nothing to do with the note. The guard is the server's
// write lock, so that a checkpoint cannot sweep a half-written note into a
// commit under the wrong subject, and a merge cannot rewrite a note a tool is
// in the middle of changing.
//
// Every call first re-examines the repository itself (see Check), remote or
// no remote. That is what makes a periodic sync the thing that notices a
// repository has broken between writes, and the thing that notices it has
// been repaired. A repository that is broken is not synced: there is nothing
// trustworthy to merge into, and the error says why. Whether the attempt
// succeeds is also recorded for Health, so a remote that has been failing for
// a day is something the vault can say rather than something only a log knows.
func (r *Repo) Sync(ctx context.Context, guard sync.Locker) error {
	if r == nil || r.disabled {
		return nil
	}
	if !r.isRepoUnlocked() && !r.wasRepo() {
		return nil
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		return nil
	}

	if err := r.Check(guard); err != nil {
		return err
	}
	if cfg.URL == "" {
		return nil
	}

	err = r.syncRemote(ctx, guard, cfg)

	// Cancellation is shutdown, which says nothing about the remote.
	if !errors.Is(err, context.Canceled) {
		r.noteSync(err)
	}
	return err
}

// syncRemote is the part of Sync that talks to the remote.
func (r *Repo) syncRemote(ctx context.Context, guard sync.Locker, cfg Config) error {
	remote := cfg.RemoteName()

	if err := r.checkpoint(guard, remote, cfg.URL); err != nil {
		return err
	}

	if _, err := r.runNetwork(ctx, "fetch", remote); err != nil {
		return fmt.Errorf("git fetch: %w", err)
	}

	branch, upstream, err := r.merge(guard, remote)
	if err != nil {
		return err
	}

	args := []string{"push", remote, branch}
	if !upstream {
		// First push: the remote has no such branch yet, so nothing was
		// merged, and the push sets the upstream.
		args = []string{"push", "-u", remote, branch}
	}
	if _, err := r.runNetwork(ctx, args...); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

// checkpoint makes the remote what the config says and commits anything
// still dirty, so the merge that follows does not refuse a dirty tree.
func (r *Repo) checkpoint(guard sync.Locker, remote, url string) error {
	guard.Lock()
	defer guard.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.setRemoteLocked(remote, url); err != nil {
		return fmt.Errorf("set remote: %w", err)
	}
	return r.commitTrackedLocked("gandalf: sync checkpoint", "")
}

// merge folds the fetched remote branch into the working tree, preferring the
// remote's version of any conflicting file. It reports the branch and whether
// the remote already had it: a remote without the branch has nothing to merge.
func (r *Repo) merge(guard sync.Locker, remote string) (branch string, upstream bool, err error) {
	guard.Lock()
	defer guard.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	branch, err = r.currentBranch()
	if err != nil {
		return "", false, err
	}

	remoteRef := remote + "/" + branch
	if !r.refExists(remoteRef) {
		return branch, false, nil
	}

	if _, err := r.run("merge", "-X", "theirs", "--no-edit", remoteRef); err != nil {
		return "", false, fmt.Errorf("git merge (remote-wins): %w", err)
	}
	return branch, true, nil
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

// StartSync runs Sync on an interval until ctx is cancelled, passing guard
// through to each run. Failures are logged; they never stop the loop.
//
// Every failed attempt is logged, at error level, with how long things have
// been wrong. A loop that logged the first failure and then went quiet is how
// a sync that stopped working stayed unnoticed for a month, and a line per
// interval is a small price for a log that still says so on day thirty.
//
// The returned channel closes once the loop has stopped and any sync it had
// under way has returned, so a caller that is about to remove the repository
// can wait for git to let go of it. It is already closed when there is
// nothing to run.
func (r *Repo) StartSync(ctx context.Context, guard sync.Locker) <-chan struct{} {
	stopped := make(chan struct{})
	if r == nil || r.disabled {
		close(stopped)
		return stopped
	}

	go func() {
		defer close(stopped)
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

			// Cancellation is shutdown, not failure: a fetch cut short
			// because the server is stopping is not worth an error line.
			if err := r.Sync(ctx, guard); err != nil && !errors.Is(err, context.Canceled) {
				health := r.Health()
				slog.ErrorContext(ctx, "gandalf: git sync",
					"error", err,
					"git", health.State.String(),
					"broken_since", health.Since,
					"sync_failures", health.SyncFailures)
			}
		}
	}()
	return stopped
}
