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
	return parseNote(path, data, false)
}

// ParseNoteCoercing parses like ParseNote but reads a bare number or boolean
// where a string list is expected as its string form, so a note the importer
// could otherwise not write — a tag YAML decoded as the number 7695 — arrives
// intact. Ordinary reads use ParseNote and stay strict.
func ParseNoteCoercing(path string, data []byte) (*Note, error) {
	return parseNote(path, data, true)
}

// parseNote is the shared body of ParseNote and ParseNoteCoercing.
func parseNote(path string, data []byte, coerce bool) (*Note, error) {
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

	fm, issues, err := parseFrontmatter(strings.Join(lines[:end], "\n"), coerce)
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
	// Only written when a note is deliberately kept out of the index, so the
	// common case carries no extra key.
	if n.FM.Index != nil && !*n.FM.Index {
		b.WriteString("index: false\n")
	}
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
	// Append above the maintained backlinks block, or the addition lands under
	// a heading it has nothing to do with and is destroyed the next time the
	// block is rewritten.
	body := n.Content()
	_, block := SplitBacklinks(n.Body)

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

	if block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}

	n.Body = b.String()
}

// AppendToSection adds content to the end of one named section, creating that
// section at the foot of the body when the note does not already have it.
//
// Plain Append puts its content after everything, which means it lands under
// whichever heading happens to be last. That is fine for a chronological note,
// where the end is where new material belongs, and wrong for a document
// organised by subject: a rule appended to the operating contract would file
// itself under the contract's closing section and read as part of it. Naming
// the section makes placement a decision rather than an accident of layout.
//
// An ambiguous heading is refused rather than guessed at, matching the
// replacement path: two sections with the same name give no basis for choosing
// one.
func (n *Note) AppendToSection(heading, content string) error {
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return fmt.Errorf("appending to a section needs a heading")
	}
	if strings.EqualFold(strings.TrimLeft(heading, "# "), backlinksTitle) {
		return fmt.Errorf("%w and cannot be appended to", ErrBacklinksProtected)
	}

	body := n.Content()
	found := headings(body)

	matches := matchHeadings(found, heading)
	if len(matches) > 1 {
		return fmt.Errorf("%w: %q matches %d headings", ErrSectionAmbiguous, heading, len(matches))
	}
	if len(matches) == 0 {
		n.Append(heading, content)
		return nil
	}

	lines := strings.Split(body, "\n")
	_, end := sectionSpan(len(lines), found, matches[0])

	// Trailing blank lines belong to the gap before the next heading, not to
	// the section, so step back over them before inserting. Otherwise every
	// addition pushes the section further from its own heading.
	at := end
	for at > matches[0].line+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}

	rebuilt := make([]string, 0, len(lines)+3)
	rebuilt = append(rebuilt, lines[:at]...)
	rebuilt = append(rebuilt, "", strings.TrimSpace(content))
	rebuilt = append(rebuilt, lines[at:]...)

	n.setContent(strings.Join(rebuilt, "\n"))
	return nil
}

// Indexed reports whether the note belongs in the search index. Notes are
// searchable unless their frontmatter says otherwise.
func (n *Note) Indexed() bool {
	return n.FM.Index == nil || *n.FM.Index
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
