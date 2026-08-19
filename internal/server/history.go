package server

import (
	"context"
	"fmt"
	"path"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/git"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// Gandalf commits the vault on every mutation, which makes git the record of
// what a note used to say. These four tools are what let a model read that
// record instead of leaving the user to open a terminal: history says what
// changed, note_version says what the note said then, note_diff says what
// moved between two points, and note_restore puts an old version back.
//
// Restoring is a new commit, never a history rewrite. Nothing here can lose a
// version — the worst it can do is add one.

// How many commits a history returns. The default covers a working session;
// the ceiling keeps a vault-wide history from flooding a context window.
const (
	historyLimit = 20
	historyMax   = 200
)

// HistoryInput selects a note's history, or the whole vault's.
type HistoryInput struct {
	Ref   string `json:"ref,omitempty" jsonschema:"the note whose history to list; omit for every change to the vault"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum commits to return; defaults to 20"`
}

// ChangedNote is one note a commit touched.
type ChangedNote struct {
	Ref    string `json:"ref"`
	Change string `json:"change"`

	// From is where the note was filed before, set only for a rename.
	From string `json:"from,omitempty"`
}

// CommitOutput is one commit in a history.
type CommitOutput struct {
	// Commit is the abbreviated hash. Pass it back to note_version,
	// note_diff, or note_restore.
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Message string `json:"message"`

	// Reason is why the change was made, as given by whoever made it. It is
	// what the message cannot say: the message reports that a note was
	// replaced, and this reports what the replacement was for.
	Reason string `json:"reason,omitempty"`

	// Change and From describe what the commit did to the note the history
	// was scoped to, and are empty in a vault-wide history.
	Change string `json:"change,omitempty"`
	From   string `json:"from,omitempty"`

	// Notes is what the commit touched, and is set only in a vault-wide
	// history.
	Notes []ChangedNote `json:"notes,omitempty"`
}

// HistoryOutput is what changed, newest first.
type HistoryOutput struct {
	Ref     string         `json:"ref,omitempty"`
	Commits []CommitOutput `json:"commits"`
	Note    string         `json:"note,omitempty"`
}

// history lists the commits behind a note, or behind the whole vault.
//
// One tool rather than two, following lint: the same question scoped two ways
// is one thing to learn rather than two.
func (s *Server) history(ctx context.Context, _ *sdk.CallToolRequest, in HistoryInput) (*sdk.CallToolResult, HistoryOutput, error) {
	repo, err := s.repo()
	if err != nil {
		return nil, HistoryOutput{}, err
	}

	limit := in.Limit
	switch {
	case limit <= 0:
		limit = historyLimit
	case limit > historyMax:
		limit = historyMax
	}

	if strings.TrimSpace(in.Ref) == "" {
		entries, err := repo.LogAll(limit)
		if err != nil {
			return nil, HistoryOutput{}, err
		}
		return nil, HistoryOutput{Commits: s.vaultCommits(entries)}, nil
	}

	ref, notePath, err := s.resolve(in.Ref)
	if err != nil {
		return nil, HistoryOutput{}, err
	}

	entries, err := repo.Log(notePath, limit)
	if err != nil {
		return nil, HistoryOutput{}, err
	}

	out := HistoryOutput{Ref: ref.String(), Commits: s.noteCommits(entries)}
	switch {
	case len(entries) == 0:
		out.Note = "no commits touch this note; it may never have been committed"
	case !s.vault.Exists(notePath):
		out.Note = "this note is not in the vault now; note_restore will bring it back"
	}
	return nil, out, nil
}

// noteCommits renders a history scoped to one note, where every commit
// touched exactly the note asked about.
func (s *Server) noteCommits(entries []git.Entry) []CommitOutput {
	out := make([]CommitOutput, 0, len(entries))

	for _, entry := range entries {
		commit := CommitOutput{Commit: entry.Commit, Date: entry.Date, Message: entry.Message, Reason: entry.Reason}
		if len(entry.Files) > 0 {
			file := entry.Files[0]
			commit.Change = string(file.Change)
			if file.From != "" {
				commit.From = s.canonical(file.From).String()
			}
		}
		out = append(out, commit)
	}

	return out
}

// vaultCommits renders a history over the whole vault.
//
// Only notes are reported. A commit that changed nothing but configuration —
// a remote being set, categories being declared — still appears, because the
// commit is part of the record, but it carries no notes.
func (s *Server) vaultCommits(entries []git.Entry) []CommitOutput {
	out := make([]CommitOutput, 0, len(entries))

	for _, entry := range entries {
		commit := CommitOutput{Commit: entry.Commit, Date: entry.Date, Message: entry.Message, Reason: entry.Reason}
		for _, file := range entry.Files {
			if !strings.EqualFold(path.Ext(file.Path), ".md") {
				continue
			}
			changed := ChangedNote{Ref: s.canonical(file.Path).String(), Change: string(file.Change)}
			if file.From != "" {
				changed.From = s.canonical(file.From).String()
			}
			commit.Notes = append(commit.Notes, changed)
		}
		out = append(out, commit)
	}

	return out
}

// NoteVersionInput selects a past version of a note.
type NoteVersionInput struct {
	Ref    string `json:"ref" jsonschema:"the note's ref, as returned by another tool"`
	Commit string `json:"commit" jsonschema:"the commit to read the note at, as returned by history"`
}

// NoteVersionOutput is what the note said at that commit.
type NoteVersionOutput struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`

	Title   string   `json:"title,omitempty"`
	Type    string   `json:"type,omitempty"`
	Created string   `json:"created,omitempty"`
	Updated string   `json:"updated,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Related []string `json:"related,omitempty"`
	Status  string   `json:"status,omitempty"`
	Content string   `json:"content"`

	// Note reports anything about this version worth knowing before restoring
	// it, such as frontmatter that no longer parses.
	Note string `json:"note,omitempty"`
}

