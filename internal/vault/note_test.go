package vault

import (
	"errors"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

const canonical = `---
type: session
created: 2026-08-17
updated: 2026-08-17
tags: [agent, memory]
related:
  - "[[Agent/OS/Memory]]"
author: both
status: in-progress
---

# Memory Toolset

Body prose.
`

func TestParseNote(t *testing.T) {
	n, err := ParseNote("Sessions/2026/08/note.md", []byte(canonical))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	if n.FM.Type != typeSession {
		t.Errorf("type = %q, want session", n.FM.Type)
	}
	if n.FM.Created.String() != "2026-08-17" {
		t.Errorf("created = %q, want 2026-08-17", n.FM.Created)
	}
	if got := strings.Join(n.FM.Tags, ","); got != "agent,memory" {
		t.Errorf("tags = %q, want agent,memory", got)
	}
	if got := strings.Join(n.FM.Related, ","); got != "Agent/OS/Memory" {
		t.Errorf("related = %q, want Agent/OS/Memory (delimiters stripped)", got)
	}
	if n.FM.Author != schema.AuthorBoth || n.FM.Status != schema.StatusInProgress {
		t.Errorf("author/status = %q/%q, want both/in-progress", n.FM.Author, n.FM.Status)
	}
	if n.Title() != "Memory Toolset" {
		t.Errorf("title = %q, want Memory Toolset", n.Title())
	}
	if !strings.HasPrefix(n.Body, "# Memory Toolset") {
		t.Errorf("body = %q, want it to open with the heading", n.Body)
	}
	if len(n.Issues) > 0 {
		t.Errorf("issues = %+v, want none", n.Issues)
	}
	if issues := n.FM.Validate(); len(issues) > 0 {
		t.Errorf("validate = %+v, want none", issues)
	}
}

func TestParseNoteRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "no frontmatter", in: "# Just a heading\n", want: ErrNoFrontmatter},
		{name: "frontmatter not first", in: "\n---\ntype: session\n---\n", want: ErrNoFrontmatter},
		{name: "unterminated", in: "---\ntype: session\n", want: ErrUnterminatedFrontmatter},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseNote("note.md", []byte(tc.in)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseNoteReportsBadFieldsAsIssues(t *testing.T) {
	const in = `---
type: session
created: yesterday
updated: 2026-08-17
tags: agent
author: [both]
---

# Note
`
	n, err := ParseNote("note.md", []byte(in))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	got := map[string]bool{}
	for _, i := range n.Issues {
		got[i.Field] = true
	}
	if !got["created"] {
		t.Errorf("issues = %+v, want one for created", n.Issues)
	}
	if !got["author"] {
		t.Errorf("issues = %+v, want one for author", n.Issues)
	}
	if !n.FM.Created.IsZero() {
		t.Error("created was set despite being unparseable")
	}
	// A bare string is a tolerated shorthand for a one-element list.
	if strings.Join(n.FM.Tags, ",") != "agent" {
		t.Errorf("tags = %v, want [agent]", n.FM.Tags)
	}
}

func TestParseNoteTolerantOfEncoding(t *testing.T) {
	in := "\uFEFF" + strings.ReplaceAll(canonical, "\n", "\r\n")

	n, err := ParseNote("note.md", []byte(in))
	if err != nil {
		t.Fatalf("ParseNote with BOM and CRLF: %v", err)
	}
	if n.FM.Type != typeSession {
		t.Errorf("type = %q, want session", n.FM.Type)
	}
	if strings.Contains(n.Body, "\r") {
		t.Error("body retained carriage returns")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	n, err := ParseNote("note.md", []byte(canonical))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	got := string(n.Render())
	if got != canonical {
		t.Errorf("render is not byte-identical to input:\n--- got ---\n%s\n--- want ---\n%s", got, canonical)
	}

	again, err := ParseNote("note.md", []byte(got))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if string(again.Render()) != got {
		t.Error("second render differs from the first")
	}
}

func TestRenderPreservesUnmanagedKeys(t *testing.T) {
	const in = `---
type: standard
created: 2026-08-17
updated: 2026-08-17
tags: [standards]
author: agent
aliases:
  - Old Name
cssclass: wide
---

# Standard
`
	n, err := ParseNote("Standards/s.md", []byte(in))
	if err != nil {
		t.Fatalf("ParseNote: %v", err)
	}

	got := string(n.Render())
	for _, want := range []string{"aliases:", "Old Name", "cssclass: wide"} {
		if !strings.Contains(got, want) {
			t.Errorf("render dropped %q:\n%s", want, got)
		}
	}
}

func TestRenderQuotesAmbiguousScalars(t *testing.T) {
	n := &Note{
		Path: "note.md",
		FM: schema.Frontmatter{
			Type:    typeSession,
			Created: mustDate(t, "2026-08-17"),
			Updated: mustDate(t, "2026-08-17"),
			Tags:    []string{"no", "2026", "plain"},
			Author:  schema.AuthorAgent,
		},
		Body: "# Note\n",
	}

	got := string(n.Render())
	if !strings.Contains(got, `tags: ["no", "2026", plain]`) {
		t.Errorf("ambiguous tags were not quoted:\n%s", got)
	}
	if !strings.Contains(got, "created: 2026-08-17\n") {
		t.Errorf("dates should stay unquoted:\n%s", got)
	}
}

func TestRenderEmptyCollections(t *testing.T) {
	n := &Note{
		Path: "note.md",
		FM: schema.Frontmatter{
			Type:    typeGlossary,
			Created: mustDate(t, "2026-08-17"),
			Updated: mustDate(t, "2026-08-17"),
			Author:  schema.AuthorAgent,
		},
	}

	got := string(n.Render())
	if !strings.Contains(got, "tags: []\n") || !strings.Contains(got, "related: []\n") {
		t.Errorf("empty collections should render as []:\n%s", got)
	}
	if strings.Contains(got, "status:") {
		t.Errorf("unset optional status should be omitted:\n%s", got)
	}
}

func TestTitleIgnoresDeeperHeadings(t *testing.T) {
	n := &Note{Body: "## Section\n\n# Real Title\n"}
	if got := n.Title(); got != "Real Title" {
		t.Errorf("title = %q, want Real Title", got)
	}
	if got := (&Note{Body: "no headings\n"}).Title(); got != "" {
		t.Errorf("title = %q, want empty", got)
	}
}

// Categories are vault data now, so tests name them as the strings they are
// rather than reaching for constants that no longer exist.
const (
	typeSession  = schema.NoteType("session")
	typeStandard = schema.NoteType("standard")
	typeGlossary = schema.NoteType("glossary")
	typeProject  = schema.NoteType("project")
	typeMeeting  = schema.NoteType("meeting")
	typeReadme   = schema.NoteType("readme")
)

func mustDate(t *testing.T, s string) schema.Date {
	t.Helper()
	d, err := schema.ParseDate(s)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", s, err)
	}
	return d
}
