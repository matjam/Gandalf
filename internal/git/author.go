package git

import (
	"fmt"
	"strings"
)

// Author is who a commit is attributed to, when that is somebody other than
// the repository's own identity.
//
// A hosted vault is written by several people through one process, so the
// repository's single configured identity cannot say who made a change. The
// author is recorded on the commit while the committer stays the repository's
// own identity, which keeps "Gandalf made this commit on behalf of X" visible
// in the log rather than collapsing the two.
type Author struct {
	Name  string
	Email string
}

// arg renders the author as the value of the --author flag.
func (a Author) arg() string {
	return fmt.Sprintf("%s <%s>", cleanIdent(a.Name), cleanIdent(a.Email))
}

// cleanIdent drops the characters that would let a name or address break out
// of the "Name <email>" form the tool parses, or smuggle a second line into it.
func cleanIdent(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', '\n', '\r', '\x00':
			return -1
		}
		return r
	}, s))
}

// authorArgs returns the arguments that attribute the next commit, or none
// when it is the repository's own. It assumes r.mu is held.
func (r *Repo) authorArgs() []string {
	if r.author == nil {
		return nil
	}
	return []string{"--author", r.author.arg()}
}
