package vault

import (
	"regexp"
	"strings"
)

// wikilinkPattern matches Obsidian's [[target#heading|alias]] form. Targets
// may not contain the delimiters, which keeps nested brackets from matching.
var wikilinkPattern = regexp.MustCompile(`\[\[([^\[\]|#]+)(?:#([^\[\]|]+))?(?:\|([^\[\]]+))?\]\]`)

// Wikilink is a single [[target]] reference found in a note.
type Wikilink struct {
	// Target is the note the link points at: either a vault-relative path
	// without its .md extension, or a bare note name.
	Target  string
	Heading string
	Alias   string
	Raw     string

	// Line is 1-based within the body, or 0 for links that came from
	// frontmatter rather than prose.
	Line int
}

// ParseWikilinks extracts the wikilinks in markdown body text. Links inside
// fenced code blocks are ignored: sample code that mentions a link is not a
// reference to be resolved.
func ParseWikilinks(text string) []Wikilink {
	var links []Wikilink
	inFence := false

	for i, line := range strings.Split(text, "\n") {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range findWikilinks(line) {
			links = append(links, Wikilink{
				Target:  strings.TrimSpace(group(line, m, 1)),
				Heading: strings.TrimSpace(group(line, m, 2)),
				Alias:   strings.TrimSpace(group(line, m, 3)),
				Raw:     line[m[0]:m[1]],
				Line:    i + 1,
			})
		}
	}

	return links
}

// RewriteWikilinks rewrites the target of every wikilink in body text, leaving
// headings, aliases, and everything outside the delimiters untouched. A rewrite
// returning the empty string leaves that link as it was.
//
// Links inside fenced code blocks are left alone, for the same reason
// ParseWikilinks ignores them: sample text that mentions a link is not one.
func RewriteWikilinks(text string, rewrite func(target string) string) string {
	lines := strings.Split(text, "\n")
	inFence := false

	for i, line := range lines {
		if isFenceLine(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		matches := findWikilinks(line)

		var b strings.Builder
		last := 0
		for _, m := range matches {
			target := rewrite(strings.TrimSpace(group(line, m, 1)))
			if target == "" {
				continue
			}

			b.WriteString(line[last:m[0]])
			b.WriteString("[[")
			b.WriteString(target)
			if heading := strings.TrimSpace(group(line, m, 2)); heading != "" {
				b.WriteString("#")
				b.WriteString(heading)
			}
			if alias := strings.TrimSpace(group(line, m, 3)); alias != "" {
				b.WriteString("|")
				b.WriteString(alias)
			}
			b.WriteString("]]")
			last = m[1]
		}
		b.WriteString(line[last:])

		lines[i] = b.String()
	}

	return strings.Join(lines, "\n")
}

// LinkTarget reduces a related-entry to its bare target, tolerating entries
// that were hand-written with delimiters, a heading, or an alias.
func LinkTarget(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "[[")
	s = strings.TrimSuffix(s, "]]")
	if i := strings.IndexAny(s, "#|"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// findWikilinks returns the submatch index sets for the links on a line,
// skipping any inside inline code.
//
// A document explaining how links are written contains examples of links, and
// treating those as references would have the instruction set linking to notes
// that were never meant to exist. Backticks are how a writer says "this is
// syntax, not a reference".
func findWikilinks(line string) [][]int {
	masked := maskInlineCode(line)

	var out [][]int
	for _, m := range wikilinkPattern.FindAllStringSubmatchIndex(masked, -1) {
		out = append(out, m)
	}
	return out
}

// group returns a submatch from the original line, or the empty string when
// the group did not participate.
func group(line string, m []int, n int) string {
	if 2*n+1 >= len(m) || m[2*n] < 0 {
		return ""
	}
	return line[m[2*n]:m[2*n+1]]
}

// maskInlineCode blanks the contents of inline code spans, preserving length
// so match offsets still refer to the original line.
func maskInlineCode(line string) string {
	out := []byte(line)

	for i := 0; i < len(out); {
		if out[i] != '`' {
			i++
			continue
		}

		// Match the opening run of backticks with a closing run of the same
		// length, as markdown does.
		start := i
		for i < len(out) && out[i] == '`' {
			i++
		}
		fence := i - start

		for j := i; j < len(out); {
			if out[j] != '`' {
				j++
				continue
			}
			closeStart := j
			for j < len(out) && out[j] == '`' {
				j++
			}
			if j-closeStart != fence {
				continue
			}
			for k := start; k < j; k++ {
				out[k] = ' '
			}
			i = j
			break
		}
	}

	return string(out)
}

// isFenceLine reports whether a line opens or closes a fenced code block.
func isFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}
