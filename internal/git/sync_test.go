package git

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A sync talks to the network, and the network can stall. These tests pin
// the two properties that keep a stalled remote from reaching a note write:
// a commit never waits on a fetch or a push, and neither waits forever.

// stalledRemote serves git's own protocol and never answers, which is what a
// remote behind a dead link or an unanswered prompt looks like to a fetch:
// connected, and waiting. It returns the URL and a channel that closes once
// a client has connected.
func stalledRemote(t *testing.T) (url string, connected <-chan struct{}) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})

	go func() {
		var once sync.Once
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			once.Do(func() { close(first) })
			go func() {
				<-done
				conn.Close()
			}()
		}
	}()

	return "git://" + ln.Addr().String() + "/vault.git", first
}

// bareRemote is a local repository a vault can push to and fetch from with
// no network at all.
func bareRemote(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if _, err := Open(dir).run("init", "--bare"); err != nil {
		t.Fatalf("init --bare: %v", err)
	}
	return dir
}

// repoWithRemote is a vault repository with one committed note and the given
// remote configured.
func repoWithRemote(t *testing.T, url string) *Repo {
	t.Helper()
	requireGit(t)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("# Note\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := repo.SetRemote(url); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	return repo
}

// stallFor shortens the network timeout for one test.
func stallFor(t *testing.T, d time.Duration) {
	t.Helper()
	previous := networkTimeout
	networkTimeout = d
	t.Cleanup(func() { networkTimeout = previous })
}

// headOf returns the commit a repository's branch points at.
func headOf(t *testing.T, dir, branch string) string {
	t.Helper()
	out, err := Open(dir).run("rev-parse", "--verify", "--quiet", branch)
	if err != nil {
		return ""
	}
	return out
}

func TestCommitDoesNotWaitOnAStalledRemote(t *testing.T) {
	url, connected := stalledRemote(t)
	repo := repoWithRemote(t, url)
	stallFor(t, 5*time.Second)

	synced := make(chan error, 1)
	go func() { synced <- repo.Sync(context.Background(), Unguarded) }()

	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("fetch never reached the remote")
	}

	if err := os.WriteFile(filepath.Join(repo.root, "another.md"), []byte("# Another\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit("gandalf: note new another", "while the fetch is stalled"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// The commit returned. If the sync had already given up by then, the
	// commit may only have been waiting on it, which is the defect.
	select {
	case err := <-synced:
		t.Fatalf("sync finished (%v) before the commit did: the commit was waiting on the network", err)
	default:
	}

	out, err := repo.run("log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if out != "gandalf: note new another" {
		t.Errorf("last commit = %q, want the note commit to have landed during the stall", out)
	}

	select {
	case <-synced:
	case <-time.After(30 * time.Second):
		t.Fatal("sync never gave up on the stalled remote")
	}
}

func TestSyncGivesUpOnAStalledRemote(t *testing.T) {
	url, _ := stalledRemote(t)
	repo := repoWithRemote(t, url)
	stallFor(t, time.Second)

	start := time.Now()
	err := repo.Sync(context.Background(), Unguarded)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Sync error = %v, want the deadline to be reported", err)
	}
	if elapsed > 20*time.Second {
		t.Errorf("Sync took %s to give up on a %s timeout", elapsed, networkTimeout)
	}
}

func TestSyncPushesAndMergesThroughARemote(t *testing.T) {
	bare := bareRemote(t)
	repo := repoWithRemote(t, bare)

	branch, err := repo.currentBranch()
	if err != nil {
		t.Fatal(err)
	}

	// First sync: the remote has no branch yet, so this is the push that
	// sets the upstream.
	if err := repo.Sync(context.Background(), Unguarded); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if got, want := headOf(t, bare, branch), headOf(t, repo.root, "HEAD"); got == "" || got != want {
		t.Fatalf("after first sync remote %s = %q, local HEAD = %q", branch, got, want)
	}

	// Second sync: the remote has the branch, so the merge runs before the
	// push, and a local commit made in between travels.
	if err := os.WriteFile(filepath.Join(repo.root, "later.md"), []byte("# Later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit("gandalf: note new later", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := repo.Sync(context.Background(), Unguarded); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if got, want := headOf(t, bare, branch), headOf(t, repo.root, "HEAD"); got != want {
		t.Fatalf("after second sync remote %s = %q, local HEAD = %q", branch, got, want)
	}
}

func TestSyncWaitsOnTheGuardBeforeTouchingTheTree(t *testing.T) {
	repo := repoWithRemote(t, bareRemote(t))

	var guard sync.Mutex
	guard.Lock()

	synced := make(chan error, 1)
	go func() { synced <- repo.Sync(context.Background(), &guard) }()

	select {
	case err := <-synced:
		t.Fatalf("Sync finished (%v) while the guard was held", err)
	case <-time.After(100 * time.Millisecond):
	}

	guard.Unlock()

	select {
	case err := <-synced:
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Sync never finished once the guard was released")
	}
}
