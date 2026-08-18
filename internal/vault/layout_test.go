package vault

import (
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

func TestLayoutPath(t *testing.T) {
	l := DefaultLayout()
	on := mustDate(t, "2026-08-17")

	tests := []struct {
		name        string
		noteType    schema.NoteType
		scope, slug string
		date        schema.Date
		want        string
		wantErr     bool
	}{
		{
			name:     "session is filed by date",
			noteType: schema.TypeSession,
			slug:     "memory-toolset",
			date:     on,
			want:     "Sessions/2026/08/2026-08-17-memory-toolset.md",
		},
		{
			name:     "single digit month is padded",
			noteType: schema.TypeSession,
			slug:     "early",
			date:     mustDate(t, "2026-01-05"),
			want:     "Sessions/2026/01/2026-01-05-early.md",
		},
		{
			name:     "meeting shares the dated convention",
			noteType: schema.TypeMeeting,
			slug:     "planning",
			date:     on,
			want:     "Meetings/2026/08/2026-08-17-planning.md",
		},
		{
			name:     "interview shares the dated convention",
			noteType: schema.TypeInterview,
			slug:     "candidate",
			date:     on,
			want:     "Interviews/2026/08/2026-08-17-candidate.md",
		},
		{
			name:     "design is filed under its project",
			noteType: schema.TypeDesign,
			scope:    "gandalf",
			date:     on,
			want:     "Projects/gandalf/Design.md",
		},
		{
			name:     "decisions is filed under its project",
			noteType: schema.TypeDecisions,
			scope:    "gandalf",
			date:     on,
			want:     "Projects/gandalf/Decisions.md",
		},
		{
			name:     "standard is filed by slug",
			noteType: schema.TypeStandard,
			slug:     "language-go",
			date:     on,
			want:     "Standards/language-go.md",
		},
		{
			name:     "glossary is a single file",
			noteType: schema.TypeGlossary,
			date:     on,
			want:     "Glossary.md",
		},
		{
			name:     "dated note without a slug is refused",
			noteType: schema.TypeSession,
			date:     on,
			wantErr:  true,
		},
		{
			name:     "dated note without a date is refused",
			noteType: schema.TypeSession,
			slug:     "no-date",
			wantErr:  true,
		},
		{
			name:     "project note without a scope is refused",
			noteType: schema.TypeDesign,
			date:     on,
			wantErr:  true,
		},
		{
			name:     "unknown type is refused",
			noteType: "diary",
			slug:     "x",
			date:     on,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := l.Path(tc.noteType, tc.scope, tc.slug, tc.date)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Path() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Path(): %v", err)
			}
			if got != tc.want {
				t.Errorf("Path() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "Memory Toolset", want: "memory-toolset"},
		{in: "  Leading and trailing  ", want: "leading-and-trailing"},
		{in: "Punctuation: it's here!", want: "punctuation-it-s-here"},
		{in: "Already-slugged", want: "already-slugged"},
		{in: "Multiple   spaces", want: "multiple-spaces"},
		{in: "GandalfOS v1.2", want: "gandalfos-v1-2"},
		{in: "!!!", want: ""},
		{in: "", want: ""},
	}

	for _, tc := range tests {
		if got := Slugify(tc.in); got != tc.want {
			t.Errorf("Slugify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
