package git

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrBroken is the reason a vault's repository cannot be used although it is
// there. It is a different error from ErrNoGit because the remedies differ:
// no repository is created by running init, and a broken one is repaired by
// somebody looking at it. Treating the second as the first is how a vault
// went a month without a commit, with every tool reporting that it simply was
// not a repository.
//
// Wrapped around the cause, so errors.Is finds it and the message reads
// "the vault's git repository is broken: <what git said>".
var ErrBroken = errors.New("the vault's git repository is broken")

// State is whether the repository can currently be committed to.
type State int

const (
	// StateHealthy means HEAD resolves to a commit whose tree can be read,
	// or the repository has no commits yet.
	StateHealthy State = iota

	// StateBroken means the repository exists and git cannot use it: a
	// commit, tree or object is missing or empty, HEAD is unreadable, or the
	// last commit failed. Nothing is being committed or synced.
	StateBroken

	// StateDisabled means git is off by choice: serve -no-git for this
	// process, or "enabled": false in the vault's git config.
	StateDisabled

	// StateAbsent means there is no repository at the vault root. It is only
	// ever reported by Inspect: a served vault is given one by Ensure.
	StateAbsent
)

// String names a state for logs and reports.
func (s State) String() string {
	switch s {
	case StateHealthy:
		return "healthy"
	case StateBroken:
		return "broken"
	case StateDisabled:
		return "disabled"
	case StateAbsent:
		return "absent"
	}
	return "unknown"
}

// Health is what is known about the repository right now.
//
// It is a snapshot of recorded fact, not a probe: Repo.Health never runs git.
// The probes run where they are cheap and useful (before a commit, on a sync
// tick, when a history tool is asked a question) and record what they find
// here, so asking for health costs nothing.
type Health struct {
	State State

	// Err is why the repository is broken, as git said it. Nil unless State is
	// StateBroken.
	Err error

	// Since is when the repository was first seen broken, and stays put while
	// it remains broken, so "since" means since it stopped working rather than
	// since the last attempt failed.
	Since time.Time

	// SyncErr is why the last sync with the remote failed, or nil. It is
	// tracked apart from State on purpose: a remote that is unreachable
	// leaves commits working, and reporting "changes are not being committed"
	// for a laptop that is merely offline would be false.
	SyncErr error

	// SyncSince is when sync started failing, and SyncFailures how many
	// attempts have failed in a row since.
	SyncSince    time.Time
	SyncFailures int
}

// Broken reports whether the repository itself is unusable.
func (h Health) Broken() bool { return h.State == StateBroken }

// Problem returns a sentence for each thing that is wrong, or nil. It is what
// boot and the write tools put in front of the model, so it says what failed,
// since when, and what that means for the notes being written.
func (h Health) Problem() []string {
	var out []string

	if h.State == StateBroken {
		out = append(out, fmt.Sprintf(
			"the vault's git repository is broken since %s: %v. Changes to the vault are NOT being committed or "+
				"synced, so nothing written now has history or a backup until it is repaired. Notes are still "+
				"being saved to disk. Tell the user; do not carry on as if nothing were wrong",
			h.Since.Format(time.RFC3339), h.Err))
	}

	if h.SyncErr != nil {
		out = append(out, fmt.Sprintf(
			"syncing with the git remote has failed %d time(s) in a row since %s: %v. Commits continue locally "+
				"but are not reaching the remote",
			h.SyncFailures, h.SyncSince.Format(time.RFC3339), h.SyncErr))
	}

	return out
}

// brokenError wraps a cause in ErrBroken.
func brokenError(cause error) error {
	return fmt.Errorf("%w: %w", ErrBroken, cause)
}

// errNoGitDir is the cause recorded when a repository this process has seen
// vanishes from under it.
var errNoGitDir = errors.New("the .git directory is missing")

// healthState is the mutable record behind Health.
//
// It has its own lock, apart from the one serialising git commands, so that
// asking for health never waits behind a commit or a checkpoint, and so the
// two can never be taken in an order that deadlocks: this one is held only to
// read or write the fields.
type healthState struct {
	mu sync.Mutex
	h  Health

	// seen is set once the vault has been observed as a repository. A vault
	// that was never one is a silent no-op for Commit, as it always was; one
	// that was and now is not has lost its .git directory, which is damage.
	seen bool
}

// Health returns what is currently known about the repository.
func (r *Repo) Health() Health {
	switch {
	case r == nil:
		return Health{State: StateDisabled}
	case r.disabled:
		return Health{State: StateDisabled}
	}

	r.hs.mu.Lock()
	defer r.hs.mu.Unlock()
	return r.hs.h
}

// wasRepo reports whether the vault has been seen as a repository.
func (r *Repo) wasRepo() bool {
	r.hs.mu.Lock()
	defer r.hs.mu.Unlock()
	return r.hs.seen
}

