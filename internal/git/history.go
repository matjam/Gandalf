package git

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Reasons a vault has no history to answer questions about. They are separate
// errors because the remedies differ: one is "run init", the other is "drop
// -no-git".
var (
	ErrNoGit       = errors.New("this vault is not a git repository, so it has no history")
	ErrGitDisabled = errors.New("git is disabled for this process, so history is not available")
)

// Separators for the log format. Both are control characters that cannot occur
// in a path and will not occur in a commit subject, so a message containing
// tabs, pipes, or newlines cannot be mistaken for structure.
const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"
)

// Change is what a commit did to one file.
type Change string

const (
	ChangeAdded    Change = "added"
	ChangeModified Change = "modified"
	ChangeDeleted  Change = "deleted"
	ChangeRenamed  Change = "renamed"
	ChangeCopied   Change = "copied"
)

// File is one path a commit touched.
type File struct {
	Change Change

	// Path is where the file lived at that commit, which is not where it
	// lives now once anything has been renamed.
	Path string

	// From is the previous path, set only for a rename or a copy.
	From string
}

// Entry is one commit.
type Entry struct {
	// Commit is the abbreviated hash. It is what callers pass back, and git
	// accepts it wherever a revision is wanted.
	Commit  string
	Date    string
	Message string
	Files   []File
}

// Available reports whether this repo can answer questions about history.
func (r *Repo) Available() error {
	switch {
	case r == nil:
		return ErrNoGit
	case r.disabled:
		return ErrGitDisabled
	case !r.IsRepo():
		return ErrNoGit
	}
	return nil
}

// Log returns the commits that touched one note, newest first, following the
// file across renames so history does not stop at a move. A limit of zero
// returns every commit.
func (r *Repo) Log(notePath string, limit int) ([]Entry, error) {
	return r.log(limit, "", notePath)
}

// LogAll returns the commits that touched the vault, newest first, each with
// the paths it changed.
func (r *Repo) LogAll(limit int) ([]Entry, error) {
	return r.log(limit, "", "")
}

