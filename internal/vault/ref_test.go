package vault

import (
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Ref
		wantErr bool
	}{
		{name: "session", in: "session:2026-08-17-memory-toolset",
			want: Ref{Kind: KindSession, Name: "2026-08-17-memory-toolset"}},
		{name: "meeting", in: "meeting:2026-08-17-planning",
			want: Ref{Kind: KindMeeting, Name: "2026-08-17-planning"}},
		{name: "project design", in: "project:gandalf:design",
			want: Ref{Kind: KindProject, Scope: "gandalf", Name: "design"}},
		{name: "project todo", in: "project:gandalf:todo",
			want: Ref{Kind: KindProject, Scope: "gandalf", Name: "todo"}},
		{name: "standard", in: "standard:language-go",
			want: Ref{Kind: KindStandard, Name: "language-go"}},
		{name: "topic", in: "topic:shipping",
			want: Ref{Kind: KindTopic, Name: "shipping"}},
		{name: "glossary", in: "glossary",
			want: Ref{Kind: KindGlossary}},
		{name: "path with spaces", in: "path:Dads Eulogy/On Dad",
			want: Ref{Kind: KindPath, Name: "Dads Eulogy/On Dad"}},
		{name: "surrounding space is ignored", in: "  standard:language-go  ",
			want: Ref{Kind: KindStandard, Name: "language-go"}},
		{name: "latest is parsed, resolved later", in: "session:latest",
			want: Ref{Kind: KindSession, Name: Latest}},

		{name: "unknown kind", in: "diary:today", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "session without a name", in: "session", wantErr: true},
		{name: "session with an empty name", in: "session:", wantErr: true},
		{name: "glossary with a name", in: "glossary:terms", wantErr: true},
		{name: "project without a facet", in: "project:gandalf", wantErr: true},
		{name: "project with an unknown facet", in: "project:gandalf:roadmap", wantErr: true},
		{name: "project with an empty scope", in: "project::design", wantErr: true},
		{name: "path without a path", in: "path:", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRef(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRef(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRefRoundTrip(t *testing.T) {
	for _, in := range []string{
		"session:2026-08-17-memory-toolset",
		"project:gandalf:decisions",
		"standard:language-go",
		"topic:shipping",
		"glossary",
		"path:Dads Eulogy/On Dad",
	} {
		t.Run(in, func(t *testing.T) {
			ref, err := ParseRef(in)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}
			if got := ref.String(); got != in {
				t.Errorf("String() = %q, want %q", got, in)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	l := DefaultLayout()

	tests := []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{ref: "session:2026-08-17-memory-toolset", want: "Sessions/2026/08/2026-08-17-memory-toolset.md"},
		{ref: "meeting:2026-01-05-standup", want: "Meetings/2026/01/2026-01-05-standup.md"},
		{ref: "interview:2026-08-17-candidate", want: "Interviews/2026/08/2026-08-17-candidate.md"},
		{ref: "project:gandalf:design", want: "Projects/gandalf/Design.md"},
		{ref: "project:gandalf:decisions", want: "Projects/gandalf/Decisions.md"},
		{ref: "project:gandalf:todo", want: "Projects/gandalf/Todo.md"},
		{ref: "standard:language-go", want: "Standards/language-go.md"},
		{ref: "glossary", want: "Glossary.md"},
		{ref: "path:Dads Eulogy/On Dad", want: "Dads Eulogy/On Dad.md"},
		{ref: "path:Dads Eulogy/On Dad.md", want: "Dads Eulogy/On Dad.md"},

		{ref: "topic:shipping", wantErr: true},
		{ref: "session:latest", wantErr: true},
		{ref: "session:not-a-date-slug", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			ref, err := ParseRef(tc.ref)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}

			got, err := l.Resolve(ref)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Resolve() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(): %v", err)
			}
			if got != tc.want {
				t.Errorf("Resolve() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRefForInvertsResolve is the property search and lint depend on: a path
// they report must come back as a ref that addresses the same note.
func TestRefForInvertsResolve(t *testing.T) {
	l := DefaultLayout()

	for _, in := range []string{
		"session:2026-08-17-memory-toolset",
		"meeting:2026-08-17-planning",
		"interview:2026-08-17-candidate",
		"project:gandalf:design",
		"project:gandalf:decisions",
		"project:gandalf:todo",
		"standard:language-go",
		"glossary",
	} {
		t.Run(in, func(t *testing.T) {
			ref, err := ParseRef(in)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}
			notePath, err := l.Resolve(ref)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got := l.RefFor(notePath); got != ref {
				t.Errorf("RefFor(%q) = %+v, want %+v", notePath, got, ref)
			}
		})
	}
}

func TestRefForUnconventionalPaths(t *testing.T) {
	l := DefaultLayout()

	tests := []struct{ path, want string }{
		{path: "Dads Eulogy/On Dad.md", want: "path:Dads Eulogy/On Dad"},
		{path: "docs/plans/some-plan.md", want: "path:docs/plans/some-plan"},
		{path: "Sessions/2026/08/no-date-prefix.md", want: "path:Sessions/2026/08/no-date-prefix"},
		{path: "Projects/gandalf/Extra Notes.md", want: "path:Projects/gandalf/Extra Notes"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := l.RefFor(tc.path)
			if got.String() != tc.want {
				t.Errorf("RefFor(%q) = %q, want %q", tc.path, got, tc.want)
			}
			if got.Writable() {
				t.Error("a path ref should not be writable")
			}
		})
	}
}

func TestRefWritable(t *testing.T) {
	for _, in := range []string{"session:2026-08-17-x", "project:p:design", "standard:s", "glossary"} {
		ref, err := ParseRef(in)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", in, err)
		}
		if !ref.Writable() {
			t.Errorf("%q should be writable", in)
		}
	}
}

func TestRefType(t *testing.T) {
	tests := []struct {
		ref  string
		want schema.NoteType
	}{
		{ref: "session:2026-08-17-x", want: schema.TypeSession},
		{ref: "meeting:2026-08-17-x", want: schema.TypeMeeting},
		{ref: "interview:2026-08-17-x", want: schema.TypeInterview},
		{ref: "project:p:design", want: schema.TypeDesign},
		{ref: "project:p:decisions", want: schema.TypeDecisions},
		{ref: "project:p:todo", want: schema.TypeTodo},
		{ref: "standard:s", want: schema.TypeStandard},
		{ref: "topic:shipping", want: schema.TypeStandard},
		{ref: "glossary", want: schema.TypeGlossary},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			ref, err := ParseRef(tc.ref)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}
			got, err := ref.Type()
			if err != nil {
				t.Fatalf("Type(): %v", err)
			}
			if got != tc.want {
				t.Errorf("Type() = %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := (Ref{Kind: KindPath, Name: "x"}).Type(); err == nil {
		t.Error("a path ref should have no note type")
	}
}

func TestLayoutDepth(t *testing.T) {
	on, err := schema.ParseDate("2026-08-07")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}

	month := DefaultLayout()
	got, err := month.Path(schema.TypeSession, "", "work", on)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "Sessions/2026/08/2026-08-07-work.md"; got != want {
		t.Errorf("month depth = %q, want %q", got, want)
	}

	day := DefaultLayout()
	day.SessionDepth = DepthDay
	got, err = day.Path(schema.TypeSession, "", "work", on)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "Sessions/2026/08/07/2026-08-07-work.md"; got != want {
		t.Errorf("day depth = %q, want %q", got, want)
	}

	// Refs must still resolve under the deeper layout.
	ref, err := ParseRef("session:2026-08-07-work")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	resolved, err := day.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != got {
		t.Errorf("Resolve() = %q, want %q", resolved, got)
	}
	if back := day.RefFor(resolved); back != ref {
		t.Errorf("RefFor(%q) = %+v, want %+v", resolved, back, ref)
	}
}
