package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// historyRepo is an initialised vault repository with nothing in it yet.
func historyRepo(t *testing.T) *Repo {
	t.Helper()
	requireGit(t)

	repo := Open(t.TempDir())
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return repo
}

// commitFile writes a note and commits it, the way a tool mutation does.
func commitFile(t *testing.T, repo *Repo, rel, content, message string) {
	t.Helper()
	commitFileBecause(t, repo, rel, content, message, "")
}

// commitFileBecause is commitFile with the reason a caller gave for the change.
func commitFileBecause(t *testing.T, repo *Repo, rel, content, message, reason string) {
	t.Helper()

	abs := filepath.Join(repo.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
	if err := repo.Commit(message, reason); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// commitMove relocates a note the way Gandalf would, by writing the new path
// and dropping the old one in the same commit.
func commitMove(t *testing.T, repo *Repo, from, to, content, message string) {
	t.Helper()

	abs := filepath.Join(repo.Root(), filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", to, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", to, err)
	}
	if err := os.Remove(filepath.Join(repo.Root(), filepath.FromSlash(from))); err != nil {
		t.Fatalf("remove %q: %v", from, err)
	}
	if err := repo.Commit(message, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// renamed builds the history a moved note has: added, modified, moved,
// modified again.
func renamed(t *testing.T) *Repo {
	t.Helper()

	repo := historyRepo(t)
	commitFile(t, repo, "Projects/A/Design.md", "one\n", "first")
	commitFile(t, repo, "Projects/A/Design.md", "two\n", "second")
	commitMove(t, repo, "Projects/A/Design.md", "Projects/B/Design.md", "two\n", "moved")
	commitFile(t, repo, "Projects/B/Design.md", "three\n", "third")
	return repo
}

// TestLogFollowsARename is the property the whole design rests on: a note's
// history has to survive the filing conventions changing, and each entry has
// to carry the path the note held at that commit rather than the one it holds
// now.
func TestLogFollowsARename(t *testing.T) {
	repo := renamed(t)

	entries, err := repo.Log("Projects/B/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(entries), entries)
	}

	want := []File{
		{Change: ChangeModified, Path: "Projects/B/Design.md"},
		{Change: ChangeRenamed, Path: "Projects/B/Design.md", From: "Projects/A/Design.md"},
		{Change: ChangeModified, Path: "Projects/A/Design.md"},
		{Change: ChangeAdded, Path: "Projects/A/Design.md"},
	}
	for i, w := range want {
		if len(entries[i].Files) != 1 {
			t.Fatalf("entry %d touched %d files, want 1", i, len(entries[i].Files))
		}
		if got := entries[i].Files[0]; got != w {
			t.Errorf("entry %d = %+v, want %+v", i, got, w)
		}
		if entries[i].Commit == "" || entries[i].Date == "" || entries[i].Message == "" {
			t.Errorf("entry %d is missing metadata: %+v", i, entries[i])
		}
	}
}

// noteText is what a Gandalf note looks like: frontmatter that is identical
// between notes bar a title, and a short body. It is the shape that makes git
// pair unrelated notes.
func noteText(title, body string) string {
	return "---\ntype: project\ncreated: 2026-08-18\nupdated: 2026-08-18\ntags: [gandalf]\nauthor: agent\n---\n\n# " +
		title + "\n\n" + body + "\n"
}

// TestLogDoesNotCrossIntoAnotherNotesHistory is a regression test. --follow
// turns copy detection on, and with notes this alike git paired two of them
// and reported one note's commits as the other's — which a restore would then
// have written into the wrong file.
func TestLogDoesNotCrossIntoAnotherNotesHistory(t *testing.T) {
	repo := historyRepo(t)

	commitFile(t, repo, "Projects/other/Design.md", noteText("Other — Design", "Something to link at."), "note new other")

	// One commit that adds the second note and touches the first, which is
	// what a write does when it updates the other note's backlinks.
	root := repo.Root()
	if err := os.MkdirAll(filepath.Join(root, "Projects", "gandalf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Projects", "gandalf", "Design.md"),
		[]byte(noteText("Gandalf — Design", "See the other one.")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Projects", "other", "Design.md"),
		[]byte(noteText("Other — Design", "Something to link at.\n\n## Backlinks\n\n- [[Projects/gandalf/Design]]")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit("note new gandalf", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	entries, err := repo.Log("Projects/gandalf/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 — the history reaches into another note: %+v", len(entries), entries)
	}
	if got := entries[0].Files[0]; got.Change != ChangeAdded || got.Path != "Projects/gandalf/Design.md" || got.From != "" {
		t.Errorf("entry = %+v, want the note added at its own path", got)
	}

	// The oldest commit of this note must hold this note's text.
	data, err := repo.Show(entries[0].Commit, entries[0].Files[0].Path)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if !strings.Contains(string(data), "See the other one.") {
		t.Errorf("content came from the wrong note:\n%s", data)
	}
}

func TestLogRespectsALimit(t *testing.T) {
	repo := renamed(t)

	entries, err := repo.Log("Projects/B/Design.md", 2)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestPathAtCrossesARename(t *testing.T) {
	repo := renamed(t)

	entries, err := repo.Log("Projects/B/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	// The oldest commit predates the move, so the note is only readable there
	// under the name it had then.
	first := entries[len(entries)-1].Commit

	at, ok, err := repo.PathAt("Projects/B/Design.md", first)
	if err != nil {
		t.Fatalf("PathAt: %v", err)
	}
	if !ok || at != "Projects/A/Design.md" {
		t.Fatalf("PathAt = %q, %v; want Projects/A/Design.md", at, ok)
	}

	data, err := repo.Show(first, at)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if string(data) != "one\n" {
		t.Errorf("content = %q, want %q", data, "one\n")
	}
}

// TestPathAtUsesTheLastChangeBeforeACommit is what lets a commit taken from a
// vault-wide history be used to read a note that commit did not touch.
func TestPathAtUsesTheLastChangeBeforeACommit(t *testing.T) {
	repo := historyRepo(t)

	commitFile(t, repo, "Projects/A/Design.md", "one\n", "design")
	commitFile(t, repo, "Sessions/2026/note.md", "session\n", "unrelated")

	head, err := repo.Resolve("HEAD")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	at, ok, err := repo.PathAt("Projects/A/Design.md", head)
	if err != nil {
		t.Fatalf("PathAt: %v", err)
	}
	if !ok || at != "Projects/A/Design.md" {
		t.Errorf("PathAt = %q, %v; want the note to exist at an unrelated commit", at, ok)
	}
}

func TestPathAtReportsADeletedNoteAsAbsent(t *testing.T) {
	repo := historyRepo(t)

	commitFile(t, repo, "Projects/A/Design.md", "one\n", "design")
	if err := os.Remove(filepath.Join(repo.Root(), filepath.FromSlash("Projects/A/Design.md"))); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := repo.Commit("deleted", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	head, err := repo.Resolve("HEAD")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if _, ok, err := repo.PathAt("Projects/A/Design.md", head); err != nil || ok {
		t.Errorf("PathAt = %v, %v; want the note reported as gone", ok, err)
	}

	// The version before the deletion is still reachable, which is what makes
	// a deleted note restorable.
	entries, err := repo.Log("Projects/A/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	at, ok, err := repo.PathAt("Projects/A/Design.md", entries[len(entries)-1].Commit)
	if err != nil || !ok || at != "Projects/A/Design.md" {
		t.Errorf("PathAt before the deletion = %q, %v, %v", at, ok, err)
	}
}

// TestShowReturnsContentVerbatim matters because a note's trailing newline is
// content: trimming it would make every restored note differ from the one
// that was committed.
func TestShowReturnsContentVerbatim(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "note.md", "body\n\n", "add")

	data, err := repo.Show("HEAD", "note.md")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if string(data) != "body\n\n" {
		t.Errorf("content = %q, want %q", data, "body\n\n")
	}
}

func TestDiffBetweenCommits(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "note.md", "old\n", "first")
	commitFile(t, repo, "note.md", "new\n", "second")

	entries, err := repo.Log("note.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	diff, err := repo.Diff(entries[1].Commit, "note.md", entries[0].Commit, "note.md")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, want := range []string{"-old", "+new"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not contain %q:\n%s", want, diff)
		}
	}
}

func TestDiffAgainstTheWorkingTree(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "note.md", "committed\n", "first")

	abs := filepath.Join(repo.Root(), "note.md")
	if err := os.WriteFile(abs, []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	diff, err := repo.Diff("HEAD", "note.md", "", "note.md")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "+edited by hand") {
		t.Errorf("diff does not show the uncommitted edit:\n%s", diff)
	}
}

// TestCheckRevRefusesWhatGitWouldMisread covers the two hazards: a value that
// arrives as an option, and a colon turning a commit into an arbitrary file.
func TestCheckRevRefusesWhatGitWouldMisread(t *testing.T) {
	for _, rev := range []string{"", "   ", "-n1", "--output=/tmp/stolen", "HEAD:../../etc/passwd", "HEAD:Projects/A/Design.md"} {
		if got, err := checkRev(rev); err == nil {
			t.Errorf("checkRev(%q) allowed it as %q", rev, got)
		}
	}

	got, err := checkRev("  HEAD~2  ")
	if err != nil {
		t.Fatalf("checkRev: %v", err)
	}
	if got != "HEAD~2" {
		t.Errorf("checkRev = %q, want HEAD~2", got)
	}
}

func TestResolveRefusesAnUnknownCommit(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "note.md", "body\n", "add")

	if _, err := repo.Resolve("0123456789abcdef0123456789abcdef01234567"); err == nil {
		t.Error("Resolve accepted a commit that is not in the history")
	}
}

// TestLogOnARepositoryWithNoCommits is the state a vault is in between init
// and its first note, which is not an error.
func TestLogOnARepositoryWithNoCommits(t *testing.T) {
	requireGit(t)

	repo := Open(t.TempDir())
	if _, err := repo.run("init"); err != nil {
		t.Fatalf("init: %v", err)
	}

	entries, err := repo.LogAll(10)
	if err != nil {
		t.Fatalf("LogAll: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want none", len(entries))
	}
}

func TestHistoryNeedsAnEnabledRepository(t *testing.T) {
	requireGit(t)

	if err := Open(t.TempDir()).Available(); err != ErrNoGit {
		t.Errorf("a directory that is not a repository = %v, want ErrNoGit", err)
	}

	repo := historyRepo(t)
	repo.Disable()
	if err := repo.Available(); err != ErrGitDisabled {
		t.Errorf("a disabled repository = %v, want ErrGitDisabled", err)
	}
}

func TestLogAllReportsEveryPathACommitTouched(t *testing.T) {
	repo := historyRepo(t)

	root := repo.Root()
	for _, rel := range []string{"Projects/A/Design.md", "Projects/A/Todo.md"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("body\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Commit("two notes at once", ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	entries, err := repo.LogAll(1)
	if err != nil {
		t.Fatalf("LogAll: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if len(entries[0].Files) != 2 {
		t.Errorf("commit touched %d files, want 2: %+v", len(entries[0].Files), entries[0].Files)
	}
}

func TestCommitRecordsTheReasonAndLogReadsItBack(t *testing.T) {
	repo := historyRepo(t)

	const reason = "record the decision to keep tool docs in the binary"
	commitFileBecause(t, repo, "Projects/A/Design.md", "one\n", "gandalf: note new project:A:design", reason)

	entries, err := repo.Log("Projects/A/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "gandalf: note new project:A:design" {
		t.Errorf("message = %q", entries[0].Message)
	}
	if entries[0].Reason != reason {
		t.Errorf("reason = %q, want %q", entries[0].Reason, reason)
	}
	if len(entries[0].Files) != 1 {
		t.Errorf("commit touched %d files, want 1: %+v", len(entries[0].Files), entries[0].Files)
	}

	// The subject stays what it was, so a log read by a human is unchanged by
	// the reason living in the body.
	subject, err := repo.run("log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if subject != "gandalf: note new project:A:design" {
		t.Errorf("subject = %q", subject)
	}
}

// TestCommitWithoutAReasonHasNoBody covers commits Gandalf makes on its own
// behalf, and every commit made before reasons were recorded.
func TestCommitWithoutAReasonHasNoBody(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "Projects/A/Design.md", "one\n", "gandalf: initialise vault")

	entries, err := repo.Log("Projects/A/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Reason != "" {
		t.Errorf("reason = %q, want empty", entries[0].Reason)
	}
	if len(entries[0].Files) != 1 {
		t.Errorf("commit touched %d files, want 1: %+v", len(entries[0].Files), entries[0].Files)
	}
}

// TestAMultiLineReasonStaysOnOneLine is what keeps the log parseable: a body
// spanning several lines is indistinguishable from the file list below it.
func TestAMultiLineReasonStaysOnOneLine(t *testing.T) {
	repo := historyRepo(t)

	commitFileBecause(t, repo, "Projects/A/Design.md", "one\n", "gandalf: note new project:A:design",
		"first line\nsecond line\n\tthird\tline")

	entries, err := repo.Log("Projects/A/Design.md", 0)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if want := "first line second line third line"; entries[0].Reason != want {
		t.Errorf("reason = %q, want %q", entries[0].Reason, want)
	}
	if len(entries[0].Files) != 1 {
		t.Errorf("commit touched %d files, want 1: %+v", len(entries[0].Files), entries[0].Files)
	}
}

// TestABodyThatLooksLikeAFileListIsNotOne guards the parser against a commit
// made outside Gandalf, where the body can be anything at all.
func TestABodyThatLooksLikeAFileListIsNotOne(t *testing.T) {
	repo := historyRepo(t)
	commitFile(t, repo, "Projects/A/Design.md", "one\n", "first")

	abs := filepath.Join(repo.Root(), filepath.FromSlash("Projects/A/Design.md"))
	if err := os.WriteFile(abs, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.run("add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A body whose lines are tab-separated, as a hand-written commit may well
	// be. Read as name-status it would claim the commit touched a file called
	// "of the old note".
	if _, err := repo.run("commit", "-m", "by hand", "-m", "M\tsome notes\nD\tof the old note"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	entries, err := repo.Log("Projects/A/Design.md", 1)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	for _, file := range entries[0].Files {
		if file.Path != "Projects/A/Design.md" {
			t.Errorf("commit reports touching %q, which is a line of its message", file.Path)
		}
	}
}

func TestOneLineBoundsALongReason(t *testing.T) {
	long := strings.Repeat("ünïcode ", 200)

	got := OneLine(long)
	if runes := []rune(got); len(runes) > maxReason+3 {
		t.Errorf("reason is %d runes, want at most %d", len(runes), maxReason+3)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated reason does not say so: %q", got[len(got)-10:])
	}
	if !utf8.ValidString(got) {
		t.Error("truncation produced invalid UTF-8")
	}
}
