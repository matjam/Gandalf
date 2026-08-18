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
		for _, m := range wikilinkPattern.FindAllStringSubmatch(line, -1) {
			links = append(links, Wikilink{
				Target:  strings.TrimSpace(m[1]),
				Heading: strings.TrimSpace(m[2]),
				Alias:   strings.TrimSpace(m[3]),
				Raw:     m[0],
				Line:    i + 1,
			})
		}
	}

	return links
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

// isFenceLine reports whether a line opens or closes a fenced code block.
func isFenceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}
