package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitAsAttributesTheAuthor(t *testing.T) {
	requireGit(t)

	root := t.TempDir()
	repo := Open(root)
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	cases := []struct {
		name       string
		author     *Author
		wantAuthor string
	}{
		{"attributed", &Author{Name: "u_abc", Email: "u_abc@users.gandalf.invalid"}, "u_abc <u_abc@users.gandalf.invalid>"},
		{"delimiters cannot break out", &Author{Name: "u_x <evil@x>\nCo-Authored-By: y", Email: "u_x@users.gandalf.invalid"}, "u_x evil@xCo-Authored-By: y <u_x@users.gandalf.invalid>"},
		{"nil keeps the repository identity", nil, "Gandalf <gandalf@localhost>"},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(root, "note"+string(rune('a'+i))+".md")
			if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := repo.CommitAs("gandalf: note new", "because", c.author); err != nil {
				t.Fatalf("CommitAs: %v", err)
			}

			got, err := repo.run("log", "-1", "--format=%an <%ae>|%cn <%ce>")
			if err != nil {
				t.Fatalf("log: %v", err)
			}
			author, committer, _ := strings.Cut(got, "|")
			if author != c.wantAuthor {
				t.Errorf("author = %q, want %q", author, c.wantAuthor)
			}
			// Attribution never changes who the committer is.
			if committer != "Gandalf <gandalf@localhost>" {
				t.Errorf("committer = %q, want the repository identity", committer)
			}
		})
	}
}

// An attributed commit is held to the same health checks as any other: the
// pre-commit probe refuses a broken repository, and the record says so.
func TestCommitAsIsProbedLikeCommit(t *testing.T) {
	author := &Author{Name: "u_abc", Email: "u_abc@users.gandalf.invalid"}

	for _, d := range damages {
		t.Run(d.name, func(t *testing.T) {
			repo := committedRepo(t)
			d.apply(t, repo)

			writeNote(t, repo, "second.md", "# Second\n")
			err := repo.CommitAs("gandalf: note new second", "written after the damage", author)
			if !errors.Is(err, ErrBroken) {
				t.Fatalf("CommitAs = %v, want ErrBroken", err)
			}
			if health := repo.Health(); !health.Broken() {
				t.Fatalf("health = %+v, want broken", health)
			}
			if repo.author != nil {
				t.Error("the author outlived the failed commit")
			}
		})
	}
}

// The recovery checkpoint records everything written while git was broken, by
// anyone, so it belongs to the repository and not to whoever wrote first after
// the repair; the ordinary commit after it is attributed again.
func TestCommitAsRecoverySweepIsNotAttributed(t *testing.T) {
	author := &Author{Name: "u_abc", Email: "u_abc@users.gandalf.invalid"}

	repo := committedRepo(t)
	repair := damages[0].apply(t, repo)

	writeNote(t, repo, "while-broken.md", "# While broken\n")
	if err := repo.CommitAs("gandalf: note new while-broken", "", author); !errors.Is(err, ErrBroken) {
		t.Fatalf("CommitAs = %v, want ErrBroken", err)
	}
	repair()

	writeNote(t, repo, "after.md", "# After\n")
	if err := repo.CommitAs("gandalf: note new after", "", author); err != nil {
		t.Fatalf("CommitAs after repair: %v", err)
	}
	got, _ := repo.run("log", "-1", "--format=%s|%an <%ae>")
	if want := "gandalf: checkpoint after git recovered|Gandalf <gandalf@localhost>"; got != want {
		t.Errorf("sweep = %q, want %q", got, want)
	}

	writeNote(t, repo, "ordinary.md", "# Ordinary\n")
	if err := repo.CommitAs("gandalf: note new ordinary", "", author); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.run("log", "-1", "--format=%an <%ae>")
	if want := "u_abc <u_abc@users.gandalf.invalid>"; got != want {
		t.Errorf("ordinary author = %q, want %q", got, want)
	}
}