// noteVersion reads a note as it stood at a commit.
//
// It is also the gate on restoring: a version read here is recorded against
// the commit it came from, and note_restore refuses a version nobody has
// looked at.
func (s *Server) noteVersion(ctx context.Context, _ *sdk.CallToolRequest, in NoteVersionInput) (*sdk.CallToolResult, NoteVersionOutput, error) {
	ref, commit, data, err := s.versionAt(in.Ref, in.Commit)
	if err != nil {
		return nil, NoteVersionOutput{}, err
	}

	out := NoteVersionOutput{Ref: ref.String(), Commit: commit}

	// A version whose frontmatter no longer parses is still worth reading —
	// it may be exactly the damage the caller is investigating — so the text
	// is returned either way and only the metadata is missing.
	note, err := vault.ParseNote(ref.String(), data)
	if err != nil {
		out.Content = string(data)
		out.Note = fmt.Sprintf("this version cannot be parsed as a note (%v); the text is returned verbatim "+
			"and note_restore will refuse it", err)
		s.markVersionRead(ref, commit)
		return nil, out, nil
	}

	out.Title = note.Title()
	out.Type = string(note.FM.Type)
	out.Created = note.FM.Created.String()
	out.Updated = note.FM.Updated.String()
	out.Tags = note.FM.Tags
	out.Related = s.refsFor(note.FM.Related)
	out.Status = string(note.FM.Status)
	out.Content = s.toRefs(note.Body)
	if len(note.Issues) > 0 {
		out.Note = fmt.Sprintf("this version has %d frontmatter issue(s) and note_restore will refuse it: %s",
			len(note.Issues), note.Issues[0].Message)
	}

	s.markVersionRead(ref, commit)
	return nil, out, nil
}

// NoteDiffInput selects two versions of a note to compare.
type NoteDiffInput struct {
	Ref  string `json:"ref" jsonschema:"the note's ref, as returned by another tool"`
	From string `json:"from" jsonschema:"the older commit"`
	To   string `json:"to,omitempty" jsonschema:"the newer commit; omit to compare against the note as it stands now"`
}

