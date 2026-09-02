package server

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/matjam/gandalf/internal/git"
)

// The periodic sync changes the working tree — a checkpoint commit and a
// merge — and those phases have to wait on the same lock the tools take, or a
// sync can commit a half-written note under its own subject. This pins that
// StartSync passes the write lock through.

// gitIn runs git in a directory and returns trimmed stdout, or "" on failure.
func gitIn(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestStartSyncHoldsTheWriteLockWhileChangingTheTree(t *testing.T) {
	h := newGitHarness(t)

	bare := t.TempDir()
	if err := exec.Command("git", "init", "--bare", bare).Run(); err != nil {
		t.Fatalf("init --bare: %v", err)
	}

	cfg, err := h.server.git.SetRemote(bare)
	if err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	cfg.SyncInterval = "20ms"
	if err := git.SaveConfig(h.vault.Root(), cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	branch := gitIn(h.vault.Root(), "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" {
		t.Fatal("no current branch")
	}

	ctx, cancel := context.WithCancel(h.context)
	defer cancel()

	h.server.Core.write.Lock()
	h.server.StartSync(ctx)

	// Several intervals pass. Nothing reaches the remote, because the sync's
	// first phase is waiting on the lock this test holds.
	time.Sleep(200 * time.Millisecond)
	if got := gitIn(bare, "rev-parse", "--verify", "--quiet", branch); got != "" {
		t.Fatalf("remote received %s while the write lock was held", got)
	}

	h.server.Core.write.Unlock()

	// The sync's checkpoint commits the interval change above, so local HEAD
	// moves once more before anything is pushed; what has to hold is that
	// the remote ends up at wherever local HEAD then is.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got := gitIn(bare, "rev-parse", "--verify", "--quiet", branch)
		if got != "" && got == gitIn(h.vault.Root(), "rev-parse", "HEAD") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("remote %s = %q, local HEAD = %q: the sync never ran once the write lock was released",
		branch, gitIn(bare, "rev-parse", "--verify", "--quiet", branch), gitIn(h.vault.Root(), "rev-parse", "HEAD"))
}
