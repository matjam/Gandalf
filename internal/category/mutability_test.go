package category

import "testing"

// TestDefaultMutability pins the shipped answer for each category, because
// getting one of these backwards either lets an agent compact a record or
// blocks an edit the memory protocol asks for.
func TestDefaultMutability(t *testing.T) {
	tests := []struct {
		category string
		facet    string
		want     Mutability
	}{
		{category: "session", want: AppendOnly},
		{category: "meeting", want: AppendOnly},
		{category: "standard", want: Replaceable},
		{category: "glossary", want: Replaceable},
		{category: "project", facet: "design", want: Replaceable},
		{category: "project", facet: "todo", want: Replaceable},
		{category: "project", facet: "decisions", want: AppendOnly},
	}

	set := Defaults()
	for _, tt := range tests {
		name := tt.category
		if tt.facet != "" {
			name += ":" + tt.facet
		}
		t.Run(name, func(t *testing.T) {
			cat, ok := set.Lookup(tt.category)
			if !ok {
				t.Fatalf("no category %q", tt.category)
			}
			if got := cat.MutabilityOf(tt.facet); got != tt.want {
				t.Errorf("MutabilityOf(%q) = %q, want %q", tt.facet, got, tt.want)
			}
		})
	}
}

// TestMutabilityInheritedFromShipped covers a vault whose categories.json was
// written before the field existed. Those declarations say nothing, and
// treating silence as append-only would make every design note and standard
// unwritable on upgrade.
func TestMutabilityInheritedFromShipped(t *testing.T) {
	old := Category{
		Name:        "project",
		Plural:      "projects",
		Rule:        RuleScoped,
		Folder:      "Projects",
		Description: "as seeded before mutability existed",
		Facets: []Facet{
			{Name: "design", File: "Design.md"},
			{Name: "decisions", File: "Decisions.md"},
			{Name: "todo", File: "Todo.md"},
		},
	}

	if got := old.MutabilityOf("design"); got != Replaceable {
		t.Errorf("design = %q, want %q", got, Replaceable)
	}
	if got := old.MutabilityOf("decisions"); got != AppendOnly {
		t.Errorf("decisions = %q, want %q", got, AppendOnly)
	}
}

// TestMutabilityUnknownCategoryIsAppendOnly is the safe direction: a category
// Gandalf never shipped and the vault says nothing about keeps its contents.
func TestMutabilityUnknownCategoryIsAppendOnly(t *testing.T) {
	c := Category{Name: "incident", Plural: "incidents", Rule: RuleDated, Folder: "Incidents", Description: "x"}

	if got := c.MutabilityOf(""); got != AppendOnly {
		t.Errorf("MutabilityOf = %q, want %q", got, AppendOnly)
	}
}

// TestMutabilityDeclarationWins: the vault outranks the shipped default, the
// same way its standards do.
func TestMutabilityDeclarationWins(t *testing.T) {
	c := Category{
		Name: "session", Plural: "sessions", Rule: RuleDated, Folder: "Sessions",
		Description: "x", Mutability: Replaceable,
	}
	if got := c.MutabilityOf(""); got != Replaceable {
		t.Errorf("MutabilityOf = %q, want %q", got, Replaceable)
	}

	scoped := Category{
		Name: "project", Plural: "projects", Rule: RuleScoped, Folder: "Projects",
		Description: "x", Mutability: AppendOnly,
		Facets: []Facet{{Name: "design", File: "Design.md", Mutability: Replaceable}},
	}
	if got := scoped.MutabilityOf("design"); got != Replaceable {
		t.Errorf("facet override = %q, want %q", got, Replaceable)
	}
	if got := scoped.MutabilityOf(""); got != AppendOnly {
		t.Errorf("category default = %q, want %q", got, AppendOnly)
	}
}

func TestMutabilityValidation(t *testing.T) {
	c := Category{Name: "notes", Plural: "notes-list", Rule: RuleNamed, Folder: "Notes",
		Description: "x", Mutability: Mutability("sometimes")}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown category mutability")
	}

	scoped := Category{Name: "project", Plural: "projects", Rule: RuleScoped, Folder: "Projects",
		Description: "x",
		Facets:      []Facet{{Name: "design", File: "Design.md", Mutability: Mutability("maybe")}}}
	if err := scoped.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown facet mutability")
	}
}
