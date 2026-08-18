package vault

import (
	"sort"
	"strings"
)

// BacklinksHeading opens the block Gandalf maintains at the foot of a note.
//
// The block is machine-owned. Everything above it belongs to whoever wrote the
// note; everything from the heading to the end of the file is rewritten
// whenever the links pointing here change, so nothing hand-written should live
// there.
const BacklinksHeading = "## Backlinks"

// backlinksNote explains the block to whoever opens the file in an editor and
// wonders why their edits keep vanishing.
const backlinksNote = "*Maintained by Gandalf. Edits here are overwritten.*"

// SplitBacklinks separates a note's own content from the maintained block.
//
// Three things depend on this split, and each of them is wrong without it:
// links in the block are not the note's outgoing links, so extracting them
// would make every backlink reciprocal and recurse; the block is not part of
// what a user wrote, so a seed fingerprint that included it would report a
// document as modified the moment somebody linked to it; and appended content
// belongs above it, not filed under a heading it has nothing to do with.
func SplitBacklinks(body string) (content, block string) {
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		if strings.TrimSpace(line) != BacklinksHeading {
			continue
		}
		return strings.Join(lines[:i], "\n"), strings.Join(lines[i:], "\n")
	}

	return body, ""
}

// Content returns the note's body without the maintained backlinks block.
func (n *Note) Content() string {
	content, _ := SplitBacklinks(n.Body)
	return strings.TrimRight(content, "\n")
}

// Backlinks returns the notes recorded as linking here.
func (n *Note) Backlinks() []string {
	_, block := SplitBacklinks(n.Body)
	if block == "" {
		return nil
	}

	var targets []string
	for _, link := range ParseWikilinks(block) {
		targets = append(targets, link.Target)
	}
	return targets
}

// SetBacklinks rewrites the maintained block, leaving the note's own content
// untouched. Passing no targets removes the block entirely rather than leaving
// an empty heading behind.
func (n *Note) SetBacklinks(targets []string) {
	content := n.Content()

	unique := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, t := range targets {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			unique = append(unique, t)
			seen[t] = true
		}
	}
	sort.Strings(unique)

	if len(unique) == 0 {
		n.Body = content + "\n"
		return
	}

	var b strings.Builder
	b.WriteString(content)
	b.WriteString("\n\n")
	b.WriteString(BacklinksHeading)
	b.WriteString("\n\n")
	b.WriteString(backlinksNote)
	b.WriteString("\n\n")
	for _, target := range unique {
		b.WriteString("- [[" + target + "]]\n")
	}

	n.Body = b.String()
}

// OutgoingLinks returns the link targets a note points at: its frontmatter
// relations plus the wikilinks in its own content, excluding the maintained
// block.
func (n *Note) OutgoingLinks() []string {
	out := make([]string, 0, len(n.FM.Related))
	seen := map[string]bool{}

	add := func(target string) {
		if target = strings.TrimSpace(target); target != "" && !seen[target] {
			out = append(out, target)
			seen[target] = true
		}
	}

	for _, target := range n.FM.Related {
		add(target)
	}
	for _, link := range ParseWikilinks(n.Content()) {
		add(link.Target)
	}

	return out
}
