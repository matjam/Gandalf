package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo is a vault root that Gandalf maintains as a git repository.
type Repo struct {
	root string
	mu   sync.Mutex

	// disabled skips every operation. Set by serve -no-git for one process.
	disabled bool

	// hs is what is known about whether the repository works. See Health.
	hs healthState
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
//
// A failure here is recorded as the repository being broken, not merely
// returned. The caller decides what to do with the error, and the one that
// existed before this was recorded decided to carry on as though there were no
// repository at all; the record is what keeps the repository attached, and
// reported, whatever a caller does.
func (r *Repo) ensureLocked() (err error) {
	defer func() {
		if err != nil && !errors.Is(err, ErrBroken) {
			r.markBroken(err)
		}
	}()

	if err := ensureRootIgnore(r.root); err != nil {
		return err
	}

	existed := r.isRepoUnlocked()
	if !existed {
		if _, err := r.run("init"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}

	if err := r.pinConfig(); err != nil {
		// Git cannot write config into a repository it cannot recognise, so
		// a repository damaged badly enough (an unreadable HEAD) fails here
		// before it reaches the check below. That is damage to report, not a
		// config error to return.
		if existed {
			if cause := r.verify(); cause != nil {
				r.markBroken(cause)
				return brokenError(cause)
			}
		}
		return err
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		r.hs.mu.Lock()
		r.hs.h.State = StateDisabled
		r.hs.mu.Unlock()
		return nil
	}

	// A repository that was already there is looked at before anything is
	// committed to it. Ensure creates what is missing; it must never mistake
	// a repository git cannot read for one that is missing, and a commit on
	// top of a broken one only produces a second, less legible error.
	if existed {
		if err := r.verify(); err != nil {
			r.markBroken(err)
			return brokenError(err)
		}
	}

	if cfg.URL != "" {
		if err := r.setRemoteLocked(cfg.RemoteName(), cfg.URL); err != nil {
			return err
		}
	}

	return r.commitTrackedLocked("gandalf: initialise vault", "")
}

// pinConfig sets the repository-local settings that commits depend on.
func (r *Repo) pinConfig() error {
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

	// Durability is pinned for the same reason, and with more at stake. See
	// pinDurability.
	return r.pinDurability()
}

// Git added core.fsync, and with it the means to harden loose objects and
// references, in 2.36. Before that the only switch was core.fsyncObjectFiles.
const (
	fsyncMajor = 2
	fsyncMinor = 36
)

// fsyncComponents is what a vault's repository hardens: objects (loose and
// packed), derived metadata (pack indexes, bitmaps, the commit graph), and
// references. The index is left out: it is rebuilt from the tree and costs a
// sync on every add.
const fsyncComponents = "objects,derived-metadata,reference"

// supportsFsync reports whether a git of this version understands core.fsync.
func supportsFsync(major, minor int) bool {
	return major > fsyncMajor || (major == fsyncMajor && minor >= fsyncMinor)
}

// pinDurability makes git flush what it writes before it reports success.
//
// Git's default is to fsync packs and references but not loose objects, which
// is where every commit Gandalf makes lands. After a crash or a power cut the
// file can exist with its contents never written, and a ref that was flushed
// then names an object that is empty. That is exactly the state the incident
// vault was found in: a branch pointing at a commit of zero bytes, and six more
// empty objects beside it. A note write is followed by a commit within
// milliseconds, so the window is real.
//
// The cost is a few fsyncs per commit, on a repository whose commits are tiny
// and infrequent next to the cost of losing one.
//
// A git too old for core.fsync would ignore it silently, so the version is
// checked, and the nearest older switch used with a warning rather than a
// failure: running with weaker guarantees is better than refusing to serve
// notes, but not better than saying so.
func (r *Repo) pinDurability() error {
	if major, minor, ok := r.gitVersion(); ok && !supportsFsync(major, minor) {
		slog.Warn("gandalf: this git is too old to fsync references and derived metadata; "+
			"loose objects are hardened, but a crash may still lose a ref update",
			"have", fmt.Sprintf("%d.%d", major, minor), "want", fmt.Sprintf("%d.%d", fsyncMajor, fsyncMinor))

		_, err := r.run("config", "core.fsyncObjectFiles", "true")
		return err
	}

	if _, err := r.run("config", "core.fsync", fsyncComponents); err != nil {
		return err
	}
	_, err := r.run("config", "core.fsyncMethod", "fsync")
	return err
}

// gitVersion reports the installed git's major and minor version, and whether
// it could be read. A version that cannot be parsed, such as a distribution's
// suffix in an unexpected place, is treated as new enough rather than as a
// reason to refuse to start.
func (r *Repo) gitVersion() (major, minor int, ok bool) {
	out, err := r.run("version")
	if err != nil {
		return 0, 0, false
	}
	return parseVersion(out)
}

// parseVersion reads the major and minor numbers out of `git version`.
func parseVersion(out string) (major, minor int, ok bool) {
	// "git version 2.55.0", "git version 2.39.3 (Apple Git-146)", or
	// "git version 2.45.0.windows.1".
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return 0, 0, false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	return major, minor, err1 == nil && err2 == nil
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
	return r.execute(context.Background(), nil, args...)
}

// runNetwork executes a git command that talks to the remote, and returns
// what it wrote to stdout, trimmed.
//
// It differs from run in two ways, both following from the same fact: a
// command holding a network connection can stall for as long as the far end
// or an interactive prompt likes. So the command is given a deadline and
// killed when it passes, and git is told never to prompt on the terminal —
// there is no terminal behind a server, and a prompt would wait forever for
// an answer that cannot come.
func (r *Repo) runNetwork(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()

	out, err := r.execute(ctx, []string{"GIT_TERMINAL_PROMPT=0"}, args...)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("gave up after %s: %w", networkTimeout, context.DeadlineExceeded)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// execute runs git in the vault root with extra environment, returning stdout
// untouched and folding stderr into the error. The command is killed when ctx
// ends.
func (r *Repo) execute(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.root

	// Git finds its repository by looking in the working directory and then
	// in each parent. A .git directory it cannot make sense of — a HEAD that
	// was emptied, say — is skipped over, and the search carries on upward to
	// whatever repository the vault happens to sit inside. Every command here
	// would then read, and `config` would write, somebody else's repository,
	// and report it healthy. The vault's own parent is the ceiling, so a
	// broken repository is found broken rather than replaced by its neighbour.
	cmd.Env = os.Environ()
	if ceiling := r.ceiling(); ceiling != "" {
		cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+ceiling)
	}
	cmd.Env = append(cmd.Env, env...)

	// What it takes to stop git at the deadline differs by platform; see kill.
	cmd.Cancel = func() error { return kill(cmd) }

	// Killing git does not kill the ssh it may have spawned, and an orphan
	// still holding the output pipes would keep Wait blocked past the
	// deadline it was meant to enforce.
	cmd.WaitDelay = 5 * time.Second

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

// ceiling is the directory git must not search above: the vault's parent, or
// "" when it cannot be worked out.
//
// Git compares the ceiling, as text, with the directory it is really in, so a
// vault reached through a symlink (or through a temporary directory that is
// one, as on macOS, or a short 8.3 name on Windows) needs the resolved path or
// the ceiling never matches and protects nothing.
func (r *Repo) ceiling() string {
	abs, err := filepath.Abs(r.root)
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Dir(abs)
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
