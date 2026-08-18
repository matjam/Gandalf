// Package schema defines Gandalf's note metadata contract: the frontmatter
// fields every note carries, the values those fields may take, and the
// validation rules applied whenever a note is written or linted.
//
// The package is pure domain logic and never touches the filesystem. Checks
// that need the vault as a whole — dead wikilinks, filing conventions — live
// in package vault.
package schema

import "slices"

// NoteType identifies the kind of note. It selects the note's template and,
// for some types, where in the vault the note is filed.
type NoteType string

// The note types Gandalf understands.
const (
	TypeSession   NoteType = "session"
	TypeDesign    NoteType = "design"
	TypeDecisions NoteType = "decisions"
	TypeStandard  NoteType = "standard"
	TypeGlossary  NoteType = "glossary"
	TypeMeeting   NoteType = "meeting"
	TypeInterview NoteType = "interview"
	TypeReadme    NoteType = "readme"
)

// NoteTypes returns every valid note type.
func NoteTypes() []NoteType {
	return []NoteType{
		TypeSession, TypeDesign, TypeDecisions, TypeStandard,
		TypeGlossary, TypeMeeting, TypeInterview, TypeReadme,
	}
}

// Valid reports whether t is a known note type.
func (t NoteType) Valid() bool { return slices.Contains(NoteTypes(), t) }

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
