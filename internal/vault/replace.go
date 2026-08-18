package vault

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Errors returned when a replacement cannot be located unambiguously, or when
// it would write somewhere Gandalf owns.
//
// Every one of these is a refusal rather than a best guess. A replacement the
// caller cannot see the extent of is the one edit that can quietly destroy a
// note, so an ambiguous request is an error and never a first match.
var (
	ErrSectionNotFound    = errors.New("no section with that heading")
	ErrSectionAmbiguous   = errors.New("more than one section with that heading")
	ErrAnchorNotFound     = errors.New("anchor does not appear in the note")
	ErrAnchorAmbiguous    = errors.New("anchor appears more than once")
	ErrAnchorOrder        = errors.New("the closing anchor does not follow the opening one")
	ErrBacklinksProtected = errors.New("the backlinks block is maintained by Gandalf")
)

// headingLine matches an ATX heading.
var headingLine = regexp.MustCompile(`^(#{1,6})[ \t]+(.*)$`)

// heading is one heading found in a note's content.
type heading struct {
	line  int
	level int
	text  string
}

// ReplaceContent replaces everything the note says, leaving its frontmatter
// and its maintained backlinks block alone. The removed text is returned so
// the caller can see what it just discarded.
func (n *Note) ReplaceContent(content string) (removed string, err error) {
	if err := guardContent(content); err != nil {
		return "", err
	}

	removed = n.Content()
	n.setContent(content)
	return removed, nil
}

// ReplaceSection replaces one section: the text under a heading, up to the
// next heading of equal or higher level, or the end of the note.
//
// The heading may be given as its text ("Verification") or as the whole line
// ("## Verification"), in which case the level must match too. It must name
// exactly one section; two sections with the same heading are an error rather
// than a coin toss.
//
// includeHeading extends the replacement to cover the heading line itself,
// which is how a section is deleted outright. It defaults to false so that a
// replacement aimed below a heading cannot remove the heading it was aimed at.
func (n *Note) ReplaceSection(section, content string, includeHeading bool) (removed string, err error) {
	if err := guardContent(content); err != nil {
		return "", err
	}
	if strings.EqualFold(strings.TrimLeft(strings.TrimSpace(section), "# "), backlinksTitle) {
		return "", fmt.Errorf("%w and cannot be replaced", ErrBacklinksProtected)
	}

	body := n.Content()
	lines := strings.Split(body, "\n")
	found := headings(body)

	matches := matchHeadings(found, section)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %q (this note has %s)", ErrSectionNotFound, section, headingList(found))
	case 1:
	default:
		return "", fmt.Errorf("%w: %q matches %d headings; pass the exact line, such as %q",
			ErrSectionAmbiguous, section, len(matches), lines[matches[0].line])
	}

	start, end := sectionSpan(len(lines), found, matches[0])
	if !includeHeading {
		start++
	}

	removed = strings.Join(lines[start:end], "\n")
	n.setContent(stitch(
		strings.Join(lines[:start], "\n"),
		content,
		strings.Join(lines[end:], "\n"),
	))

	return strings.Trim(removed, "\n"), nil
}

// ReplaceBetween replaces the span bounded by two literal anchors.
//
// Both anchors must appear exactly once, and the closing one must follow the
// opening one. A closing anchor that is missing or out of order is an error:
// falling back to the end of the note is the behaviour that eats documents.
//
// includeAnchors extends the replacement to cover the anchors themselves. It
// defaults to false, so the anchors survive and only what sits between them is
// rewritten.
func (n *Note) ReplaceBetween(from, to, content string, includeAnchors bool) (removed string, err error) {
	if err := guardContent(content); err != nil {
		return "", err
	}
	if from == "" || to == "" {
		return "", fmt.Errorf("anchored replacement needs both a from and a to anchor")
	}

	body := n.Content()

	opening, err := onlyIndex(body, from, "from")
	if err != nil {
		return "", err
	}
	closing, err := onlyIndex(body, to, "to")
	if err != nil {
		return "", err
	}

	if closing < opening+len(from) {
		return "", fmt.Errorf("%w: %q ends at %d and %q starts at %d",
			ErrAnchorOrder, from, opening+len(from), to, closing)
	}

	start, end := opening+len(from), closing
	if includeAnchors {
		start, end = opening, closing+len(to)
	}

	removed = body[start:end]
	n.setContent(body[:start] + content + body[end:])

	return removed, nil
}

