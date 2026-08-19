package server

import (
	"strings"
	"testing"
)

// project creates a design note for a named project, which is the replaceable
// case and so the one restore does not have to force.
func project(h *harness, scope, content string) string {
	h.t.Helper()

	var out NoteOutput
	h.call("note_new", NoteNewInput{
		Kind:    "project",
		Scope:   scope,
		Facet:   "design",
		Title:   scope + " — Design",
		Content: content,
		Tags:    []string{"gandalf"},
	}, &out)

	return out.Ref
}

// history returns a note's commits, newest first.
func history(h *harness, ref string) HistoryOutput {
	h.t.Helper()

	var out HistoryOutput
	h.call("history", HistoryInput{Ref: ref}, &out)
	return out
}

func TestHistoryListsANotesCommits(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_replace", NoteReplaceInput{
		Ref:     ref,
		Section: "Verification",
		Content: "New verification.",
	}, nil)

	out := history(h, ref)
	if len(out.Commits) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(out.Commits), out.Commits)
	}
	if out.Ref != ref {
		t.Errorf("ref = %q, want %q", out.Ref, ref)
	}

	// Newest first, and each says what it did to this note.
	if out.Commits[0].Change != "modified" || out.Commits[1].Change != "added" {
		t.Errorf("changes = %q, %q; want modified then added",
			out.Commits[0].Change, out.Commits[1].Change)
	}
	if !strings.Contains(out.Commits[0].Message, "note replace") {
		t.Errorf("newest message = %q", out.Commits[0].Message)
	}
	for i, c := range out.Commits {
		if c.Commit == "" || c.Date == "" {
			t.Errorf("commit %d is missing metadata: %+v", i, c)
		}
		if len(c.Notes) != 0 {
			t.Errorf("commit %d listed notes in a history scoped to one: %+v", i, c.Notes)
		}
	}
}

func TestHistoryOverTheWholeVault(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	var out HistoryOutput
	h.call("history", HistoryInput{Limit: 1}, &out)

	if len(out.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(out.Commits))
	}
	if out.Ref != "" {
		t.Errorf("a vault-wide history reported a ref: %q", out.Ref)
	}

	var found bool
	for _, note := range out.Commits[0].Notes {
		if note.Ref == ref {
			found = true
			if note.Change != "added" {
				t.Errorf("change = %q, want added", note.Change)
			}
		}
	}
	if !found {
		t.Errorf("the newest commit does not mention %s: %+v", ref, out.Commits[0].Notes)
	}
}

func TestNoteVersionReturnsPastContent(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_replace", NoteReplaceInput{
		Ref:     ref,
		Section: "Verification",
		Content: "New verification.",
	}, nil)

	out := history(h, ref)
	first := out.Commits[len(out.Commits)-1].Commit

	var version NoteVersionOutput
	h.call("note_version", NoteVersionInput{Ref: ref, Commit: first}, &version)

	if !strings.Contains(version.Content, "Old verification.") {
		t.Errorf("the first version does not hold the original text:\n%s", version.Content)
	}
	if strings.Contains(version.Content, "New verification.") {
		t.Errorf("the first version holds text written after it:\n%s", version.Content)
	}
	if version.Ref != ref || version.Commit != first {
		t.Errorf("version = %s at %s, want %s at %s", version.Ref, version.Commit, ref, first)
	}
	if version.Title == "" || version.Type == "" {
		t.Errorf("metadata was not parsed out of the version: %+v", version)
	}

	// Reading a version must not touch the note.
	var current NoteOutput
	h.call("note_read", NoteReadInput{Ref: ref}, &current)
	if !strings.Contains(current.Content, "New verification.") {
		t.Error("note_version changed the note it was asked about")
	}
}

