package git

import (
	"fmt"
	"strings"
)

// maxReason bounds how much of a reason is kept. A commit body is a note about
// a change rather than a place to restate the change, and an unbounded one
// would be read back into a context window by every later history call.
const maxReason = 500

// Commit stages every change in the vault and creates a commit when there is
// something to record. An empty working tree is not an error.
//
// The message says which tool changed which note, and is generated. The reason
// says why, comes from whoever asked for the change, and may be empty — a
// commit Gandalf makes on its own behalf has no why beyond its message.
func (r *Repo) Commit(message, reason string) error {
	if r == nil || r.disabled {
		return nil
	}
	if !r.IsRepo() {
		return nil
	}

	cfg, err := LoadConfig(r.root)
	if err != nil {
		return err
	}
	if !cfg.IsEnabled() {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.commitLocked(message, reason)
}

// commitLocked assumes r.mu is held.
func (r *Repo) commitLocked(message, reason string) error {
	if _, err := r.run("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	status, err := r.run("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if status == "" {
		return nil
	}

	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "gandalf: update vault"
	}

	args := []string{"commit", "-m", msg}
	if reason := OneLine(reason); reason != "" {
		// A second -m becomes the body, separated from the subject by a blank
		// line, which is what makes the subject stay short and readable while
		// the reason is still there to be read back.
		args = append(args, "-m", reason)
	}

	if _, err := r.run(args...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// OneLine reduces a reason to the single line the log format can carry back.
//
// History is parsed line by line, so a body spanning several lines would be
// indistinguishable from the list of files a commit touched. Collapsing here
// rather than refusing keeps a caller that wrote a paragraph from losing its
// change over formatting.
func OneLine(reason string) string {
	fields := strings.FieldsFunc(reason, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == '\v' || r == '\f'
	})

	joined := strings.TrimSpace(strings.Join(fields, " "))

	// Counted in runes rather than bytes: a note title can be in any script,
	// and a reason cut mid-character would put invalid UTF-8 in the history.
	if runes := []rune(joined); len(runes) > maxReason {
		joined = strings.TrimSpace(string(runes[:maxReason])) + "..."
	}
	return joined
}