// setContent replaces the note's own content while leaving the maintained
// backlinks block where it is, in the same shape SetBacklinks writes it.
func (n *Note) setContent(content string) {
	_, block := SplitBacklinks(n.Body)

	content = strings.Trim(content, "\n")
	if block == "" {
		n.Body = content + "\n"
		return
	}
	n.Body = content + "\n\n" + block
}

// guardContent refuses replacement text that would forge the maintained
// block. A note carrying a second "## Backlinks" heading either loses the
// content below it on the next rebuild or has its link state read from prose
// nobody maintains.
func guardContent(content string) error {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == BacklinksHeading {
			return fmt.Errorf("%w: replacement content may not contain a %q heading",
				ErrBacklinksProtected, BacklinksHeading)
		}
	}
	return nil
}

// headings returns the headings in a note's content.
//
// Fenced code is skipped: a shell comment or a Go build tag inside an example
// is not a section, and treating one as a section would bound a replacement at
// a line the caller cannot see.
func headings(content string) []heading {
	var out []heading
	fence := ""

	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fence = "```"
			continue
		case strings.HasPrefix(trimmed, "~~~"):
			fence = "~~~"
			continue
		}

		m := headingLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, heading{
			line:  i,
			level: len(m[1]),
			text:  strings.TrimSpace(strings.TrimRight(m[2], "# \t")),
		})
	}

	return out
}

// matchHeadings returns the headings a request names. A request carrying
// hashes has to match the level as well, so "### Notes" does not select a
// "## Notes" elsewhere in the note.
func matchHeadings(found []heading, want string) []heading {
	want = strings.TrimSpace(want)

	level := 0
	if m := headingLine.FindStringSubmatch(want); m != nil {
		level = len(m[1])
		want = strings.TrimSpace(strings.TrimRight(m[2], "# \t"))
	}

	var out []heading
	for _, h := range found {
		if level > 0 && h.level != level {
			continue
		}
		if strings.EqualFold(h.text, want) {
			out = append(out, h)
		}
	}
	return out
}

// sectionSpan returns the half-open line range a section occupies: its heading
// through the line before the next heading of equal or higher level.
func sectionSpan(lines int, found []heading, at heading) (start, end int) {
	for _, h := range found {
		if h.line > at.line && h.level <= at.level {
			return at.line, h.line
		}
	}
	return at.line, lines
}

// headingList renders a note's headings for an error message, so a caller that
// named the wrong one can see what is actually there.
func headingList(found []heading) string {
	if len(found) == 0 {
		return "no headings"
	}
	names := make([]string, 0, len(found))
	for _, h := range found {
		names = append(names, fmt.Sprintf("%q", h.text))
	}
	return strings.Join(names, ", ")
}

// onlyIndex returns the position of the one occurrence of anchor in body.
func onlyIndex(body, anchor, which string) (int, error) {
	first := strings.Index(body, anchor)
	if first < 0 {
		return 0, fmt.Errorf("%w: %s anchor %q", ErrAnchorNotFound, which, anchor)
	}
	if n := strings.Count(body, anchor); n > 1 {
		return 0, fmt.Errorf("%w: %s anchor %q appears %d times; extend it until it is unique",
			ErrAnchorAmbiguous, which, anchor, n)
	}
	return first, nil
}

// stitch joins the pieces of an edited body with exactly one blank line at
// each seam, so a replacement neither welds two paragraphs together nor leaves
// a run of blank lines where the old text was.
func stitch(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.Trim(p, "\n"); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}