// markBroken records a failure. The first one starts the clock and is logged;
// later ones update the cause without moving it, so repeated failures do not
// reset "since" and do not bury the log in a line per attempt (the callers
// that retry log each attempt themselves).
func (r *Repo) markBroken(cause error) {
	r.hs.mu.Lock()
	defer r.hs.mu.Unlock()

	r.hs.seen = true
	first := r.hs.h.State != StateBroken
	if first {
		r.hs.h.Since = time.Now()
	}
	r.hs.h.State = StateBroken
	r.hs.h.Err = cause

	if first {
		slog.Error("gandalf: git repository is broken; changes are not being committed or synced",
			"vault", r.root, "error", cause)
	}
}

// markHealthy records that the repository works, and logs the recovery when it
// had not been.
func (r *Repo) markHealthy() {
	r.hs.mu.Lock()
	defer r.hs.mu.Unlock()

	r.hs.seen = true
	if r.hs.h.State == StateBroken {
		slog.Info("gandalf: git repository recovered; committing and syncing again",
			"vault", r.root, "was_broken_for", time.Since(r.hs.h.Since).Round(time.Second).String())
	}
	r.hs.h.State = StateHealthy
	r.hs.h.Err = nil
	r.hs.h.Since = time.Time{}
}

// noteSync records the outcome of a sync attempt against a remote.
func (r *Repo) noteSync(err error) {
	r.hs.mu.Lock()
	defer r.hs.mu.Unlock()

	if err == nil {
		if r.hs.h.SyncErr != nil {
			slog.Info("gandalf: git sync recovered", "vault", r.root, "failed_attempts", r.hs.h.SyncFailures)
		}
		r.hs.h.SyncErr, r.hs.h.SyncSince, r.hs.h.SyncFailures = nil, time.Time{}, 0
		return
	}

	if r.hs.h.SyncErr == nil {
		r.hs.h.SyncSince = time.Now()
	}
	r.hs.h.SyncErr = err
	r.hs.h.SyncFailures++
}

// probe checks that the repository can be committed to, cheaply: one git
// command in the healthy case.
//
// `cat-file -t HEAD^{tree}` resolves HEAD to a commit, reads that commit to
// find its tree, and reads the tree's header. An empty or missing commit, an
// empty or missing tree, and an unreadable HEAD all fail here, which are the
// shapes a crash left the incident vault in. `cat-file -e` would not do: it
// asks only whether an object exists, and a file of zero bytes exists.
//
// It cannot see damage deeper in history, or in objects HEAD does not reach.
// verify adds the scan for empty objects; a full fsck is far too slow for a
// path that runs on every write.
func (r *Repo) probe() error {
	_, err := r.run("cat-file", "-t", "HEAD^{tree}")
	if err == nil {
		return nil
	}

	// The same command fails on a repository with no commits, which is not
	// broken, just new.
	if r.unborn() {
		return nil
	}
	return tidyGitError(err)
}