// NoteDiffOutput is a unified diff.
type NoteDiffOutput struct {
	Ref  string `json:"ref"`
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	Diff string `json:"diff"`

	// Identical says the two versions are the same. An empty diff would
	// otherwise be indistinguishable from a diff that failed to be taken.
	Identical bool `json:"identical,omitempty"`
}

// noteDiff reports what changed in a note between two commits, or between a
// commit and what the note says now.
//
// A diff costs a fraction of what two full versions cost, which is the whole
// reason it is a tool rather than something the caller assembles.
func (s *Server) noteDiff(ctx context.Context, _ *sdk.CallToolRequest, in NoteDiffInput) (*sdk.CallToolResult, NoteDiffOutput, error) {
	repo, err := s.repo()
	if err != nil {
		return nil, NoteDiffOutput{}, err
	}

	ref, notePath, err := s.resolve(in.Ref)
	if err != nil {
		return nil, NoteDiffOutput{}, err
	}

	from, fromPath, err := s.pathAt(repo, ref, notePath, in.From)
	if err != nil {
		return nil, NoteDiffOutput{}, err
	}

	to, toPath := "", notePath
	if strings.TrimSpace(in.To) != "" {
		to, toPath, err = s.pathAt(repo, ref, notePath, in.To)
		if err != nil {
			return nil, NoteDiffOutput{}, err
		}
	} else if !s.vault.Exists(notePath) {
		return nil, NoteDiffOutput{}, fmt.Errorf(
			"%s is not in the vault now, so there is nothing to compare against; give a to commit", ref)
	}

	diff, err := repo.Diff(from, fromPath, to, toPath)
	if err != nil {
		return nil, NoteDiffOutput{}, err
	}

	return nil, NoteDiffOutput{
		Ref:       ref.String(),
		From:      from,
		To:        to,
		Diff:      diff,
		Identical: strings.TrimSpace(diff) == "",
	}, nil
}

// NoteRestoreInput selects the version to put back.
type NoteRestoreInput struct {
	Ref    string `json:"ref" jsonschema:"the note's ref, as returned by another tool"`
	Commit string `json:"commit" jsonschema:"the commit to restore the note from, as returned by history"`

	Reason string `json:"reason" jsonschema:"why this version is being put back, in a few words; it becomes the commit message"`

	Force bool `json:"force,omitempty" jsonschema:"restore a note that is normally append-only, such as a session or a decisions log. Doing so discards whatever was appended since, so use it to undo damage rather than to revise a record"`
}

// NoteRestoreOutput is the note as it now stands, having been put back.
type NoteRestoreOutput struct {
	NoteOutput

	// RestoredFrom is the commit the content came from.
	RestoredFrom string `json:"restored_from"`

	// Recreated says the note was absent and has been brought back.
	Recreated bool `json:"recreated,omitempty"`

	// DeadLinks are links in the restored text pointing at notes that no
	// longer exist. They do not stop the restore; a version that could be
	// blocked by an unrelated later deletion would not be much of an undo.
	DeadLinks []string `json:"dead_links,omitempty"`

	// Forced reports that an append-only note was rewritten, so the act shows
	// up in the transcript and not only in the git log.
	Forced bool `json:"forced,omitempty"`
}

