package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matjam/gandalf/internal/git"
)

// breakGit empties the loose object HEAD points at, which is the state a crash
// left the incident vault in, and returns the function that puts it back.
func breakGit(t *testing.T, h *harness) (repair func()) {
	t.Helper()
	return breakGitAt(t, h.vault.Root())
}

// breakGitAt is breakGit for the repository at root, which is how a test
// breaks one core's repository and leaves the rest of its vaults alone.
func breakGitAt(t *testing.T, root string) (repair func()) {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	id := strings.TrimSpace(string(out))

	object := filepath.Join(root, ".git", "objects", id[:2], id[2:])
	original, err := os.ReadFile(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(object, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(object, 0); err != nil {
		t.Fatal(err)
	}

	return func() {
		t.Helper()
		if err := os.WriteFile(object, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// lastSubject is the subject of the vault's newest commit.
func lastSubject(h *harness) string {
	return gitIn(h.vault.Root(), "log", "-1", "--pretty=%s")
}

// gitWarningOf calls a tool and returns what its result says about git, and
// whether it said anything.
func gitWarningOf(h *harness, tool string, args any) (string, bool) {
	h.t.Helper()

	var out map[string]any
	h.call(tool, args, &out)

	warning, ok := out["git_warning"].(string)
	return warning, ok && warning != ""
}

// TestWritingToolsWarnWhenGitIsBroken: the note write succeeds, and the result
// says that nothing recorded it. Silence here is what let a vault run for a
// month with no commits.
func TestWritingToolsWarnWhenGitIsBroken(t *testing.T) {
	tests := []struct {
		tool string

		// setup runs while git still works and returns the arguments for the
		// call that follows the breakage.
		setup func(h *harness) any
	}{
		{"session_start", func(h *harness) any {
			return SessionStartInput{Title: "A broken morning"}
		}},
		{"note_new", func(h *harness) any {
			return NoteNewInput{Kind: "project", Scope: "other", Facet: "design", Title: "Other design", Content: designBody}
		}},
		{"note_append", func(h *harness) any {
			return NoteAppendInput{Ref: project(h, "gandalf", designBody), Content: "More."}
		}},
		{"note_update", func(h *harness) any {
			return NoteUpdateInput{Ref: project(h, "gandalf", designBody), AddTags: []string{"extra"}}
		}},
		{"note_replace", func(h *harness) any {
			return NoteReplaceInput{Ref: project(h, "gandalf", designBody), Section: "Verification", Content: "New verification."}
		}},
		{"note_delete", func(h *harness) any {
			return NoteDeleteInput{Ref: project(h, "doomed", designBody)}
		}},
		{"category_create", func(h *harness) any {
			return CategoryCreateInput{
				Name: "incident", Plural: "incidents", Rule: "dated",
				Folder: "Incidents", Description: "Something broke and why.",
			}
		}},
		{"topic_new", func(h *harness) any {
			return TopicNewInput{Title: "Review", When: "Reviewing a change.", Content: "Read the diff."}
		}},
		{"correct", func(h *harness) any {
			return CorrectInput{Guidance: "Check the repository before trusting it."}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			h := newGitHarness(t)
			args := tt.setup(h)

			breakGit(t, h)

			warning, ok := gitWarningOf(h, tt.tool, args)
			if !ok {
				t.Fatalf("%s returned no git_warning although the repository is broken", tt.tool)
			}
			for _, want := range []string{
				"saved to the vault but NOT recorded in git",
				"broken since",
				"NOT being committed or synced",
			} {
				if !strings.Contains(warning, want) {
					t.Errorf("git_warning = %q, want it to say %q", warning, want)
				}
			}
		})
	}
}

// TestNoWarningWhileGitWorks guards the other side: a field that is always
// present stops being read.
func TestNoWarningWhileGitWorks(t *testing.T) {
	for _, withGit := range []bool{true, false} {
		name := "no-git"
		if withGit {
			name = "git"
		}
		t.Run(name, func(t *testing.T) {
			h := harnessFor(t, withGit)

			var out map[string]any
			h.call("session_start", SessionStartInput{Title: "A calm morning"}, &out)
			if _, present := out["git_warning"]; present {
				t.Errorf("session_start carried git_warning %q with nothing wrong", out["git_warning"])
			}

			var boot map[string]any
			h.call("boot", BootInput{}, &boot)
			if _, present := boot["git_warning"]; present {
				t.Errorf("boot carried git_warning %q with nothing wrong", boot["git_warning"])
			}
		})
	}
}

// TestAWriteLandsWhileGitIsBroken: the warning is in addition to the write,
// never instead of it.
func TestAWriteLandsWhileGitIsBroken(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)
	breakGit(t, h)

	if _, ok := gitWarningOf(h, "note_append", NoteAppendInput{Ref: ref, Content: "Written while broken."}); !ok {
		t.Fatal("no warning")
	}

	var read NoteOutput
	h.call("note_read", NoteReadInput{Ref: ref}, &read)
	if !strings.Contains(read.Content, "Written while broken.") {
		t.Errorf("the append was lost:\n%s", read.Content)
	}
}

// TestBootReportsBrokenGit: what failed, since when, and what it means.
func TestBootReportsBrokenGit(t *testing.T) {
	h := newGitHarness(t)
	breakGit(t, h)

	var out BootOutput
	h.call("boot", BootInput{}, &out)

	for _, want := range []string{"broken since", "is empty", "NOT being committed or synced"} {
		if !strings.Contains(out.GitWarning, want) {
			t.Errorf("git_warning = %q, want it to mention %q", out.GitWarning, want)
		}
	}
	if len(out.Notices) == 0 || !strings.HasPrefix(out.Notices[0], "GIT IS NOT WORKING: ") {
		t.Errorf("notices = %q, want the git notice first", out.Notices)
	}
}

// TestHistoryToolsSayTheRepositoryIsBroken: "this vault is not a git
// repository" sends a model off to create one, on top of the one that is there.
func TestHistoryToolsSayTheRepositoryIsBroken(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)
	breakGit(t, h)

	tests := []struct {
		tool string
		args any
	}{
		{"history", HistoryInput{}},
		{"history", HistoryInput{Ref: ref}},
		{"note_version", NoteVersionInput{Ref: ref, Commit: "HEAD"}},
		{"note_diff", NoteDiffInput{Ref: ref, From: "HEAD"}},
		{"note_restore", NoteRestoreInput{Ref: ref, Commit: "HEAD"}},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := h.callErr(tt.tool, tt.args)

			if !strings.Contains(got, "the vault's git repository is broken: ") {
				t.Errorf("%s said %q, want it to say the repository is broken", tt.tool, got)
			}
			if strings.Contains(got, "not a git repository") {
				t.Errorf("%s said %q, which treats a broken repository as absent", tt.tool, got)
			}
		})
	}
}

// TestHistoryStillSaysNoGitWithoutGit keeps the old answer where it is true.
func TestHistoryStillSaysNoGitWithoutGit(t *testing.T) {
	h := newHarness(t)

	got := h.callErr("history", HistoryInput{})
	if !strings.Contains(got, "not a git repository") {
		t.Errorf("history said %q, want the not-a-repository answer for a vault run without git", got)
	}
}

// TestRepairResumesCommitting: the next write after a repair gets a normal
// commit, preceded in effect by a checkpoint of what piled up, and history
// works again, all without restarting anything.
func TestRepairResumesCommitting(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)
	repair := breakGit(t, h)

	if _, ok := gitWarningOf(h, "note_append", NoteAppendInput{Ref: ref, Content: "During the outage."}); !ok {
		t.Fatal("no warning while broken")
	}

	repair()

	warning, ok := gitWarningOf(h, "note_append", NoteAppendInput{Ref: ref, Content: "After the repair."})
	if ok {
		t.Fatalf("still warning after the repair: %q", warning)
	}
	if got := lastSubject(h); got != "gandalf: checkpoint after git recovered" {
		t.Errorf("newest commit = %q, want the recovery checkpoint", got)
	}

	// History reads again, and the note's newest commit is the checkpoint
	// that swept in the outage's appends and the one made after the repair,
	// and says so.
	var out HistoryOutput
	h.call("history", HistoryInput{Ref: ref}, &out)
	if len(out.Commits) != 2 {
		t.Fatalf("got %d commits for the note, want its creation and the checkpoint: %+v", len(out.Commits), out.Commits)
	}
	if !strings.Contains(out.Commits[0].Reason, "git was broken since") {
		t.Errorf("checkpoint reason = %q, want it to say git was broken", out.Commits[0].Reason)
	}
	var read NoteOutput
	h.call("note_read", NoteReadInput{Ref: ref}, &read)
	for _, want := range []string{"During the outage.", "After the repair."} {
		if !strings.Contains(read.Content, want) {
			t.Errorf("note lost %q", want)
		}
	}

	// And boot is clean.
	var boot BootOutput
	h.call("boot", BootInput{}, &boot)
	if boot.GitWarning != "" {
		t.Errorf("boot still warns after the repair: %q", boot.GitWarning)
	}
}

