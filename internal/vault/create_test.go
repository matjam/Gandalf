package vault

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/schema"
)

// newVault returns a vault rooted in a temporary directory.
func newVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return v
}

func TestNewNote(t *testing.T) {
	v := newVault(t)
	on := mustDate(t, "2026-08-17")

	n, err := v.NewNote(NewNoteRequest{
		Type:    typeSession,
		Title:   "Memory Toolset Design",
		Tags:    []string{"agent", "memory"},
		Related: []string{"[[Agent/OS/Memory]]", "Standards/privacy"},
		Author:  schema.AuthorBoth,
		Status:  schema.StatusInProgress,
		On:      on,
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}

	if want := "Sessions/2026/08/2026-08-17-memory-toolset-design.md"; n.Path != want {
		t.Errorf("path = %q, want %q", n.Path, want)
	}
	if issues := n.FM.Validate(); len(issues) > 0 {
		t.Errorf("generated frontmatter is invalid: %+v", issues)
	}
	if !n.FM.Created.Equal(on) || !n.FM.Updated.Equal(on) {
		t.Errorf("created/updated = %s/%s, want %s", n.FM.Created, n.FM.Updated, on)
	}
	if got := strings.Join(n.FM.Related, ","); got != "Agent/OS/Memory,Standards/privacy" {
		t.Errorf("related = %q, want delimiters stripped from both entries", got)
	}
	if n.Title() != "Memory Toolset Design" {
		t.Errorf("title = %q, want the requested title", n.Title())
	}
	if !strings.Contains(n.Body, "## Goal") {
		t.Errorf("session template missing its sections:\n%s", n.Body)
	}
}

func TestNewNoteDefaults(t *testing.T) {
	v := newVault(t)

	n, err := v.NewNote(NewNoteRequest{
		Type:  typeStandard,
		Title: "Language Go",
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}

	if n.FM.Author != schema.AuthorAgent {
		t.Errorf("author = %q, want the agent default", n.FM.Author)
	}
	if n.FM.Created.IsZero() || !n.FM.Created.Equal(schema.Today()) {
		t.Errorf("created = %q, want today", n.FM.Created)
	}
	if n.FM.Status != "" {
		t.Errorf("status = %q, want it left unset", n.FM.Status)
	}
	if n.Path != "Standards/language-go.md" {
		t.Errorf("path = %q, want Standards/language-go.md", n.Path)
	}
}

func TestNewNoteBodyHandling(t *testing.T) {
	v := newVault(t)

	tests := []struct {
		name     string
		body     string
		wantHead string
		wantOnce string
	}{
		{
			name:     "supplied body gains the title heading",
			body:     "Some prose.",
			wantHead: "# Custom\n\nSome prose.\n",
		},
		{
			name:     "body that already has a heading is left alone",
			body:     "# Their Own Heading\n\nProse.",
			wantHead: "# Their Own Heading\n\nProse.\n",
			wantOnce: "# ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := v.NewNote(NewNoteRequest{
				Type:  typeStandard,
				Title: "Custom",
				Body:  tc.body,
			})
			if err != nil {
				t.Fatalf("NewNote: %v", err)
			}
			if n.Body != tc.wantHead {
				t.Errorf("body = %q, want %q", n.Body, tc.wantHead)
			}
			if tc.wantOnce != "" && strings.Count(n.Body, tc.wantOnce) != 1 {
				t.Errorf("body has %d headings, want 1:\n%s", strings.Count(n.Body, tc.wantOnce), n.Body)
			}
		})
	}
}

func TestNewNoteExplicitPathBypassesRouting(t *testing.T) {
	v := newVault(t)

	n, err := v.NewNote(NewNoteRequest{
		Type:  typeProject,
		Title: "Design",
		Path:  "somewhere/else/Design.md",
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if n.Path != "somewhere/else/Design.md" {
		t.Errorf("path = %q, want the explicit path", n.Path)
	}
}

func TestNewNoteRejects(t *testing.T) {
	v := newVault(t)

	tests := []struct {
		name string
		req  NewNoteRequest
	}{
		{name: "unknown type", req: NewNoteRequest{Type: "diary", Title: "x"}},
		{name: "missing title", req: NewNoteRequest{Type: typeSession}},
		{name: "blank title", req: NewNoteRequest{Type: typeSession, Title: "   "}},
		{name: "unknown author", req: NewNoteRequest{Type: typeSession, Title: "x", Author: "robot"}},
		{name: "unknown status", req: NewNoteRequest{Type: typeSession, Title: "x", Status: "wip"}},
		{name: "project note without scope", req: NewNoteRequest{Type: typeProject, Title: "x"}},
		{name: "title with no slug characters", req: NewNoteRequest{Type: typeSession, Title: "!!!"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if n, err := v.NewNote(tc.req); err == nil {
				t.Errorf("NewNote() = %+v, want error", n)
			}
		})
	}
}

// TestEveryCategoryProducesAValidNote walks whatever categories the vault
// declares, so adding one cannot quietly ship a template that fails the schema
// it is supposed to satisfy.
func TestEveryCategoryProducesAValidNote(t *testing.T) {
	v := newVault(t)

	for _, cat := range v.Categories().Categories {
		t.Run(cat.Name, func(t *testing.T) {
			req := NewNoteRequest{
				Type:  schema.NoteType(cat.Name),
				Title: "Example Note",
				Scope: "example",
				Tags:  []string{"example"},
			}
			switch cat.Rule {
			case category.RuleScoped:
				req.Name = cat.FacetNames()[0]
			case category.RuleExplicit:
				// Filed by the folder it documents, which no rule can derive.
				req.Path = "example/README.md"
			}

			n, err := v.NewNote(req)
			if err != nil {
				t.Fatalf("NewNote: %v", err)
			}
			if issues := n.FM.Validate(); len(issues) > 0 {
				t.Errorf("frontmatter invalid: %+v", issues)
			}
			if n.Title() == "" {
				t.Errorf("template produced no H1:\n%s", n.Body)
			}

			// Whatever the template contains must survive a round trip.
			reparsed, err := ParseNote(n.Path, n.Render())
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if string(reparsed.Render()) != string(n.Render()) {
				t.Error("note does not survive a render/parse round trip")
			}
		})
	}
}