// TestNoteRestoreNeedsTheVersionRead is the gate: restoring a version nobody
// has looked at is the failure worth making impossible.
func TestNoteRestoreNeedsTheVersionRead(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_replace", NoteReplaceInput{Ref: ref, Section: "Verification", Content: "New."}, nil)

	out := history(h, ref)
	first := out.Commits[len(out.Commits)-1].Commit

	msg := h.callErr("note_restore", NoteRestoreInput{Ref: ref, Commit: first})
	if !strings.Contains(msg, "note_version") {
		t.Errorf("error = %q, want it to name note_version", msg)
	}

	// Reading a different version does not unlock this one.
	h.call("note_version", NoteVersionInput{Ref: ref, Commit: out.Commits[0].Commit}, nil)
	if msg := h.callErr("note_restore", NoteRestoreInput{Ref: ref, Commit: first}); !strings.Contains(msg, "note_version") {
		t.Errorf("reading another version unlocked this one: %q", msg)
	}
}

func TestNoteRestorePutsTheOldTextBack(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_replace", NoteReplaceInput{
		Ref:     ref,
		Section: "Verification",
		Content: "New verification.",
	}, nil)

	before := history(h, ref)
	first := before.Commits[len(before.Commits)-1].Commit

	h.call("note_version", NoteVersionInput{Ref: ref, Commit: first}, nil)

	var out NoteRestoreOutput
	h.call("note_restore", NoteRestoreInput{Ref: ref, Commit: first}, &out)

	if !strings.Contains(out.Content, "Old verification.") {
		t.Errorf("restore did not bring the old text back:\n%s", out.Content)
	}
	if out.RestoredFrom != first {
		t.Errorf("restored_from = %q, want %q", out.RestoredFrom, first)
	}
	if out.Recreated || out.Forced {
		t.Errorf("restore of an existing replaceable note reported %+v", out)
	}

	// The note on disk, not merely the result, has to have changed.
	var current NoteOutput
	h.call("note_read", NoteReadInput{Ref: ref}, &current)
	if !strings.Contains(current.Content, "Old verification.") {
		t.Errorf("the note on disk was not restored:\n%s", current.Content)
	}

	// Restoring adds to the history rather than rewriting it.
	after := history(h, ref)
	if len(after.Commits) != len(before.Commits)+1 {
		t.Errorf("history went from %d commits to %d, want one more",
			len(before.Commits), len(after.Commits))
	}
	if !strings.Contains(after.Commits[0].Message, "note restore") {
		t.Errorf("newest message = %q", after.Commits[0].Message)
	}
}

// TestNoteRestoreRefusesAnAppendOnlyNote protects the record a session note
// exists to be: restoring one discards everything appended since.
func TestNoteRestoreRefusesAnAppendOnlyNote(t *testing.T) {
	h := newGitHarness(t)

	var session SessionStartOutput
	h.call("session_start", SessionStartInput{Title: "Versioning"}, &session)
	h.call("note_append", NoteAppendInput{Ref: session.Ref, Content: "What happened next."}, nil)

	out := history(h, session.Ref)
	first := out.Commits[len(out.Commits)-1].Commit

	msg := h.callErr("note_restore", NoteRestoreInput{Ref: session.Ref, Commit: first})
	if !strings.Contains(msg, "append-only") {
		t.Errorf("error = %q, want it to name the rule", msg)
	}

	// Forcing is allowed, and says so in the result.
	h.call("note_version", NoteVersionInput{Ref: session.Ref, Commit: first}, nil)

	var restored NoteRestoreOutput
	h.call("note_restore", NoteRestoreInput{Ref: session.Ref, Commit: first, Force: true}, &restored)

	if !restored.Forced {
		t.Error("a forced restore did not report itself as forced")
	}
	if strings.Contains(restored.Content, "What happened next.") {
		t.Error("the forced restore did not discard the later append")
	}
}

func TestNoteRestoreRecreatesADeletedNote(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_delete", NoteDeleteInput{Ref: ref}, nil)

	out := history(h, ref)
	if len(out.Commits) == 0 {
		t.Fatal("a deleted note has no history")
	}
	if !strings.Contains(out.Note, "not in the vault") {
		t.Errorf("history did not say the note is gone: %q", out.Note)
	}

	first := out.Commits[len(out.Commits)-1].Commit
	h.call("note_version", NoteVersionInput{Ref: ref, Commit: first}, nil)

	var restored NoteRestoreOutput
	h.call("note_restore", NoteRestoreInput{Ref: ref, Commit: first}, &restored)

	if !restored.Recreated {
		t.Error("restoring a deleted note did not report it as recreated")
	}

	var current NoteOutput
	h.call("note_read", NoteReadInput{Ref: ref}, &current)
	if !strings.Contains(current.Content, "Old verification.") {
		t.Errorf("the note was not brought back:\n%s", current.Content)
	}
}