// log runs git log and parses it. An empty notePath logs the whole vault; a
// non-empty one follows that file. An empty from starts at HEAD.
func (r *Repo) log(limit int, from, notePath string) ([]Entry, error) {
	if err := r.Available(); err != nil {
		return nil, err
	}
	if !r.hasCommits() {
		// A repository with no commits is not an error: it is a vault nothing
		// has been written to yet.
		return nil, nil
	}

	// core.quotePath=false keeps non-ASCII paths verbatim. Note titles become
	// filenames, so a note about naïveté would otherwise arrive C-quoted and
	// fail to match anything.
	// A high similarity threshold, because notes resemble each other by
	// construction: same frontmatter fields, same heading shape, often only a
	// few lines of prose. At git's default of 50% two unrelated notes pair
	// readily, and a note's history would then be somebody else's.
	args := []string{"-c", "core.quotePath=false", "log", "--name-status", "-M90%",
		"--pretty=format:" + recordSep + "%h" + fieldSep + "%aI" + fieldSep + "%s"}
	if notePath != "" {
		args = append(args, "--follow")
	}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	if from != "" {
		args = append(args, from)
	}
	if notePath != "" {
		args = append(args, "--", notePath)
	}

	out, err := r.run(args...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	entries := parseLog(out)
	if notePath != "" {
		entries = stopAtCopy(entries)
	}
	return entries, nil
}

// stopAtCopy truncates a followed history where git paired the note with a
// file that still existed.
//
// --follow turns copy detection on, so the search for where a note came from
// is not limited to files the commit deleted. Given how alike notes are, it
// will pair two unrelated ones and then carry on reporting the wrong note's
// commits as this note's — and a restore taken from one of them would write
// the wrong note's text. A move deletes the old path and so arrives as a
// rename; a copy means the source was still there, which means it was not a
// move, which means the note's own history starts here.
func stopAtCopy(entries []Entry) []Entry {
	for i, entry := range entries {
		if len(entry.Files) == 0 || entry.Files[0].Change != ChangeCopied {
			continue
		}
		entries[i].Files[0] = File{Change: ChangeAdded, Path: entry.Files[0].Path}
		return entries[:i+1]
	}
	return entries
}

// parseLog reads the record-separated log format back into entries.
func parseLog(out string) []Entry {
	var entries []Entry

	for _, record := range strings.Split(out, recordSep) {
		record = strings.Trim(record, "\n")
		if record == "" {
			continue
		}

		lines := strings.Split(record, "\n")
		fields := strings.SplitN(lines[0], fieldSep, 3)
		if len(fields) < 3 {
			continue
		}

		entry := Entry{Commit: fields[0], Date: fields[1], Message: fields[2]}
		for _, line := range lines[1:] {
			if file, ok := parseNameStatus(line); ok {
				entry.Files = append(entry.Files, file)
			}
		}
		entries = append(entries, entry)
	}

	return entries
}

// parseNameStatus reads one --name-status line. Rename and copy lines carry
// two paths, old then new; everything else carries one.
func parseNameStatus(line string) (File, bool) {
	parts := strings.Split(strings.TrimRight(line, "\r"), "\t")
	if len(parts) < 2 || parts[0] == "" {
		return File{}, false
	}

	switch parts[0][0] {
	case 'A':
		return File{Change: ChangeAdded, Path: parts[1]}, true
	case 'D':
		return File{Change: ChangeDeleted, Path: parts[1]}, true
	case 'R', 'C':
		if len(parts) < 3 {
			return File{}, false
		}
		change := ChangeRenamed
		if parts[0][0] == 'C' {
			change = ChangeCopied
		}
		return File{Change: change, Path: parts[2], From: parts[1]}, true
	default:
		return File{Change: ChangeModified, Path: parts[1]}, true
	}
}

// PathAt returns the path a note held at a commit, and whether the note
// existed there at all.
//
// The search starts from where the note lives now and walks its history back,
// so a note that has since been renamed is found under the name it had then.
// A commit that did not itself touch the note is answered from the most recent
// change before it, which is what lets a commit taken from a vault-wide
// history be used to read any note.
func (r *Repo) PathAt(currentPath, commit string) (string, bool, error) {
	commit, err := checkRev(commit)
	if err != nil {
		return "", false, err
	}

	entries, err := r.Log(currentPath, 0)
	if err != nil {
		return "", false, err
	}

	for _, entry := range entries {
		if len(entry.Files) == 0 || !r.isAncestor(entry.Commit, commit) {
			continue
		}
		file := entry.Files[0]
		if file.Change == ChangeDeleted {
			// The last thing that happened to the note at or before this
			// commit was its removal, so there is nothing to read.
			return "", false, nil
		}
		return file.Path, true, nil
	}

	return "", false, nil
}

// isAncestor reports whether one commit is an ancestor of another, or is the
// same commit.
func (r *Repo) isAncestor(a, b string) bool {
	_, err := r.run("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// Show returns a file's bytes at a commit.
func (r *Repo) Show(commit, notePath string) ([]byte, error) {
	if err := r.Available(); err != nil {
		return nil, err
	}
	commit, err := checkRev(commit)
	if err != nil {
		return nil, err
	}

	out, err := r.runRaw("show", commit+":"+notePath)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", notePath, commit, err)
	}
	return out, nil
}

// Diff returns a unified diff of one note between two commits. An empty
// toCommit diffs against the working tree, which is how a note is compared
// with what it says now.
func (r *Repo) Diff(fromCommit, fromPath, toCommit, toPath string) (string, error) {
	if err := r.Available(); err != nil {
		return "", err
	}
	fromCommit, err := checkRev(fromCommit)
	if err != nil {
		return "", err
	}

	args := []string{"-c", "core.quotePath=false", "diff", fromCommit + ":" + fromPath}
	if toCommit == "" {
		// A leading ./ keeps a path that begins with a dash from arriving as
		// an option. Paths come from the vault rather than from a caller, but
		// note titles become filenames and a title can begin with anything.
		args = append(args, "./"+toPath)
	} else {
		to, err := checkRev(toCommit)
		if err != nil {
			return "", err
		}
		args = append(args, to+":"+toPath)
	}

	out, err := r.run(args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// Resolve returns the abbreviated hash a revision names, and refuses one that
// names nothing. Callers key on what it returns, so "HEAD~2" and a full hash
// for the same commit come back as the same string.
func (r *Repo) Resolve(rev string) (string, error) {
	if err := r.Available(); err != nil {
		return "", err
	}
	rev, err := checkRev(rev)
	if err != nil {
		return "", err
	}

	out, err := r.run("rev-parse", "--verify", "--quiet", "--short", rev+"^{commit}")
	if err != nil || out == "" {
		return "", fmt.Errorf("no commit %q in this vault's history", rev)
	}
	return out, nil
}

// hasCommits reports whether anything has been committed yet.
func (r *Repo) hasCommits() bool {
	_, err := r.run("rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// checkRev refuses a revision git would read as something other than a commit,
// and returns the trimmed form.
//
// No shell is involved, so the hazard is not injection. It is a value starting
// with a dash arriving as an option, and a colon turning "show this commit"
// into "show this arbitrary file", since the blob syntax is rev:path.
func checkRev(rev string) (string, error) {
	rev = strings.TrimSpace(rev)
	switch {
	case rev == "":
		return "", fmt.Errorf("a commit is required")
	case strings.HasPrefix(rev, "-"), strings.ContainsAny(rev, ":\x00\n"):
		return "", fmt.Errorf("%q is not a usable commit", rev)
	}
	return rev, nil
}
