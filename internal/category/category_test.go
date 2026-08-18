package category

import (
	"strings"
	"testing"
	"time"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	on, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return on
}

func TestDefaultsAreValid(t *testing.T) {
	set := Defaults()
	if err := set.Validate(); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}

	// Interviews were folded into meetings: an interview is a meeting with a
	// narrower name, and a category nobody uses is clutter in every listing.
	if _, ok := set.Lookup("interview"); ok {
		t.Error("interview is still a default category")
	}

	for _, want := range []string{"session", "project", "standard", "meeting", "glossary"} {
		if _, ok := set.Lookup(want); !ok {
			t.Errorf("%s is missing from the defaults", want)
		}
	}
}

func TestCategoryValidate(t *testing.T) {
	valid := func() Category {
		return Category{
			Name: "incident", Plural: "incidents", Rule: RuleDated,
			Folder: "Incidents", Description: "Something broke.",
		}
	}

	tests := []struct {
		name   string
		mutate func(*Category)
		want   string
	}{
		{name: "a complete definition", mutate: func(*Category) {}},
		{name: "no name", mutate: func(c *Category) { c.Name = "" }, want: "needs a name"},
		{name: "no plural", mutate: func(c *Category) { c.Plural = "" }, want: "needs a plural"},
		{name: "capitals", mutate: func(c *Category) { c.Name = "Incident" }, want: "lowercase"},
		{name: "spaces", mutate: func(c *Category) { c.Name = "incident report" }, want: "lowercase"},
		{name: "unknown rule", mutate: func(c *Category) { c.Rule = "whenever" }, want: "unknown rule"},
		{name: "dated without a folder", mutate: func(c *Category) { c.Folder = "" }, want: "needs a folder"},
		{
			name:   "singleton without a filename",
			mutate: func(c *Category) { c.Rule = RuleSingleton; c.Folder = "Incidents" },
			want:   "ending in .md",
		},
		{
			name:   "scoped without facets",
			mutate: func(c *Category) { c.Rule = RuleScoped },
			want:   "at least one facet",
		},
		{
			name: "facets on an unscoped category",
			mutate: func(c *Category) {
				c.Facets = []Facet{{Name: "design", File: "Design.md"}}
			},
			want: "cannot have facets",
		},
		{
			name: "a facet without a markdown file",
			mutate: func(c *Category) {
				c.Rule = RuleScoped
				c.Facets = []Facet{{Name: "design", File: "Design"}}
			},
			want: "ending in .md",
		},
		{
			name: "duplicate facets",
			mutate: func(c *Category) {
				c.Rule = RuleScoped
				c.Facets = []Facet{{Name: "design", File: "A.md"}, {Name: "design", File: "B.md"}}
			},
			want: "duplicate facet",
		},
		{name: "a reserved name", mutate: func(c *Category) { c.Name = "path" }, want: "reserved"},
		{name: "a reserved plural", mutate: func(c *Category) { c.Plural = "all" }, want: "reserved"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.mutate(&c)

			err := c.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate(): %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	set := Defaults()
	on := date(t, "2026-08-17")

	tests := []struct {
		category    string
		scope, name string
		depth       Depth
		want        string
		wantErr     string
	}{
		{category: "session", name: "2026-08-17-work", depth: DepthMonth, want: "Sessions/2026/08/2026-08-17-work.md"},
		{category: "session", name: "2026-08-17-work", depth: DepthDay, want: "Sessions/2026/08/17/2026-08-17-work.md"},
		{category: "meeting", name: "2026-08-17-standup", depth: DepthMonth, want: "Meetings/2026/08/2026-08-17-standup.md"},
		{category: "project", scope: "gandalf", name: "design", want: "Projects/gandalf/Design.md"},
		{category: "project", scope: "gandalf", name: "todo", want: "Projects/gandalf/Todo.md"},
		{category: "standard", name: "language-go", want: "Standards/language-go.md"},
		{category: "glossary", want: "Glossary.md"},

		{category: "session", wantErr: "need a name"},
		{category: "project", name: "design", wantErr: "need a scope"},
		{category: "project", scope: "gandalf", wantErr: "need a facet"},
		{category: "project", scope: "gandalf", name: "roadmap", wantErr: "no facet"},
		{category: "readme", name: "root", wantErr: "filed explicitly"},
	}

	for _, tc := range tests {
		t.Run(tc.category+"/"+tc.name, func(t *testing.T) {
			cat, ok := set.Lookup(tc.category)
			if !ok {
				t.Fatalf("no category %q", tc.category)
			}

			got, err := cat.Path(tc.scope, tc.name, on, tc.depth)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Path() = %q, want an error mentioning %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
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

// TestOwnsInvertsPath is the property refs depend on: a path a category
// produced must be recognised as its own, with the same identifying parts.
func TestOwnsInvertsPath(t *testing.T) {
	set := Defaults()
	on := date(t, "2026-08-17")

	cases := []struct{ category, scope, name string }{
		{category: "session", name: "2026-08-17-work"},
		{category: "meeting", name: "2026-08-17-standup"},
		{category: "project", scope: "gandalf", name: "design"},
		{category: "project", scope: "gandalf", name: "decisions"},
		{category: "standard", name: "language-go"},
		{category: "glossary"},
	}

	for _, depth := range []Depth{DepthMonth, DepthDay} {
		for _, tc := range cases {
			t.Run(string(depth)+"/"+tc.category+"/"+tc.name, func(t *testing.T) {
				cat, ok := set.Lookup(tc.category)
				if !ok {
					t.Fatalf("no category %q", tc.category)
				}

				notePath, err := cat.Path(tc.scope, tc.name, on, depth)
				if err != nil {
					t.Fatalf("Path(): %v", err)
				}

				owner, scope, name, ok := set.Owner(notePath)
				if !ok {
					t.Fatalf("no category owns %q", notePath)
				}
				if owner.Name != tc.category || scope != tc.scope || name != tc.name {
					t.Errorf("Owner(%q) = %s/%s/%s, want %s/%s/%s",
						notePath, owner.Name, scope, name, tc.category, tc.scope, tc.name)
				}
			})
		}
	}
}

func TestOwnerRejectsUnconventionalPaths(t *testing.T) {
	set := Defaults()

	for _, notePath := range []string{
		"Dads Eulogy/On Dad.md",
		"Sessions/2026/08/no-date-prefix.md",
		"Projects/gandalf/Extra Notes.md",
		"Standards/nested/deeper.md",

		// A folder's README describes the folder rather than being a note of
		// the kind it holds. Standards/README.md answering to standard:README
		// would dress a folder description as an engineering standard.
		"Standards/README.md",
		"Sessions/README.md",
		"Projects/README.md",
	} {
		t.Run(notePath, func(t *testing.T) {
			if cat, _, _, ok := set.Owner(notePath); ok {
				t.Errorf("%q was claimed by %s", notePath, cat.Name)
			}
		})
	}
}

func TestSetMutation(t *testing.T) {
	set := Defaults()
	incident := Category{
		Name: "incident", Plural: "incidents", Rule: RuleDated,
		Folder: "Incidents", Description: "Something broke.",
	}

	if err := set.Add(incident); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, ok := set.Lookup("incident"); !ok {
		t.Fatal("the added category is missing")
	}

	tests := []struct {
		name string
		add  Category
		want string
	}{
		{
			name: "same name",
			add:  incident,
			want: "already exists",
		},
		{
			name: "plural collides with another name",
			add:  Category{Name: "note", Plural: "standard", Rule: RuleNamed, Folder: "Notes"},
			want: "collides",
		},
		{
			name: "folder already in use",
			add:  Category{Name: "note", Plural: "notes", Rule: RuleNamed, Folder: "Standards"},
			want: "already uses",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := set.Add(tc.add)
			if err == nil {
				t.Fatalf("Add() = nil, want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}

	if err := set.Remove("incident"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := set.Lookup("incident"); ok {
		t.Error("the removed category is still there")
	}
	if err := set.Remove("incident"); err == nil {
		t.Error("removing a category twice succeeded")
	}
}

func TestRetiredCategoriesAreNotCreatable(t *testing.T) {
	set := Defaults()

	cat, ok := set.Lookup("meeting")
	if !ok {
		t.Fatal("no meeting category")
	}
	cat.Retired = true
	if err := set.Replace("meeting", cat); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	for _, name := range set.CreatableNames() {
		if name == "meeting" {
			t.Error("a retired category is still offered for creation")
		}
	}

	// Notes already filed under it stay addressable, which is the difference
	// between retiring and deleting.
	if _, ok := set.Lookup("meeting"); !ok {
		t.Error("a retired category disappeared entirely")
	}
	found := false
	for _, name := range set.Names() {
		if name == "meeting" {
			found = true
		}
	}
	if !found {
		t.Error("a retired category is missing from Names")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "Memory Toolset", want: "memory-toolset"},
		{in: "  Leading and trailing  ", want: "leading-and-trailing"},
		{in: "Punctuation: it's here!", want: "punctuation-it-s-here"},
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

func TestDatedName(t *testing.T) {
	if got := DatedName(date(t, "2026-08-17"), "work"); got != "2026-08-17-work" {
		t.Errorf("DatedName() = %q, want 2026-08-17-work", got)
	}
}
