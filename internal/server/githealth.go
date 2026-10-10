package server

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/matjam/gandalf/internal/git"
)

// GitNotice is embedded in the output of every tool that changes the vault,
// and in boot's. It is how a git problem reaches the model that caused the
// write, in the result it is already reading.
//
// The note write itself still succeeds when git is broken: the text landed on
// disk, and refusing it would trade a missing commit for a lost note. What
// must not happen is the quiet part. A tool that returned a bare success while
// nothing was being committed or synced is how a vault went a month without a
// commit and nobody, human or model, was told. An empty field means the change
// was committed, or the vault is deliberately not under git.
type GitNotice struct {
	GitWarning string `json:"git_warning,omitempty" jsonschema:"present when git is not working for this vault: the repository is broken or syncing is failing, so changes are saved but not committed or synced. Tell the user"`
}

// commit commits the vault after a successful mutation, attributed to author
// when there is one, and returns the warning to put in the tool's result, or ""
// when the change was committed. A commit failure is logged and surfaced but
// never returned as an error: the note already landed, and failing the call
// would invite the model to write it again.
//
// Each Core has its own repository and so its own health: a broken team vault
// warns the writer who changed that vault, and says nothing about the
// personal one.
//
// The message is generated and says which tool changed which note; the reason
// is the caller's and says why. Both are needed to make the history readable:
// the message alone gives a log of mechanical operations, and the reason alone
// loses the distinction between an append and a replacement, which is the
// distinction between adding to a note and overwriting part of it.
//
// A vault served with -no-git has no repository to be broken, so nothing is
// warned about; that was the user's choice and boot says so.
func (c *Core) commit(message, reason string, author *git.Author) string {
	if c.git == nil {
		return ""
	}

	err := c.git.CommitAs(message, reason, author)
	if err == nil {
		return ""
	}

	slog.Error("gandalf: git commit", "message", message, "error", err)
	return c.commitWarning(err)
}

// commitWarning words the warning for a change that was saved but not
// committed.
func (c *Core) commitWarning(err error) string {
	problems := c.git.Health().Problem()
	if len(problems) == 0 {
		// Health says nothing is wrong, which would be a commit that failed
		// without being recorded. Say what happened rather than nothing.
		return fmt.Sprintf("this change was saved to the vault but NOT committed to git: %v", err)
	}
	return "this change was saved to the vault but NOT recorded in git: " + strings.Join(problems, "; ")
}

// gitWarning is the current state of the repository as a warning, or "" when
// there is nothing to say.
//
// It examines the repository first rather than reporting what was last seen.
// Boot is where a session decides whether to trust the vault, and the last
// write may be hours old: a repair made since should not be reported as
// breakage, and damage since should not be missed. A repair is committed to
// on the way, under the write lock so the checkpoint cannot sweep up a note
// another session is half-way through writing.
func (c *Core) gitWarning() string {
	if c.git == nil {
		return ""
	}

	if err := c.git.Check(&c.write); err != nil {
		slog.Error("gandalf: git is broken", "error", err)
	}

	return strings.Join(c.git.Health().Problem(), "; ")
}