// TestBootNoticesARepair: a session starting after the repair is told nothing
// is wrong, and the checkpoint is made on the way.
func TestBootNoticesARepair(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)
	repair := breakGit(t, h)

	if _, ok := gitWarningOf(h, "note_append", NoteAppendInput{Ref: ref, Content: "During the outage."}); !ok {
		t.Fatal("no warning while broken")
	}
	repair()

	var boot BootOutput
	h.call("boot", BootInput{}, &boot)
	if boot.GitWarning != "" {
		t.Errorf("boot warned after the repair: %q", boot.GitWarning)
	}
	if got := lastSubject(h); got != "gandalf: checkpoint after git recovered" {
		t.Errorf("newest commit = %q, want the recovery checkpoint", got)
	}
}

// TestSyncTickNoticesARepair: nothing is written after the repair, and the
// periodic sync is what finds it.
func TestSyncTickNoticesARepair(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)
	repair := breakGit(t, h)

	if _, ok := gitWarningOf(h, "note_append", NoteAppendInput{Ref: ref, Content: "During the outage."}); !ok {
		t.Fatal("no warning while broken")
	}
	if err := git.SaveConfig(h.vault.Root(), git.Config{SyncInterval: "20ms"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	repair()

	ctx, cancel := context.WithCancel(h.context)
	defer cancel()
	stopped := h.server.StartSync(ctx)
	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if h.server.git.Health().State == git.StateHealthy && lastSubject(h) == "gandalf: checkpoint after git recovered" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the sync loop never noticed the repair: health = %+v, newest commit = %q",
		h.server.git.Health(), lastSubject(h))
}
