package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/git"
)

func TestPrepareRequiresAVault(t *testing.T) {
	if _, _, err := prepare("", true, true); err == nil {
		t.Error("prepare succeeded without a vault path; the server should not guess")
	}
}

func TestPrepareSeeds(t *testing.T) {
	silence(t)

	root := filepath.Join(t.TempDir(), "vault")

	v, repo, err := prepare(root, true, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !v.Exists("Gandalf/Operating.md") {
		t.Error("prepare did not seed the contract")
	}
	if repo == nil || !repo.IsRepo() {
		t.Error("prepare did not create a git repository")
	}
}

func TestPrepareCanSkipSeeding(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")

	v, _, err := prepare(root, false, false)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if v.Exists("Gandalf/Operating.md") {
		t.Error("prepare seeded despite being told not to")
	}

	// The directory is still created, so a later init has somewhere to write.
	if _, err := os.Stat(v.Root()); err != nil {
		t.Errorf("vault root was not created: %v", err)
	}
}

func TestServeRejectsBadFlags(t *testing.T) {
	silence(t)

	if err := run([]string{"serve"}); err == nil {
		t.Error("serve without -vault succeeded")
	}
	if err := run([]string{"serve", "-nonsense"}); err == nil {
		t.Error("serve with an unknown flag succeeded")
	}
}

// emptyHeadCommit zeroes the loose object HEAD points at, which is what a
// crash left behind in the vault that prompted this test: the branch ref was
// intact, the commit it named was a file of no bytes, and `git log` answered
// "bad object HEAD".
func emptyHeadCommit(t *testing.T, root string) {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	id := strings.TrimSpace(string(out))

	object := filepath.Join(root, ".git", "objects", id[:2], id[2:])
	if err := os.Chmod(object, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", object, err)
	}
	if err := os.Truncate(object, 0); err != nil {
		t.Fatalf("truncate %s: %v", object, err)
	}
}

// TestPrepareKeepsABrokenRepositoryAttached pins the defect that hid a month
// of lost history. A repository that exists but cannot be read is not the same
// as no repository: prepare used to print the error and hand back a nil repo,
// and a server holding nil answers every history question with "not a git
// repository" and commits nothing, indefinitely, with no one told.
func TestPrepareKeepsABrokenRepositoryAttached(t *testing.T) {
	silence(t)

	root := filepath.Join(t.TempDir(), "vault")
	if _, repo, err := prepare(root, true, true); err != nil || repo == nil {
		t.Fatalf("prepare on a fresh vault: repo=%v err=%v", repo, err)
	}
	emptyHeadCommit(t, root)

	_, repo, err := prepare(root, false, true)
	if err != nil {
		t.Fatalf("prepare refused to serve a vault with a broken repository: %v", err)
	}
	if repo == nil {
		t.Fatal("prepare dropped the repository, so the server would run as if the vault were not under git")
	}
	if !repo.IsRepo() {
		t.Error("the repository exists, so it must still be reported as one")
	}
	if health := repo.Health(); health.State != git.StateBroken || health.Err == nil {
		t.Errorf("health = %+v, want broken with the error that broke it", health)
	}
}

// TestDoctorReportsGitHealth: doctor says whether the repository works, names
// what is wrong with one that does not, and fails so a script notices.
func TestDoctorReportsGitHealth(t *testing.T) {
	silence(t)

	tests := []struct {
		name  string
		setup func(t *testing.T, root string)

		wantBroken bool
		want       []string
	}{
		{
			name:  "healthy",
			setup: func(t *testing.T, root string) {},
			want:  []string{"git: healthy"},
		},
		{
			name: "no repository",
			setup: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"no repository here"},
		},
		{
			name:       "HEAD's commit is empty",
			setup:      emptyHeadCommit,
			wantBroken: true,
			want:       []string{"git: BROKEN: ", "is empty", "not being committed or synced"},
		},
		{
			name: "an unreachable object is empty",
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, ".git", "objects", "ab")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, strings.Repeat("c", 38)), nil, 0o444); err != nil {
					t.Fatal(err)
				}
			},
			wantBroken: true,
			want:       []string{"git: BROKEN: ", "1 zero-byte loose object(s)", "ab" + strings.Repeat("c", 38)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "vault")
			if _, _, err := prepare(root, true, true); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			tt.setup(t, root)

			lines, broken := gitReport(root)
			report := strings.Join(lines, "\n")

			if broken != tt.wantBroken {
				t.Errorf("broken = %v, want %v:\n%s", broken, tt.wantBroken, report)
			}
			for _, want := range tt.want {
				if !strings.Contains(report, want) {
					t.Errorf("report does not contain %q:\n%s", want, report)
				}
			}

			err := run([]string{"doctor", "-vault", root})
			if tt.wantBroken != errors.Is(err, git.ErrBroken) {
				t.Errorf("doctor returned %v, want a broken-repository failure = %v", err, tt.wantBroken)
			}
		})
	}
}
