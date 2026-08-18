package schema

import (
	"strings"
	"testing"
)

// fields lists the fields reported by a set of issues at a given severity.
func fields(issues []Issue, sev Severity) []string {
	var out []string
	for _, i := range issues {
		if i.Severity == sev {
			out = append(out, i.Field)
		}
	}
	return out
}

func date(t *testing.T, s string) Date {
	t.Helper()
	d, err := ParseDate(s)
	if err != nil {
		t.Fatalf("ParseDate(%q): %v", s, err)
	}
	return d
}

func TestFrontmatterValidate(t *testing.T) {
	valid := func() Frontmatter {
		return Frontmatter{
			Type:    TypeSession,
			Created: date(t, "2026-08-17"),
			Updated: date(t, "2026-08-17"),
			Tags:    []string{"agent", "semantic-search"},
			Author:  AuthorBoth,
			Status:  StatusInProgress,
		}
	}

	tests := []struct {
		name       string
		mutate     func(*Frontmatter)
		wantErrors []string
		wantWarns  []string
	}{
		{
			name:   "complete frontmatter is clean",
			mutate: func(*Frontmatter) {},
		},
		{
			name:       "type is required",
			mutate:     func(f *Frontmatter) { f.Type = "" },
			wantErrors: []string{"type"},
		},
		{
			name:       "type must be known",
			mutate:     func(f *Frontmatter) { f.Type = "diary" },
			wantErrors: []string{"type"},
		},
		{
			name:       "created is required",
			mutate:     func(f *Frontmatter) { f.Created = Date{} },
			wantErrors: []string{"created"},
		},
		{
			name:       "updated is required",
			mutate:     func(f *Frontmatter) { f.Updated = Date{} },
			wantErrors: []string{"updated"},
		},
		{
			name:       "updated may not precede created",
			mutate:     func(f *Frontmatter) { f.Updated = date(t, "2026-08-16") },
			wantErrors: []string{"updated"},
		},
		{
			name:   "updated may equal created",
			mutate: func(f *Frontmatter) { f.Updated = f.Created },
		},
		{
			name:       "author is required",
			mutate:     func(f *Frontmatter) { f.Author = "" },
			wantErrors: []string{"author"},
		},
		{
			name:       "author must be known",
			mutate:     func(f *Frontmatter) { f.Author = "robot" },
			wantErrors: []string{"author"},
		},
		{
			name:   "status is optional",
			mutate: func(f *Frontmatter) { f.Status = "" },
		},
		{
			name:       "status must be known when set",
			mutate:     func(f *Frontmatter) { f.Status = "wip" },
			wantErrors: []string{"status"},
		},
		{
			name:      "missing tags warn rather than fail",
			mutate:    func(f *Frontmatter) { f.Tags = nil },
			wantWarns: []string{"tags"},
		},
		{
			name:       "tags must be lowercase hyphenated",
			mutate:     func(f *Frontmatter) { f.Tags = []string{"Agent", "semantic search", "ok"} },
			wantErrors: []string{"tags", "tags"},
		},
		{
			name:      "duplicate tags warn",
			mutate:    func(f *Frontmatter) { f.Tags = []string{"agent", "agent"} },
			wantWarns: []string{"tags"},
		},
		{
			name:       "empty related entries fail",
			mutate:     func(f *Frontmatter) { f.Related = []string{"   "} },
			wantErrors: []string{"related"},
		},
		{
			name:      "duplicate related entries warn",
			mutate:    func(f *Frontmatter) { f.Related = []string{"Agent/OS/Memory", "Agent/OS/Memory"} },
			wantWarns: []string{"related"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm := valid()
			tc.mutate(&fm)

			issues := fm.Validate()
			gotErrors := fields(issues, SeverityError)
			gotWarns := fields(issues, SeverityWarning)

			if strings.Join(gotErrors, ",") != strings.Join(tc.wantErrors, ",") {
				t.Errorf("errors = %v, want %v (all issues: %+v)", gotErrors, tc.wantErrors, issues)
			}
			if strings.Join(gotWarns, ",") != strings.Join(tc.wantWarns, ",") {
				t.Errorf("warnings = %v, want %v (all issues: %+v)", gotWarns, tc.wantWarns, issues)
			}
		})
	}
}

func TestEnumsValid(t *testing.T) {
	for _, ty := range NoteTypes() {
		if !ty.Valid() {
			t.Errorf("NoteType %q reported invalid", ty)
		}
	}
	for _, a := range Authors() {
		if !a.Valid() {
			t.Errorf("Author %q reported invalid", a)
		}
	}
	for _, s := range Statuses() {
		if !s.Valid() {
			t.Errorf("Status %q reported invalid", s)
		}
	}
	if NoteType("").Valid() || Author("").Valid() || Status("").Valid() {
		t.Error("empty enum values reported valid")
	}
}
