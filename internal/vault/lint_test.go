package vault

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

// has reports whether any finding for the given path mentions substr.
func has(findings []Finding, path, substr string) bool {
	for _, f := range findings {
		if f.Path == path && strings.Contains(f.String(), substr) {
			return true
		}
	}
	return false
}

func TestLintCleanVault(t *testing.T) {
	v := newVault(t)

	for _, req := range []NewNoteRequest{
		{Type: typeStandard, Title: "Privacy", Tags: []string{"standards"}},
		{Type: typeSession, Title: "First Session", Tags: []string{"session"},
			Related: []string{"Standards/privacy"}, On: mustDate(t, "2026-08-17")},
	} {
		n, err := v.NewNote(req)
		if err != nil {
			t.Fatalf("NewNote(%q): %v", req.Title, err)
		}
		if err := v.Write(n); err != nil {
			t.Fatalf("Write(%q): %v", n.Path, err)
		}
	}

	findings, err := v.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("clean vault produced findings: %v", findings)
	}
	if HasErrors(findings) {
		t.Error("HasErrors on an empty finding set")
	}
}

func TestLintReportsProblems(t *testing.T) {
	v := newVault(t)

	write(t, v, "Standards/privacy.md", `---
type: standard
created: 2026-08-17
updated: 2026-08-17
tags: [standards]
author: agent
---

# Privacy
`)

	// Unknown type and author, a malformed tag, and a dead related link.
	write(t, v, "bad-metadata.md", `---
type: diary
created: 2026-08-17
updated: 2026-08-17
tags: [Not Valid]
related:
  - "[[Standards/nonexistent]]"
author: robot
---

# Bad Metadata
`)

	// No H1, and a dead wikilink in the prose.
	write(t, v, "no-heading.md", `---
type: session
created: 2026-08-17
updated: 2026-08-17
tags: [session]
author: agent
---

Prose referencing [[Missing/Note]] and [[Standards/privacy]].
`)

	// Not a note at all.
	write(t, v, "not-a-note.md", "# Just markdown\n")

	findings, err := v.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if !HasErrors(findings) {
		t.Fatal("HasErrors = false, want true")
	}

	tests := []struct{ path, want string }{
		{path: "bad-metadata.md", want: `unknown category "diary"`},
		{path: "bad-metadata.md", want: `unknown author "robot"`},
		{path: "bad-metadata.md", want: "not lowercase-hyphenated"},
		{path: "bad-metadata.md", want: `"Standards/nonexistent" does not exist`},
		{path: "no-heading.md", want: "no level-one heading"},
		{path: "no-heading.md", want: `"Missing/Note" does not exist`},
		{path: "not-a-note.md", want: "no frontmatter block"},
	}
	for _, tc := range tests {
		if !has(findings, tc.path, tc.want) {
			t.Errorf("no finding for %s containing %q; got:\n%s", tc.path, tc.want, join(findings))
		}
	}

	// The live link in prose is not reported.
	if has(findings, "no-heading.md", `"Standards/privacy" does not exist`) {
		t.Errorf("a resolvable link was reported dead:\n%s", join(findings))
	}

	// A dead link in prose is a warning; a dead related entry is an error.
	for _, f := range findings {
		if strings.Contains(f.Message, "Missing/Note") && f.Severity != schema.SeverityWarning {
			t.Errorf("prose link severity = %q, want warning", f.Severity)
		}
		if strings.Contains(f.Message, "Standards/nonexistent") && f.Severity != schema.SeverityError {
			t.Errorf("related link severity = %q, want error", f.Severity)
		}
	}
}

func TestLintResolvesLinksLikeObsidian(t *testing.T) {
	v := newVault(t)

	write(t, v, "Standards/privacy.md", "---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [standards]\nauthor: agent\n---\n\n# Privacy\n")
	write(t, v, "note.md", `---
type: session
created: 2026-08-17
updated: 2026-08-17
tags: [session]
author: agent
---

# Note

Full path [[Standards/privacy]], bare name [[privacy]], different case
[[standards/PRIVACY]], with extension [[Standards/privacy.md]], and with a
heading [[Standards/privacy#Rules]].
`)

	findings, err := v.Lint("note.md")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("all five link forms should resolve; got:\n%s", join(findings))
	}
}

func TestLintSpecificPaths(t *testing.T) {
	v := newVault(t)

	write(t, v, "clean.md", "---\ntype: glossary\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [glossary]\nauthor: agent\n---\n\n# Clean\n")
	write(t, v, "dirty.md", "---\ntype: glossary\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [glossary]\nauthor: nobody\n---\n\n# Dirty\n")

	findings, err := v.Lint("clean.md")
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("linting one clean note reported: %s", join(findings))
	}

	if findings, err = v.Lint("dirty.md"); err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if !HasErrors(findings) {
		t.Errorf("linting the dirty note found nothing: %s", join(findings))
	}
}

func TestLintOrdersFindings(t *testing.T) {
	v := newVault(t)

	for _, name := range []string{"c.md", "a.md", "b.md"} {
		write(t, v, name, "# no frontmatter\n")
	}

	findings, err := v.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3", len(findings))
	}
	for i, want := range []string{"a.md", "b.md", "c.md"} {
		if findings[i].Path != want {
			t.Errorf("finding %d path = %q, want %q", i, findings[i].Path, want)
		}
	}
}

func TestFindingString(t *testing.T) {
	tests := []struct {
		name string
		in   Finding
		want string
	}{
		{
			name: "field and line",
			in:   Finding{Path: "a.md", Line: 12, Field: "tags", Severity: schema.SeverityError, Message: "bad"},
			want: "a.md:12: error: tags: bad",
		},
		{
			name: "no line",
			in:   Finding{Path: "a.md", Field: "type", Severity: schema.SeverityError, Message: "required"},
			want: "a.md: error: type: required",
		},
		{
			name: "no field",
			in:   Finding{Path: "a.md", Severity: schema.SeverityWarning, Message: "no level-one heading"},
			want: "a.md: warning: no level-one heading",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func join(findings []Finding) string {
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = "  " + f.String()
	}
	return strings.Join(lines, "\n")
}
