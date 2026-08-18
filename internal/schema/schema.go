// Package schema defines Gandalf's note metadata contract: the frontmatter
// fields every note carries, the values those fields may take, and the
// validation rules applied whenever a note is written or linted.
//
// The package is pure domain logic and never touches the filesystem. Checks
// that need the vault as a whole — whether a note's type is a category this
// vault declares, whether a link resolves — live in package vault.
package schema

import "slices"

// NoteType is the kind of note, matching the name of a category the vault
// declares.
//
// It is deliberately not a closed set here. Categories are vault data, so the
// list of valid types is a fact about a particular vault rather than about
// Gandalf, and pinning it in this package would put the two out of step the
// first time somebody added one.
type NoteType string

// Author records who produced a note's content.
type Author string

// The authorship values Gandalf understands.
const (
	AuthorAgent Author = "agent"
	AuthorUser  Author = "user"
	AuthorBoth  Author = "both"
)

// Authors returns every valid authorship value.
func Authors() []Author { return []Author{AuthorAgent, AuthorUser, AuthorBoth} }

// Valid reports whether a is a known authorship value.
func (a Author) Valid() bool { return slices.Contains(Authors(), a) }

// Status is a note's optional lifecycle marker.
type Status string

// The status values Gandalf understands.
const (
	StatusInProgress Status = "in-progress"
	StatusComplete   Status = "complete"
	StatusSuperseded Status = "superseded"
)

// Statuses returns every valid status value.
func Statuses() []Status {
	return []Status{StatusInProgress, StatusComplete, StatusSuperseded}
}

// Valid reports whether s is a known status value.
func (s Status) Valid() bool { return slices.Contains(Statuses(), s) }
