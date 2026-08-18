package vault

import (
	"errors"
	"strings"
	"testing"
)

// sample is a note with the shapes that matter: nested headings, a fenced code
// block containing something that looks like a heading, and the maintained
// block at the foot.
const sample = `# Title

Intro paragraph.

## Verification

Old verification text.

### Tests

Old tests text.

## Shipping

Ship it:

` + "```" + `sh
# Shipping
make release
` + "```" + `

## Backlinks

*Maintained by Gandalf. Edits here are overwritten.*

- [[Projects/gandalf/Design]]
`

func sampleNote() *Note { return &Note{Path: "Projects/gandalf/Design.md", Body: sample} }

func TestReplaceSection(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceSection("Verification", "New verification text.", false)
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	// A section runs to the next heading of equal or higher level, so its
	// subsections go with it.
	for _, want := range []string{"Old verification text.", "### Tests", "Old tests text."} {
		if !strings.Contains(removed, want) {
			t.Errorf("removed text is missing %q:\n%s", want, removed)
		}
	}

	content := n.Content()
	if !strings.Contains(content, "## Verification\n\nNew verification text.\n\n## Shipping") {
		t.Errorf("content did not stitch cleanly:\n%s", content)
	}
	if strings.Contains(content, "Old tests text.") {
		t.Error("the replaced section's subsection survived")
	}
	if !strings.Contains(content, "Intro paragraph.") {
		t.Error("content before the section was lost")
	}
	if !strings.Contains(n.Body, BacklinksHeading) {
		t.Error("the maintained block was lost")
	}
}

// TestReplaceSectionSubsection checks that a deeper heading is bounded by the
// next one of higher level, not by the end of the note.
func TestReplaceSectionSubsection(t *testing.T) {
	n := sampleNote()

	if _, err := n.ReplaceSection("Tests", "New tests text.", false); err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	content := n.Content()
	if !strings.Contains(content, "### Tests\n\nNew tests text.\n\n## Shipping") {
		t.Errorf("subsection replacement crossed its bound:\n%s", content)
	}
	if !strings.Contains(content, "Old verification text.") {
		t.Error("the parent section's own text was replaced")
	}
}

// TestReplaceSectionIncludingHeading is how a section is removed outright.
func TestReplaceSectionIncludingHeading(t *testing.T) {
	n := sampleNote()

	if _, err := n.ReplaceSection("## Verification", "", true); err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	content := n.Content()
	if strings.Contains(content, "Verification") {
		t.Errorf("the heading survived its own removal:\n%s", content)
	}
	if !strings.Contains(content, "Intro paragraph.\n\n## Shipping") {
		t.Errorf("removal did not stitch cleanly:\n%s", content)
	}
}

// TestReplaceSectionKeepsHeadingByDefault is the guard against a replacement
// aimed below a heading deleting the heading it was aimed at.
func TestReplaceSectionKeepsHeadingByDefault(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceSection("Verification", "", false)
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	if strings.Contains(removed, "## Verification") {
		t.Errorf("the heading was reported as removed:\n%s", removed)
	}
	if !strings.Contains(n.Content(), "## Verification") {
		t.Error("the heading was removed without being asked for")
	}
}

// TestReplaceSectionLast runs to the end of the note's own content, and must
// stop before the maintained block.
func TestReplaceSectionLast(t *testing.T) {
	n := sampleNote()

	if _, err := n.ReplaceSection("Shipping", "Nothing to ship.", false); err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	content := n.Content()
	if !strings.HasSuffix(content, "## Shipping\n\nNothing to ship.") {
		t.Errorf("last section did not run to the end of the content:\n%s", content)
	}
	if !strings.Contains(n.Body, "- [[Projects/gandalf/Design]]") {
		t.Error("the maintained block was consumed by the last section")
	}
}

// TestReplaceSectionIgnoresFencedHeadings checks that a comment in a shell
// example is not treated as a section boundary.
func TestReplaceSectionIgnoresFencedHeadings(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceSection("Shipping", "Nothing to ship.", false)
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	if !strings.Contains(removed, "make release") {
		t.Errorf("the section stopped at a heading inside fenced code:\n%s", removed)
	}
}

func TestReplaceSectionErrors(t *testing.T) {
	tests := []struct {
		name    string
		section string
		content string
		want    error
	}{
		{
			name:    "no such heading",
			section: "Deployment",
			want:    ErrSectionNotFound,
		},
		{
			name:    "wrong level",
			section: "#### Verification",
			want:    ErrSectionNotFound,
		},
		{
			name:    "backlinks block",
			section: "Backlinks",
			want:    ErrBacklinksProtected,
		},
		{
			name:    "content forging the block",
			section: "Verification",
			content: "Fine.\n\n## Backlinks\n\n- [[Projects/gandalf/Design]]",
			want:    ErrBacklinksProtected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := sampleNote()
			before := n.Body

			if _, err := n.ReplaceSection(tt.section, tt.content, false); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if n.Body != before {
				t.Error("the note was modified despite the error")
			}
		})
	}
}