// TestNoteRestoreReportsDeadLinks covers the case that would make an old
// version unrestorable if links were refused the way they are on a new write.
func TestNoteRestoreReportsDeadLinks(t *testing.T) {
	h := newGitHarness(t)

	target := project(h, "other", "# Other\n\nSomething to link at.")
	ref := project(h, "gandalf", "## Shape\n\nSee [["+target+"]] for the rest.")

	// Drop the link, then delete what it pointed at. Deletion refuses to
	// orphan a link, so this is the only order that works.
	h.call("note_replace", NoteReplaceInput{Ref: ref, Content: "## Shape\n\nNo links here."}, nil)
	h.call("note_delete", NoteDeleteInput{Ref: target}, nil)

	out := history(h, ref)
	first := out.Commits[len(out.Commits)-1].Commit
	h.call("note_version", NoteVersionInput{Ref: ref, Commit: first}, nil)

	var restored NoteRestoreOutput
	h.call("note_restore", NoteRestoreInput{Ref: ref, Commit: first}, &restored)

	var found bool
	for _, dead := range restored.DeadLinks {
		if dead == target {
			found = true
		}
	}
	if !found {
		t.Errorf("dead links = %v, want %s among them", restored.DeadLinks, target)
	}
	if !strings.Contains(restored.Content, target) {
		t.Errorf("the restore dropped the link instead of reporting it:\n%s", restored.Content)
	}
}

func TestNoteDiffBetweenVersions(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	h.call("note_replace", NoteReplaceInput{
		Ref:     ref,
		Section: "Verification",
		Content: "New verification.",
	}, nil)

	commits := history(h, ref).Commits

	var out NoteDiffOutput
	h.call("note_diff", NoteDiffInput{
		Ref:  ref,
		From: commits[len(commits)-1].Commit,
		To:   commits[0].Commit,
	}, &out)

	if out.Identical {
		t.Error("two different versions were reported identical")
	}
	for _, want := range []string{"-Old verification.", "+New verification."} {
		if !strings.Contains(out.Diff, want) {
			t.Errorf("diff does not contain %q:\n%s", want, out.Diff)
		}
	}

	// Omitting to compares against what the note says now.
	var current NoteDiffOutput
	h.call("note_diff", NoteDiffInput{Ref: ref, From: commits[0].Commit}, &current)
	if !current.Identical {
		t.Errorf("the newest commit differs from the working tree:\n%s", current.Diff)
	}
}

func TestHistoryNeedsGit(t *testing.T) {
	h := newHarness(t)

	msg := h.callErr("history", HistoryInput{})
	if !strings.Contains(msg, "not a git repository") {
		t.Errorf("error = %q, want it to say the vault has no history", msg)
	}
}

// TestNoteVersionRefusesACommitGitWouldMisread keeps the blob syntax from
// turning a commit parameter into a way to read arbitrary files.
func TestNoteVersionRefusesACommitGitWouldMisread(t *testing.T) {
	h := newGitHarness(t)
	ref := project(h, "gandalf", designBody)

	for _, commit := range []string{"HEAD:../../etc/passwd", "--output=/tmp/stolen"} {
		msg := h.callErr("note_version", NoteVersionInput{Ref: ref, Commit: commit})
		if !strings.Contains(msg, "not a usable commit") {
			t.Errorf("note_version(%q) = %q", commit, msg)
		}
	}
}

func TestHistoryToolsAreRegistered(t *testing.T) {
	h := newHarness(t)

	res, err := h.client.ListTools(h.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"history", "note_version", "note_diff", "note_restore"} {
		if !got[want] {
			t.Errorf("%s is not registered", want)
		}
	}
}
