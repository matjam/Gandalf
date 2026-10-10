package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// committedRepo is a repository with a note committed on top of the initial
// commit, so HEAD has a parent, a tree, and something in it.
func committedRepo(t *testing.T) *Repo {
	t.Helper()
	requireGit(t)

	repo := Open(t.TempDir())
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeNote(t, repo, "first.md", "# First\n")
	if err := repo.Commit("gandalf: note new first", "a note to commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return repo
}

func writeNote(t *testing.T, repo *Repo, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo.root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// objectPath is where git keeps one loose object.
func objectPath(repo *Repo, id string) string {
	return filepath.Join(repo.root, ".git", "objects", id[:2], id[2:])
}

// resolve returns the id a revision names.
func resolve(t *testing.T, repo *Repo, rev string) string {
	t.Helper()
	out, err := repo.run("rev-parse", "--verify", rev)
	if err != nil {
		t.Fatalf("rev-parse %s: %v", rev, err)
	}
	return out
}

// damage is one way a crash left a repository unreadable. Each takes the
// repository and returns a function that undoes it, so a test can repair what
// it broke and check that the repair is noticed.
type damage struct {
	name  string
	apply func(t *testing.T, repo *Repo) (repair func())
}

// emptyObject truncates a loose object to no bytes, which is what the incident
// vault's HEAD commit was, and returns what puts it back.
func emptyObject(t *testing.T, repo *Repo, id string) func() {
	t.Helper()

	path := objectPath(repo, id)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// missingObject deletes a loose object.
func missingObject(t *testing.T, repo *Repo, id string) func() {
	t.Helper()

	path := objectPath(repo, id)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var damages = []damage{
	{"HEAD commit is empty", func(t *testing.T, repo *Repo) func() {
		return emptyObject(t, repo, resolve(t, repo, "HEAD"))
	}},
	{"HEAD commit is missing", func(t *testing.T, repo *Repo) func() {
		return missingObject(t, repo, resolve(t, repo, "HEAD"))
	}},
	{"HEAD's tree is empty", func(t *testing.T, repo *Repo) func() {
		return emptyObject(t, repo, resolve(t, repo, "HEAD^{tree}"))
	}},
	{"HEAD's tree is missing", func(t *testing.T, repo *Repo) func() {
		return missingObject(t, repo, resolve(t, repo, "HEAD^{tree}"))
	}},
	{"HEAD itself is unreadable", func(t *testing.T, repo *Repo) func() {
		path := filepath.Join(repo.root, ".git", "HEAD")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not a ref\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return func() {
			t.Helper()
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}},
}

// TestABrokenRepositoryIsNotAbsent is the incident. A repository whose HEAD
// cannot be read used to come back from startup as no repository at all, and
// everything downstream then behaved as though the vault had never been one.
func TestABrokenRepositoryIsNotAbsent(t *testing.T) {
	for _, d := range damages {
		t.Run(d.name, func(t *testing.T) {
			repo := committedRepo(t)
			d.apply(t, repo)

			// A fresh process: nothing is remembered from before the damage.
			restarted := Open(repo.root)
			err := restarted.Ensure()

			if !errors.Is(err, ErrBroken) {
				t.Fatalf("Ensure = %v, want it to report the repository as broken", err)
			}
			if !restarted.IsRepo() {
				t.Error("the repository is there, so IsRepo must say so")
			}

			health := restarted.Health()
			if health.State != StateBroken || health.Err == nil || health.Since.IsZero() {
				t.Errorf("health = %+v, want broken with a cause and a time", health)
			}

			avail := restarted.Available()
			if !errors.Is(avail, ErrBroken) {
				t.Errorf("Available = %v, want ErrBroken", avail)
			}
			if errors.Is(avail, ErrNoGit) {
				t.Errorf("Available = %v, which says there is no repository", avail)
			}
			if !strings.Contains(avail.Error(), "the vault's git repository is broken: ") {
				t.Errorf("Available = %q, want the broken wording followed by the cause", avail)
			}

			if _, err := restarted.Log("", 0); !errors.Is(err, ErrBroken) {
				t.Errorf("Log = %v, want ErrBroken rather than an empty history", err)
			}
			if _, err := restarted.LogAll(5); !errors.Is(err, ErrBroken) {
				t.Errorf("LogAll = %v, want ErrBroken rather than an empty history", err)
			}
		})
	}
}

// TestBreakageIsNoticedByARunningServer covers damage that arrives while the
// process is up, which is how a crash of the machine looks to the next write.
func TestBreakageIsNoticedByARunningServer(t *testing.T) {
	for _, d := range damages {
		t.Run(d.name, func(t *testing.T) {
			repo := committedRepo(t)
			d.apply(t, repo)

			writeNote(t, repo, "second.md", "# Second\n")
			err := repo.Commit("gandalf: note new second", "written after the damage")
			if !errors.Is(err, ErrBroken) {
				t.Fatalf("Commit = %v, want ErrBroken", err)
			}

			health := repo.Health()
			if health.State != StateBroken || health.Err == nil {
				t.Fatalf("health = %+v, want broken", health)
			}
			since := health.Since

			// Failing again does not restart the clock.
			writeNote(t, repo, "third.md", "# Third\n")
			if err := repo.Commit("gandalf: note new third", ""); !errors.Is(err, ErrBroken) {
				t.Fatalf("second Commit = %v, want ErrBroken", err)
			}
			if got := repo.Health().Since; !got.Equal(since) {
				t.Errorf("since moved from %s to %s while the repository stayed broken", since, got)
			}
		})
	}
}

// TestRepairIsNoticedWithoutARestart is the other half: a repository that was
// broken and is repaired starts committing again, picks up what piled up in
// the meantime, and says so.
func TestRepairIsNoticedWithoutARestart(t *testing.T) {
	recoveries := []struct {
		name string

		// notice is whatever the running process does that finds the repair.
		notice func(t *testing.T, repo *Repo)

		// wantSubject is the commit that should now be on top.
		wantSubject string
	}{
		{
			name: "the next write",
			notice: func(t *testing.T, repo *Repo) {
				writeNote(t, repo, "after.md", "# After\n")
				if err := repo.Commit("gandalf: note new after", "written once it was repaired"); err != nil {
					t.Fatalf("Commit after repair: %v", err)
				}
			},
			wantSubject: "gandalf: checkpoint after git recovered",
		},
		{
			name: "the next sync tick",
			notice: func(t *testing.T, repo *Repo) {
				if err := repo.Sync(context.Background(), Unguarded); err != nil {
					t.Fatalf("Sync after repair: %v", err)
				}
			},
			wantSubject: "gandalf: checkpoint after git recovered",
		},
	}

	for _, d := range damages {
		for _, rec := range recoveries {
			t.Run(d.name+", noticed by "+rec.name, func(t *testing.T) {
				repo := committedRepo(t)
				repair := d.apply(t, repo)

				writeNote(t, repo, "while-broken.md", "# While broken\n")
				if err := repo.Commit("gandalf: note new while-broken", "written while broken"); !errors.Is(err, ErrBroken) {
					t.Fatalf("Commit = %v, want ErrBroken", err)
				}

				repair()
				rec.notice(t, repo)

				if health := repo.Health(); health.State != StateHealthy || health.Err != nil {
					t.Fatalf("health after repair = %+v, want healthy", health)
				}

				subject, err := repo.run("log", "-1", "--pretty=%s")
				if err != nil {
					t.Fatalf("log: %v", err)
				}
				if subject != rec.wantSubject {
					t.Errorf("last commit = %q, want %q", subject, rec.wantSubject)
				}

				body, _ := repo.run("log", "-1", "--pretty=%b")
				if !strings.Contains(body, "git was broken since") {
					t.Errorf("checkpoint reason = %q, want it to say git was broken", body)
				}

				// What was written while it was broken is in history now.
				files, err := repo.run("show", "--name-only", "--pretty=format:", "HEAD")
				if err != nil {
					t.Fatalf("show: %v", err)
				}
				if !strings.Contains(files, "while-broken.md") {
					t.Errorf("checkpoint holds %q, want the note written while broken", files)
				}

				// And it is a working repository again: an ordinary write
				// gets an ordinary commit.
				writeNote(t, repo, "ordinary.md", "# Ordinary\n")
				if err := repo.Commit("gandalf: note new ordinary", "back to normal"); err != nil {
					t.Fatalf("Commit: %v", err)
				}
				if subject, _ := repo.run("log", "-1", "--pretty=%s"); subject != "gandalf: note new ordinary" {
					t.Errorf("last commit = %q, want an ordinary commit", subject)
				}
			})
		}
	}
}

// TestBrokenAtStartupRecovers: the case where the process never saw the
// repository healthy.
func TestBrokenAtStartupRecovers(t *testing.T) {
	repo := committedRepo(t)
	repair := emptyObject(t, repo, resolve(t, repo, "HEAD"))

	restarted := Open(repo.root)
	if err := restarted.Ensure(); !errors.Is(err, ErrBroken) {
		t.Fatalf("Ensure = %v, want ErrBroken", err)
	}
	writeNote(t, restarted, "during.md", "# During\n")

	repair()
	if err := restarted.Check(Unguarded); err != nil {
		t.Fatalf("Check after repair: %v", err)
	}
	if health := restarted.Health(); health.State != StateHealthy {
		t.Fatalf("health = %+v, want healthy", health)
	}
	if files, _ := restarted.run("show", "--name-only", "--pretty=format:", "HEAD"); !strings.Contains(files, "during.md") {
		t.Errorf("recovery commit holds %q, want the note written during the outage", files)
	}
}

// TestAnUnbornRepositoryIsHealthy: no commits yet is a new vault and not a
// damaged one, though the probe fails on it exactly as it does on damage.
func TestAnUnbornRepositoryIsHealthy(t *testing.T) {
	requireGit(t)

	repo := Open(t.TempDir())
	if _, err := repo.run("init"); err != nil {
		t.Fatal(err)
	}

	if err := repo.probe(); err != nil {
		t.Errorf("probe on a repository with no commits = %v, want nil", err)
	}
	if health := repo.Inspect(); health.State != StateHealthy {
		t.Errorf("Inspect = %+v, want healthy", health)
	}
	if entries, err := repo.LogAll(0); err != nil || len(entries) != 0 {
		t.Errorf("LogAll = %v, %v; want an empty history and no error", entries, err)
	}
}

// TestNoRepositoryIsAbsentNotBroken keeps the other distinction: a vault that
// has no .git is not broken, and Ensure makes one.
func TestNoRepositoryIsAbsentNotBroken(t *testing.T) {
	requireGit(t)

	repo := Open(t.TempDir())
	if got := repo.Inspect().State; got != StateAbsent {
		t.Errorf("Inspect on a bare directory = %s, want absent", got)
	}
	if err := repo.Available(); !errors.Is(err, ErrNoGit) || errors.Is(err, ErrBroken) {
		t.Errorf("Available = %v, want ErrNoGit", err)
	}
	if err := repo.Commit("nothing", ""); err != nil {
		t.Errorf("Commit on a vault that was never a repository = %v, want a no-op", err)
	}

	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := repo.Health().State; got != StateHealthy {
		t.Errorf("health after Ensure = %s, want healthy", got)
	}
}

// TestADisabledRepoHasNoHealthToReport covers -no-git.
func TestADisabledRepoHasNoHealthToReport(t *testing.T) {
	repo := Open(t.TempDir())
	repo.Disable()
	if got := repo.Health().State; got != StateDisabled {
		t.Errorf("health = %s, want disabled", got)
	}
	var none *Repo
	if got := none.Health().State; got != StateDisabled {
		t.Errorf("nil repo health = %s, want disabled", got)
	}
}

// TestADeletedGitDirectoryIsDamage: a repository this process has seen and
// that then disappears is not "a vault without git".
func TestADeletedGitDirectoryIsDamage(t *testing.T) {
	repo := committedRepo(t)
	if err := os.RemoveAll(filepath.Join(repo.root, ".git")); err != nil {
		t.Fatal(err)
	}

	writeNote(t, repo, "after.md", "# After\n")
	if err := repo.Commit("gandalf: note new after", ""); !errors.Is(err, ErrBroken) {
		t.Fatalf("Commit = %v, want ErrBroken", err)
	}
	if err := repo.Available(); !errors.Is(err, ErrBroken) {
		t.Errorf("Available = %v, want ErrBroken", err)
	}
}

// TestEmptyObjectsAreReportedByName creates the artefact a crash leaves and
// checks it is found, named, and treated as breakage even when HEAD is fine.
func TestEmptyObjectsAreReportedByName(t *testing.T) {
	repo := committedRepo(t)

	if empty, err := repo.EmptyObjects(); err != nil || len(empty) != 0 {
		t.Fatalf("EmptyObjects on a healthy repository = %v, %v; want none", empty, err)
	}

	// Two empty loose objects, neither reachable from HEAD, plus files that
	// are not objects and must not be reported.
	ids := []string{
		"ab" + strings.Repeat("c", 38),
		"00" + strings.Repeat("1", 38),
	}
	for _, id := range ids {
		path := objectPath(repo, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	for _, stray := range []string{
		filepath.Join(repo.root, ".git", "objects", "ab", "tmp_obj_123456"),
		filepath.Join(repo.root, ".git", "objects", "info", "alternates"),
		filepath.Join(repo.root, ".git", "objects", "pack", "pack-0.idx"),
	} {
		if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stray, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	empty, err := repo.EmptyObjects()
	if err != nil {
		t.Fatalf("EmptyObjects: %v", err)
	}
	want := []string{ids[1], ids[0]}
	if strings.Join(empty, ",") != strings.Join(want, ",") {
		t.Errorf("EmptyObjects = %v, want %v, sorted and by id", empty, want)
	}

	// HEAD reads fine, but an empty object is a trap for the next commit, so
	// the full check says so, by name.
	health := repo.Inspect()
	if health.State != StateBroken {
		t.Fatalf("Inspect = %+v, want broken", health)
	}
	for _, id := range ids {
		if !strings.Contains(health.Err.Error(), id) {
			t.Errorf("error %q does not name %s", health.Err, id)
		}
	}
	if err := repo.Check(Unguarded); !errors.Is(err, ErrBroken) {
		t.Errorf("Check = %v, want ErrBroken", err)
	}

	// Removing them is the repair.
	for _, id := range ids {
		if err := os.Remove(objectPath(repo, id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Check(Unguarded); err != nil {
		t.Errorf("Check after removing them = %v, want nil", err)
	}
	if got := repo.Health().State; got != StateHealthy {
		t.Errorf("health = %s, want healthy", got)
	}
}

// TestAVaultInsideAnotherRepositoryIsNotMistakenForIt: git walks upward when
// it cannot make sense of a .git directory, and would then check the
// neighbouring repository and report it healthy.
func TestAVaultInsideAnotherRepositoryIsNotMistakenForIt(t *testing.T) {
	outer := Open(t.TempDir())
	if err := outer.Ensure(); err != nil {
		t.Fatalf("outer Ensure: %v", err)
	}

	vault := filepath.Join(outer.root, "vault")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := Open(vault)
	if err := inner.Ensure(); err != nil {
		t.Fatalf("inner Ensure: %v", err)
	}
	writeNote(t, inner, "note.md", "# Note\n")
	if err := inner.Commit("gandalf: note new note", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := os.WriteFile(filepath.Join(vault, ".git", "HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := Open(vault).Inspect().State; got != StateBroken {
		t.Errorf("Inspect = %s, want broken, not the outer repository's health", got)
	}
}

// TestSyncFailuresFeedHealth: a remote that cannot be reached is recorded,
// counted, and cleared on the first success, without ever calling the
// repository itself broken.
func TestSyncFailuresFeedHealth(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-remote.git")
	repo := repoWithRemote(t, missing)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := repo.Sync(context.Background(), Unguarded); err == nil {
			t.Fatalf("Sync %d against a missing remote succeeded", attempt)
		}

		health := repo.Health()
		if health.SyncErr == nil || health.SyncFailures != attempt || health.SyncSince.IsZero() {
			t.Fatalf("after %d failures health = %+v", attempt, health)
		}
		if health.State != StateHealthy {
			t.Errorf("state = %s: an unreachable remote does not stop commits", health.State)
		}
		if problems := health.Problem(); len(problems) != 1 || !strings.Contains(problems[0], "syncing with the git remote") {
			t.Errorf("Problem = %v, want one sentence about the remote", problems)
		}
	}

	if _, err := repo.SetRemote(bareRemote(t)); err != nil {
		t.Fatalf("SetRemote: %v", err)
	}
	if err := repo.Sync(context.Background(), Unguarded); err != nil {
		t.Fatalf("Sync against a working remote: %v", err)
	}
	if health := repo.Health(); health.SyncErr != nil || health.SyncFailures != 0 || len(health.Problem()) != 0 {
		t.Errorf("health after a good sync = %+v, want the failures cleared", health)
	}
}

// TestSyncRefusesABrokenRepository: no remote, a configured remote, either way
// the tick says why nothing is happening.
func TestSyncRefusesABrokenRepository(t *testing.T) {
	repo := repoWithRemote(t, bareRemote(t))
	emptyObject(t, repo, resolve(t, repo, "HEAD"))

	err := repo.Sync(context.Background(), Unguarded)
	if !errors.Is(err, ErrBroken) {
		t.Fatalf("Sync = %v, want ErrBroken", err)
	}
	if got := repo.Health().State; got != StateBroken {
		t.Errorf("health = %s, want broken", got)
	}
	if problems := repo.Health().Problem(); len(problems) != 1 ||
		!strings.Contains(problems[0], "NOT being committed or synced") {
		t.Errorf("Problem = %v, want it to say changes are not being committed or synced", problems)
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in           string
		major, minor int
		ok           bool
		fsync        bool
	}{
		{"git version 2.55.0", 2, 55, true, true},
		{"git version 2.36.0", 2, 36, true, true},
		{"git version 2.35.8", 2, 35, true, false},
		{"git version 2.39.3 (Apple Git-146)", 2, 39, true, true},
		{"git version 2.45.0.windows.1", 2, 45, true, true},
		{"git version 1.8.3.1", 1, 8, true, false},
		{"git version 3.0.0", 3, 0, true, true},
		{"git version", 0, 0, false, false},
		{"git version banana", 0, 0, false, false},
		{"", 0, 0, false, false},
	}

	for _, tt := range tests {
		major, minor, ok := parseVersion(tt.in)
		if major != tt.major || minor != tt.minor || ok != tt.ok {
			t.Errorf("parseVersion(%q) = %d.%d, %v; want %d.%d, %v", tt.in, major, minor, ok, tt.major, tt.minor, tt.ok)
		}
		if ok && supportsFsync(major, minor) != tt.fsync {
			t.Errorf("supportsFsync(%d.%d) = %v, want %v", major, minor, !tt.fsync, tt.fsync)
		}
	}
}

// TestEnsurePinsDurability: git hardens refs and packs by default but not
// loose objects, which is where a crash leaves empty files. Ensure sets
// it for a new repository and corrects it on one that already exists.
func TestEnsurePinsDurability(t *testing.T) {
	requireGit(t)

	probe := Open(t.TempDir())
	major, minor, ok := probe.gitVersion()
	if ok && !supportsFsync(major, minor) {
		t.Skipf("git %d.%d predates core.fsync", major, minor)
	}

	read := func(t *testing.T, repo *Repo, key string) string {
		t.Helper()
		out, err := repo.run("config", "--local", "--get", key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		return out
	}

	check := func(t *testing.T, repo *Repo) {
		t.Helper()
		if got := read(t, repo, "core.fsync"); got != "objects,derived-metadata,reference" {
			t.Errorf("core.fsync = %q, want objects, derived metadata and references", got)
		}
		if got := read(t, repo, "core.fsyncMethod"); got != "fsync" {
			t.Errorf("core.fsyncMethod = %q, want fsync", got)
		}
	}

	t.Run("a new repository", func(t *testing.T) {
		repo := Open(t.TempDir())
		if err := repo.Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		check(t, repo)
	})

	t.Run("an existing repository", func(t *testing.T) {
		// A repository made by hand, or by an older Gandalf, with the
		// defaults and then somebody's idea of faster.
		repo := Open(t.TempDir())
		if _, err := repo.run("init"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.run("config", "core.fsync", "none"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.run("config", "core.fsyncMethod", "writeout-only"); err != nil {
			t.Fatal(err)
		}

		if err := repo.Ensure(); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
		check(t, repo)
	})
}
