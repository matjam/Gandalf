// Package vault reads, writes, and validates notes in an Obsidian-compatible
// markdown vault. Markdown files stay the canonical store: Gandalf owns the
// frontmatter block and leaves prose alone.
package vault

import (
	"errors"
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// Errors returned when a file cannot be read as a note at all, as opposed to
// merely violating the schema.
var (
	ErrNoFrontmatter           = errors.New("no frontmatter block")
	ErrUnterminatedFrontmatter = errors.New("unterminated frontmatter block")
)

// fence is the frontmatter delimiter line.
const fence = "---"

// Note is a parsed markdown note: the metadata Gandalf manages plus the body
// prose it does not.
type Note struct {
	// Path is vault-relative and slash-separated.
	Path string
	FM   schema.Frontmatter
	Body string

	// Issues records frontmatter that could not be represented in the schema,
	// such as a date in the wrong format or a field of the wrong YAML type.
	// The affected fields are left unset; linting reports these alongside rule
	// violations, and writing a note with unresolved issues is refused so the
	// original values are never silently dropped.
	Issues []schema.Issue
}

// ParseNote parses the bytes of a markdown file into a Note. It returns an
// error only when there is no usable frontmatter block; schema problems are
// reported through Note.Issues.
func ParseNote(path string, data []byte) (*Note, error) {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	if !strings.HasPrefix(text, fence+"\n") {
		return nil, fmt.Errorf("%s: %w", path, ErrNoFrontmatter)
	}

	lines := strings.Split(text[len(fence)+1:], "\n")
	end := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == fence {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrUnterminatedFrontmatter)
	}

	fm, issues, err := parseFrontmatter(strings.Join(lines[:end], "\n"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &Note{
		Path:   path,
		FM:     fm,
		Body:   strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n"),
		Issues: issues,
	}, nil
}

// Render serialises the note back to markdown with frontmatter in canonical
// field order. Rendering is deterministic — the same note always produces the
// same bytes — so writes stay diff-friendly.
func (n *Note) Render() []byte {
	var b strings.Builder

	b.WriteString(fence + "\n")
	writeScalar(&b, "type", string(n.FM.Type))
	writeDate(&b, "created", n.FM.Created.String())
	writeDate(&b, "updated", n.FM.Updated.String())
	b.WriteString("tags: " + flowList(n.FM.Tags) + "\n")
	writeRelated(&b, n.FM.Related)
	writeScalar(&b, "author", string(n.FM.Author))
	writeScalar(&b, "status", string(n.FM.Status))
	writeExtra(&b, n.FM.Extra)
	b.WriteString(fence + "\n\n")

	if body := strings.TrimRight(n.Body, "\n"); body != "" {
		b.WriteString(body)
		b.WriteString("\n")
	}

	return []byte(b.String())
}

// Touch records that the note changed on the given date.
func (n *Note) Touch(on schema.Date) {
	if !on.IsZero() {
		n.FM.Updated = on
	}
}

// Append adds content to the end of the body, under an optional heading.
//
// Appending is the only body edit Gandalf performs. Notes that are append-only
// by convention — decisions, session notes — are exactly the ones an agent
// writes to repeatedly, and appending cannot destroy what is already there.
func (n *Note) Append(heading, content string) {
	body := strings.TrimRight(n.Body, "\n")

	var b strings.Builder
	b.WriteString(body)
	if body != "" {
		b.WriteString("\n\n")
	}
	if heading = strings.TrimSpace(heading); heading != "" {
		if !strings.HasPrefix(heading, "#") {
			heading = "## " + heading
		}
		b.WriteString(heading)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(content))
	b.WriteString("\n")

	n.Body = b.String()
}

// Title returns the note's first level-one heading, or the empty string when
// it has none.
func (n *Note) Title() string {
	for _, line := range strings.Split(n.Body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}