// TestReplaceSectionAmbiguous refuses rather than picking the first match.
func TestReplaceSectionAmbiguous(t *testing.T) {
	n := &Note{Body: "# T\n\n## Notes\n\nOne.\n\n## Other\n\nx\n\n## Notes\n\nTwo.\n"}

	_, err := n.ReplaceSection("Notes", "New.", false)
	if !errors.Is(err, ErrSectionAmbiguous) {
		t.Fatalf("error = %v, want ErrSectionAmbiguous", err)
	}
	if !strings.Contains(err.Error(), "matches 2 headings") {
		t.Errorf("error does not say how many matched: %v", err)
	}
}

// TestReplaceSectionLevelDisambiguates lets a caller pick between headings of
// the same text at different levels.
func TestReplaceSectionLevelDisambiguates(t *testing.T) {
	n := &Note{Body: "# T\n\n## Notes\n\nOuter.\n\n### Notes\n\nInner.\n"}

	removed, err := n.ReplaceSection("### Notes", "Replaced.", false)
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	if removed != "Inner." {
		t.Errorf("removed = %q, want %q", removed, "Inner.")
	}
	if !strings.Contains(n.Content(), "Outer.") {
		t.Error("the outer section was replaced instead")
	}
}

func TestReplaceBetween(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceBetween("## Verification", "## Shipping", "\n\nBounded.\n\n", false)
	if err != nil {
		t.Fatalf("ReplaceBetween: %v", err)
	}
	if !strings.Contains(removed, "Old verification text.") {
		t.Errorf("removed = %q", removed)
	}

	content := n.Content()
	if !strings.Contains(content, "## Verification\n\nBounded.\n\n## Shipping") {
		t.Errorf("anchored replacement did not land where expected:\n%s", content)
	}
	// Anchors are kept unless the caller says otherwise.
	if !strings.Contains(content, "## Verification") || !strings.Contains(content, "## Shipping") {
		t.Error("an anchor was consumed without include_anchors")
	}
}

func TestReplaceBetweenIncludingAnchors(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceBetween("## Verification", "## Shipping", "## Everything", true)
	if err != nil {
		t.Fatalf("ReplaceBetween: %v", err)
	}
	if !strings.HasPrefix(removed, "## Verification") || !strings.HasSuffix(removed, "## Shipping") {
		t.Errorf("removed text does not span the anchors:\n%s", removed)
	}
	if strings.Contains(n.Content(), "## Verification") {
		t.Error("the opening anchor survived include_anchors")
	}
}

func TestReplaceBetweenErrors(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		want     error
	}{
		{
			name: "opening anchor absent",
			from: "## Deployment",
			to:   "## Shipping",
			want: ErrAnchorNotFound,
		},
		{
			// The failure that would otherwise run to the end of the note.
			name: "closing anchor absent",
			from: "## Verification",
			to:   "## Deployment",
			want: ErrAnchorNotFound,
		},
		{
			name: "anchor not unique",
			from: "Old",
			to:   "## Shipping",
			want: ErrAnchorAmbiguous,
		},
		{
			name: "anchors out of order",
			from: "## Shipping",
			to:   "## Verification",
			want: ErrAnchorOrder,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := sampleNote()
			before := n.Body

			if _, err := n.ReplaceBetween(tt.from, tt.to, "x", false); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if n.Body != before {
				t.Error("the note was modified despite the error")
			}
		})
	}
}

func TestReplaceContent(t *testing.T) {
	n := sampleNote()

	removed, err := n.ReplaceContent("# Title\n\nAll new.")
	if err != nil {
		t.Fatalf("ReplaceContent: %v", err)
	}
	if !strings.Contains(removed, "Old verification text.") {
		t.Error("removed text does not include what was there")
	}
	if strings.Contains(removed, BacklinksHeading) {
		t.Error("the maintained block was reported as removed")
	}

	if n.Content() != "# Title\n\nAll new." {
		t.Errorf("content = %q", n.Content())
	}
	if !strings.Contains(n.Body, "- [[Projects/gandalf/Design]]") {
		t.Error("the maintained block was destroyed")
	}
}

// TestReplaceContentWithoutBlock covers a note nothing links to.
func TestReplaceContentWithoutBlock(t *testing.T) {
	n := &Note{Body: "# T\n\nOld.\n"}

	if _, err := n.ReplaceContent("# T\n\nNew."); err != nil {
		t.Fatalf("ReplaceContent: %v", err)
	}
	if n.Body != "# T\n\nNew.\n" {
		t.Errorf("body = %q", n.Body)
	}
}

// TestReplaceRoundTripsThroughRender checks an edited note still renders and
// parses back to the same thing, since every replacement is followed by a
// write.
func TestReplaceRoundTripsThroughRender(t *testing.T) {
	original := "---\ntype: project\ncreated: 2026-08-18\nupdated: 2026-08-18\ntags: [gandalf]\nrelated: []\nauthor: agent\n---\n\n" + sample

	n, err := ParseNote("Projects/gandalf/Design.md", []byte(original))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}
	if _, err := n.ReplaceSection("Verification", "New.", false); err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	again, err := ParseNote(n.Path, n.Render())
	if err != nil {
		t.Fatalf("ParseNote after replace: %v", err)
	}
	if again.Body != n.Body {
		t.Errorf("body changed through a render round trip:\n%q\n%q", again.Body, n.Body)
	}
	if got := again.Backlinks(); len(got) != 1 || got[0] != "Projects/gandalf/Design" {
		t.Errorf("backlinks = %v", got)
	}
}