// noteRestore writes a past version of a note back as the current one.
func (s *Server) noteRestore(ctx context.Context, _ *sdk.CallToolRequest, in NoteRestoreInput) (*sdk.CallToolResult, NoteRestoreOutput, error) {
	if err := checkReason(in.Reason); err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	repo, err := s.repo()
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	ref, notePath, err := s.writable(in.Ref)
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	commit, err := repo.Resolve(in.Commit)
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	// Restoring an append-only note discards everything appended since, which
	// is the loss its mutability exists to prevent. Protected by default and
	// not sealed, on the same reasoning as note_replace: the restore itself
	// commits, so a forced one is a revert away.
	forced := false
	if m := s.mutability(notePath); m != category.Replaceable {
		if !in.Force {
			return nil, NoteRestoreOutput{}, fmt.Errorf(
				"%s is %s: restoring it would discard everything added since %s. Pass force if that is "+
					"what you mean, or read the version with note_version and add what is missing instead",
				ref, m, commit)
		}
		forced = true
	}

	if !s.hasReadVersion(ref, commit) {
		return nil, NoteRestoreOutput{}, fmt.Errorf(
			"read %s at %s with note_version before restoring it", ref, commit)
	}

	at, ok, err := repo.PathAt(notePath, commit)
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}
	if !ok {
		return nil, NoteRestoreOutput{}, fmt.Errorf("%s did not exist at %s", ref, commit)
	}

	data, err := repo.Show(commit, at)
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	// The note is written where its ref says it lives now, not where it lived
	// then. A ref addresses what a note is; restoring recovers what it said,
	// and undoing a move is a different operation nobody asked for.
	note, err := vault.ParseNote(notePath, data)
	if err != nil {
		return nil, NoteRestoreOutput{}, fmt.Errorf(
			"the version of %s at %s cannot be parsed as a note: %w", ref, commit, err)
	}
	if len(note.Issues) > 0 {
		return nil, NoteRestoreOutput{}, fmt.Errorf(
			"the version of %s at %s has frontmatter Gandalf cannot represent, so restoring it would drop "+
				"fields: %s", ref, commit, note.Issues[0].Message)
	}

	note.Touch(schema.Today())

	// The maintained block travelled with the old text and describes what
	// linked here then. Rebuild it from what links here now, or the restore
	// resurrects a stale block that the next write would have to correct.
	backlinks, err := s.currentBacklinks(notePath)
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}
	note.SetBacklinks(backlinks)

	dead, err := s.vault.Unresolved(note.OutgoingLinks())
	if err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	recreated := !s.vault.Exists(notePath)
	if err := s.write(note); err != nil {
		return nil, NoteRestoreOutput{}, err
	}

	message := fmt.Sprintf("gandalf: note restore %s from %s", ref, commit)
	if forced {
		message += " (forced, append-only)"
	}
	s.record(message, in.Reason)

	return nil, NoteRestoreOutput{
		NoteOutput:   s.describe(ref, note),
		RestoredFrom: commit,
		Recreated:    recreated,
		DeadLinks:    s.refsFor(dead),
		Forced:       forced,
	}, nil
}

// repo returns the vault's git repository, or the reason there is no history
// to read.
func (s *Server) repo() (*git.Repo, error) {
	if s.git == nil {
		return nil, fmt.Errorf(
			"%w; run `gandalf init` in the vault, or restart without -no-git", git.ErrNoGit)
	}
	if err := s.git.Available(); err != nil {
		return nil, err
	}
	return s.git, nil
}

// versionAt resolves a ref and a commit to the note's bytes at that commit.
func (s *Server) versionAt(rawRef, rawCommit string) (vault.Ref, string, []byte, error) {
	repo, err := s.repo()
	if err != nil {
		return vault.Ref{}, "", nil, err
	}

	ref, notePath, err := s.resolve(rawRef)
	if err != nil {
		return vault.Ref{}, "", nil, err
	}

	commit, at, err := s.pathAt(repo, ref, notePath, rawCommit)
	if err != nil {
		return vault.Ref{}, "", nil, err
	}

	data, err := repo.Show(commit, at)
	if err != nil {
		return vault.Ref{}, "", nil, err
	}
	return ref, commit, data, nil
}

// pathAt resolves a revision and finds where the note lived at it.
func (s *Server) pathAt(repo *git.Repo, ref vault.Ref, notePath, rawCommit string) (string, string, error) {
	commit, err := repo.Resolve(rawCommit)
	if err != nil {
		return "", "", err
	}

	at, ok, err := repo.PathAt(notePath, commit)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("%s did not exist at %s", ref, commit)
	}
	return commit, at, nil
}

// currentBacklinks returns the maintained block a note should carry, given
// what links to it now.
func (s *Server) currentBacklinks(notePath string) ([]string, error) {
	referrers, err := s.vault.Referrers(notePath)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(referrers))
	for _, source := range referrers {
		out = append(out, strings.TrimSuffix(source, path.Ext(source)))
	}
	return out, nil
}
