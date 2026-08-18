package vault

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/category"
)

func TestParseRef(t *testing.T) {
	v := newVault(t)

	tests := []struct {
		name    string
		in      string
		want    Ref
		wantErr string
	}{
		{name: "session", in: "session:2026-08-17-memory-toolset",
			want: Ref{Kind: "session", Name: "2026-08-17-memory-toolset"}},
		{name: "meeting", in: "meeting:2026-08-17-planning",
			want: Ref{Kind: "meeting", Name: "2026-08-17-planning"}},
		{name: "project design", in: "project:gandalf:design",
			want: Ref{Kind: "project", Scope: "gandalf", Name: "design"}},
		{name: "project todo", in: "project:gandalf:todo",
			want: Ref{Kind: "project", Scope: "gandalf", Name: "todo"}},
		{name: "standard", in: "standard:language-go",
			want: Ref{Kind: "standard", Name: "language-go"}},
		{name: "glossary", in: "glossary",
			want: Ref{Kind: "glossary"}},
		{name: "path with spaces", in: "path:Dads Eulogy/On Dad",
			want: Ref{Kind: KindPath, Name: "Dads Eulogy/On Dad"}},
		{name: "surrounding space is ignored", in: "  standard:language-go  ",
			want: Ref{Kind: "standard", Name: "language-go"}},
		{name: "latest is parsed, resolved later", in: "session:latest",
			want: Ref{Kind: "session", Name: Latest}},

		{name: "unknown category", in: "diary:today", wantErr: "unknown category"},
		{name: "empty", in: "", wantErr: "required"},
		{name: "session without a name", in: "session", wantErr: "want session:<name>"},
		{name: "session with an empty name", in: "session:", wantErr: "want session:<name>"},
		{name: "glossary with a name", in: "glossary:terms", wantErr: "no further fields"},
		{name: "project without a facet", in: "project:gandalf", wantErr: "want project:<scope>"},
		{name: "project with an unknown facet", in: "project:gandalf:roadmap", wantErr: `no "roadmap"`},
		{name: "project with an empty scope", in: "project::design", wantErr: "want project:<scope>"},
		{name: "path without a path", in: "path:", wantErr: "want path:"},
		{name: "a readme is filed explicitly", in: "readme:root", wantErr: "filed explicitly"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := v.ParseRef(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseRef(%q) = %+v, want error", tc.in, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
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
	v := newVault(t)

	for _, in := range []string{
		"session:2026-08-17-memory-toolset",
		"project:gandalf:decisions",
		"standard:language-go",
		"glossary",
		"path:Dads Eulogy/On Dad",
	} {
		t.Run(in, func(t *testing.T) {
			ref, err := v.ParseRef(in)
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
	v := newVault(t)

	tests := []struct {
		ref     string
		want    string
		wantErr bool
	}{
		{ref: "session:2026-08-17-memory-toolset", want: "Sessions/2026/08/2026-08-17-memory-toolset.md"},
		{ref: "meeting:2026-01-05-standup", want: "Meetings/2026/01/2026-01-05-standup.md"},
		{ref: "project:gandalf:design", want: "Projects/gandalf/Design.md"},
		{ref: "project:gandalf:decisions", want: "Projects/gandalf/Decisions.md"},
		{ref: "project:gandalf:todo", want: "Projects/gandalf/Todo.md"},
		{ref: "standard:language-go", want: "Standards/language-go.md"},
		{ref: "glossary", want: "Glossary.md"},
		{ref: "path:Dads Eulogy/On Dad", want: "Dads Eulogy/On Dad.md"},
		{ref: "path:Dads Eulogy/On Dad.md", want: "Dads Eulogy/On Dad.md"},

		{ref: "session:latest", wantErr: true},
		{ref: "session:not-a-date-slug", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			ref, err := v.ParseRef(tc.ref)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}

			got, err := v.Resolve(ref)
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

// TestRefForInvertsResolve is the property listings and lint depend on: a path
// they report must come back as a ref addressing the same note.
func TestRefForInvertsResolve(t *testing.T) {
	v := newVault(t)

	for _, in := range []string{
		"session:2026-08-17-memory-toolset",
		"meeting:2026-08-17-planning",
		"project:gandalf:design",
		"project:gandalf:decisions",
		"project:gandalf:todo",
		"standard:language-go",
		"glossary",
	} {
		t.Run(in, func(t *testing.T) {
			ref, err := v.ParseRef(in)
			if err != nil {
				t.Fatalf("ParseRef: %v", err)
			}
			notePath, err := v.Resolve(ref)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got := v.RefFor(notePath); got != ref {
				t.Errorf("RefFor(%q) = %+v, want %+v", notePath, got, ref)
			}
		})
	}
}

func TestRefForUnconventionalPaths(t *testing.T) {
	v := newVault(t)

	tests := []struct{ path, want string }{
		{path: "Dads Eulogy/On Dad.md", want: "path:Dads Eulogy/On Dad"},
		{path: "docs/plans/some-plan.md", want: "path:docs/plans/some-plan"},
		{path: "Sessions/2026/08/no-date-prefix.md", want: "path:Sessions/2026/08/no-date-prefix"},
		{path: "Projects/gandalf/Extra Notes.md", want: "path:Projects/gandalf/Extra Notes"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := v.RefFor(tc.path)
			if got.String() != tc.want {
				t.Errorf("RefFor(%q) = %q, want %q", tc.path, got, tc.want)
			}
			if got.Writable() {
				t.Error("a path ref should not be writable")
			}
		})
	}
}

// TestPathsAreRejectedWithGuidance checks the error teaches the scheme using
// this vault's own categories, since examples from the shipped defaults would
// be wrong in a vault that had changed them.
func TestPathsAreRejectedWithGuidance(t *testing.T) {
	v := newVault(t)

	_, err := v.ParseRef("Standards/language-go.md")
	if err == nil {
		t.Fatal("a path was accepted as a ref")
	}
	for _, want := range []string{"file path", "addressed by ref", "session:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestLatest(t *testing.T) {
	v := newVault(t)

	for _, slug := range []string{"2026-08-16-earlier", "2026-08-17-later"} {
		n, err := v.NewNote(NewNoteRequest{
			Type: typeSession, Title: slug, Name: slug, Tags: []string{"work"},
			On: mustDate(t, slug[:10]),
		})
		if err != nil {
			t.Fatalf("NewNote: %v", err)
		}
		if err := v.Write(n); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	ref, err := v.ParseRef("session:latest")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	got, err := v.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "Sessions/2026/08/2026-08-17-later.md"; got != want {
		t.Errorf("latest resolved to %q, want %q", got, want)
	}
}

func TestLatestWithNothingToFind(t *testing.T) {
	v := newVault(t)

	ref, err := v.ParseRef("session:latest")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	if _, err := v.Resolve(ref); err == nil {
		t.Error("latest resolved in an empty vault")
	}
}

func TestDepth(t *testing.T) {
	v := newVault(t)
	on := mustDate(t, "2026-08-07")

	cat, ok := v.Categories().Lookup("session")
	if !ok {
		t.Fatal("no session category")
	}

	got, err := cat.Path("", "2026-08-07-work", on.Time(), category.DepthMonth)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "Sessions/2026/08/2026-08-07-work.md"; got != want {
		t.Errorf("month depth = %q, want %q", got, want)
	}

	got, err = cat.Path("", "2026-08-07-work", on.Time(), category.DepthDay)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := "Sessions/2026/08/07/2026-08-07-work.md"; got != want {
		t.Errorf("day depth = %q, want %q", got, want)
	}
}