// unborn reports whether HEAD names a branch that does not exist yet.
//
// That is told apart from a branch whose ref exists but whose object does not
// by asking git about the ref itself: show-ref exits 1 for an absent ref and
// otherwise reports what it found. Anything else — a detached HEAD, a HEAD
// git cannot parse — is not a repository without commits.
func (r *Repo) unborn() bool {
	ref, err := r.run("symbolic-ref", "-q", "HEAD")
	if err != nil || ref == "" {
		return false
	}

	_, err = r.run("show-ref", "--verify", "-q", ref)
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

// verify is probe plus the scan for empty loose objects: what is run when
// there is time for it, at startup, on a sync tick, and when deciding whether
// a broken repository has been repaired.
//
// The scan matters beyond HEAD. Of the seven empty objects in the incident
// only one was the commit HEAD named; the rest sat unreachable, waiting. Git
// treats an object file that exists as an object it already has, so adding
// content identical to an empty one writes nothing and the new commit points
// at nothing readable.
func (r *Repo) verify() error {
	if !r.isRepoUnlocked() {
		return errNoGitDir
	}

	probeErr := r.probe()

	empty, scanErr := r.EmptyObjects()
	switch {
	case len(empty) > 0 && probeErr != nil:
		return fmt.Errorf("%w; also %s", probeErr, describeEmpty(empty))
	case len(empty) > 0:
		return errors.New(describeEmpty(empty))
	case probeErr != nil:
		return probeErr
	case scanErr != nil:
		return fmt.Errorf("scan loose objects: %w", scanErr)
	}
	return nil
}

// describeEmpty names empty objects for an error, capped so a vault with
// thousands of them does not produce a message nobody reads to the end.
func describeEmpty(ids []string) string {
	const shown = 8
	list := ids
	if len(list) > shown {
		list = list[:shown]
	}

	msg := fmt.Sprintf("%d zero-byte loose object(s) in .git/objects: %s", len(ids), strings.Join(list, ", "))
	if len(ids) > shown {
		msg += fmt.Sprintf(", and %d more", len(ids)-shown)
	}
	return msg
}

// EmptyObjects lists the loose objects under .git/objects whose files hold no
// bytes, by object id, sorted.
//
// A zero-byte object is what a crash leaves when the file was created but its
// content never reached the disk. Git does not notice until something reads
// it, and by then it is the commit HEAD points at. The listing is a directory
// walk and a size check per file, so it scales with the number of loose
// objects; it is run on sync ticks and by doctor, not on every write.
func (r *Repo) EmptyObjects() ([]string, error) {
	return emptyObjectsIn(filepath.Join(r.root, ".git", "objects"), os.ReadDir)
}

// emptyObjectsIn is EmptyObjects over an objects directory, with the directory
// reader injected so a test can make a fanout directory vanish mid-walk.
func emptyObjectsIn(objects string, readDir func(string) ([]os.DirEntry, error)) ([]string, error) {
	fanout, err := readDir(objects)
	if err != nil {
		return nil, err
	}

	var empty []string
	for _, dir := range fanout {
		// Loose objects live in two-hex-digit directories; pack, info, and
		// anything else in here is not one.
		if !dir.IsDir() || len(dir.Name()) != 2 || !isHex(dir.Name()) {
			continue
		}

		files, err := readDir(filepath.Join(objects, dir.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			// Git removes a fanout directory once its objects are packed,
			// which can happen between listing the objects and reading this.
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if file.IsDir() || !isHex(file.Name()) {
				continue
			}
			info, err := file.Info()
			switch {
			case errors.Is(err, fs.ErrNotExist):
				// Packed or pruned while the walk was running.
				continue
			case err != nil:
				return nil, err
			case info.Size() == 0:
				empty = append(empty, dir.Name()+file.Name())
			}
		}
	}

	sort.Strings(empty)
	return empty, nil
}

// isHex reports whether s is non-empty and made of hex digits, which is what
// distinguishes an object file from a temporary file or a stray.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// tidyGitError reduces git's stderr to something worth showing: one line per
// distinct complaint. Git repeats "object file ... is empty" once per read, and
// an error that says the same thing four times buries the one line that
// differs.
func tidyGitError(err error) error {
	// "exit status 128: " is the wrapper's, ahead of the first line of git's
	// own, and would stop that line matching its repeats.
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "exit status "); ok {
		if _, after, found := strings.Cut(rest, ": "); found {
			msg = after
		}
	}
	lines := strings.Split(msg, "\n")

	seen := make(map[string]bool, len(lines))
	kept := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		kept = append(kept, line)
	}
	return errors.New(strings.Join(kept, "; "))
}

// Inspect examines the repository from scratch and reports what it finds,
// recording nothing. It is for a command that is not serving the vault, such
// as doctor: there is no running process whose state it could be keeping.
func (r *Repo) Inspect() Health {
	switch {
	case r == nil || r.disabled:
		return Health{State: StateDisabled}
	case !r.isRepoUnlocked():
		return Health{State: StateAbsent}
	}

	if cfg, err := LoadConfig(r.root); err == nil && !cfg.IsEnabled() {
		return Health{State: StateDisabled}
	}

	if err := r.verify(); err != nil {
		return Health{State: StateBroken, Err: err}
	}
	return Health{State: StateHealthy}
}

// Check re-examines the repository, thoroughly, and acts on what it finds:
// a repository that has stopped working is recorded as broken, and one that
// had been broken and reads again gets a checkpoint commit of everything that
// piled up in the working tree meanwhile, and is recorded as healthy.
//
// guard is the lock that protects the working tree from half-written notes,
// taken only for that checkpoint. Check is what the sync loop runs on every
// tick and what boot runs, so a repair made while the server was running is
// noticed without a restart.
func (r *Repo) Check(guard sync.Locker) error {
	if r == nil || r.disabled {
		return nil
	}
	if !r.isRepoUnlocked() && !r.wasRepo() {
		return nil
	}
	if cfg, err := LoadConfig(r.root); err == nil && !cfg.IsEnabled() {
		return nil
	}

	if err := r.verify(); err != nil {
		r.markBroken(err)
		return brokenError(err)
	}
	if !r.Health().Broken() {
		r.markHealthy()
		return nil
	}

	guard.Lock()
	defer guard.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.commitTrackedLocked("", "")
}

// recoveryCommit words the commit that follows a repair. It is made once, on
// the first commit after the repository reads again, and carries the change
// that triggered it when there is one, because that change is already in the
// working tree and cannot be committed apart from the rest.
func recoveryCommit(h Health, message, reason string) (string, string) {
	why := fmt.Sprintf("git was broken since %s (%v); this records everything written to the vault in the meantime",
		h.Since.Format(time.RFC3339), h.Err)
	if message = strings.TrimSpace(message); message != "" {
		why = fmt.Sprintf("%s (%s) is included. %s", message, OneLine(reason), why)
	}
	return "gandalf: checkpoint after git recovered", why
}
