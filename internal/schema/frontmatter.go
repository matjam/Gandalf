package schema

import (
	"fmt"
	"regexp"
	"strings"
)

// tagPattern is the required shape of a tag: lowercase alphanumeric words
// joined by single hyphens. Keeping tags mechanically uniform is what makes
// them usable as filters later.
var tagPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Severity classifies how serious a validation finding is. Errors mean the
// note violates the contract; warnings mean it is legal but likely a mistake.
type Severity string

// The severity levels Gandalf reports.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue is a single validation finding against a frontmatter field.
type Issue struct {
	Field    string
	Severity Severity
	Message  string
}

// Frontmatter is the metadata block Gandalf owns at the top of every note.
// Agents never hand-write it: the tools generate and update it so the schema
// cannot drift.
type Frontmatter struct {
	Type    NoteType
	Created Date
	Updated Date
	Tags    []string
	Related []string // wikilink targets, stored without the [[ ]] delimiters
	Author  Author
	Status  Status // optional

	// Extra holds frontmatter keys Gandalf does not manage, such as Obsidian's
	// aliases or cssclasses. They are preserved verbatim on write so the tool
	// never destroys metadata it did not create.
	Extra map[string]any
}

// Validate reports every way the frontmatter departs from the contract. It
// returns findings rather than a single error so a caller can show an agent
// everything wrong with a note in one pass.
func (f Frontmatter) Validate() []Issue {
	var issues []Issue
	add := func(field string, sev Severity, format string, args ...any) {
		issues = append(issues, Issue{
			Field:    field,
			Severity: sev,
			Message:  fmt.Sprintf(format, args...),
		})
	}

	switch {
	case f.Type == "":
		add("type", SeverityError, "required")
	case !f.Type.Valid():
		add("type", SeverityError, "unknown note type %q (want one of %s)", f.Type, list(NoteTypes()))
	}

	if f.Created.IsZero() {
		add("created", SeverityError, "required")
	}
	if f.Updated.IsZero() {
		add("updated", SeverityError, "required")
	}
	if !f.Created.IsZero() && !f.Updated.IsZero() && f.Updated.Before(f.Created) {
		add("updated", SeverityError, "%s precedes created %s", f.Updated, f.Created)
	}

	switch {
	case f.Author == "":
		add("author", SeverityError, "required")
	case !f.Author.Valid():
		add("author", SeverityError, "unknown author %q (want one of %s)", f.Author, list(Authors()))
	}

	if f.Status != "" && !f.Status.Valid() {
		add("status", SeverityError, "unknown status %q (want one of %s)", f.Status, list(Statuses()))
	}

	issues = append(issues, validateTags(f.Tags)...)
	issues = append(issues, validateRelated(f.Related)...)

	return issues
}

// validateTags enforces tag shape and uniqueness.
func validateTags(tags []string) []Issue {
	if len(tags) == 0 {
		return []Issue{{
			Field:    "tags",
			Severity: SeverityWarning,
			Message:  "no tags; untagged notes are hard to find later",
		}}
	}

	var issues []Issue
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		switch {
		case !tagPattern.MatchString(tag):
			issues = append(issues, Issue{
				Field:    "tags",
				Severity: SeverityError,
				Message:  fmt.Sprintf("tag %q is not lowercase-hyphenated", tag),
			})
		case seen[tag]:
			issues = append(issues, Issue{
				Field:    "tags",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("duplicate tag %q", tag),
			})
		}
		seen[tag] = true
	}
	return issues
}

// validateRelated checks the shape of related links. Whether their targets
// exist is a vault-level question and is checked during linting.
func validateRelated(related []string) []Issue {
	var issues []Issue
	seen := make(map[string]bool, len(related))
	for _, target := range related {
		switch {
		case strings.TrimSpace(target) == "":
			issues = append(issues, Issue{
				Field:    "related",
				Severity: SeverityError,
				Message:  "empty related entry",
			})
		case seen[target]:
			issues = append(issues, Issue{
				Field:    "related",
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("duplicate related entry %q", target),
			})
		}
		seen[target] = true
	}
	return issues
}

// list renders enum values for an error message.
func list[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}
