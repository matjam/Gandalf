// Package category defines the kinds of note a vault holds and how each is
// filed.
//
// Categories are data, not code. Gandalf ships a set of defaults and tells
// users the standards are theirs to rewrite; the shape of the vault has to be
// theirs on the same terms, or that claim only holds for the parts nobody
// wants to change.
//
// Filing rules stay a closed set, because they are behaviour rather than
// naming. An open set of categories over a closed set of rules keeps
// validation strict: a typo is still rejected, because a category name is
// checked against what the vault declares.
package category

import (
	"fmt"
	"slices"
	"strings"
)

// Rule is how notes of a category are addressed and filed.
type Rule string

const (
	// RuleDated files a note under year and month directories, named for the
	// day it was created: kind:YYYY-MM-DD-slug.
	RuleDated Rule = "dated"

	// RuleScoped files a note under a named scope, one file per facet:
	// kind:scope:facet.
	RuleScoped Rule = "scoped"

	// RuleNamed files a note by slug in a single folder: kind:slug.
	RuleNamed Rule = "named"

	// RuleSingleton is a single well-known file: kind.
	RuleSingleton Rule = "singleton"

	// RuleExplicit covers notes whose location cannot be derived, such as a
	// folder's README. They are not created through the note tools and are
	// reached by path.
	RuleExplicit Rule = "explicit"
)

// Rules returns every filing rule.
func Rules() []Rule {
	return []Rule{RuleDated, RuleScoped, RuleNamed, RuleSingleton, RuleExplicit}
}

// Valid reports whether r is a known rule.
func (r Rule) Valid() bool { return slices.Contains(Rules(), r) }

// Category is one kind of note: what it is called, how it is filed, and what a
// new one starts out looking like.
type Category struct {
	// Name is the singular form, used as a note's type and as the first field
	// of a ref.
	Name string `json:"name"`

	// Plural is what listings are asked for.
	Plural string `json:"plural"`

	Rule Rule `json:"rule"`

	// Folder is where notes of this category live. For a singleton it is the
	// file itself.
	Folder string `json:"folder,omitempty"`

	// Facets maps each facet of a scoped category to its filename, in the
	// order they should be offered.
	Facets []Facet `json:"facets,omitempty"`

	// Description says what belongs here. It is shown to a model deciding
	// where to put something.
	Description string `json:"description"`

	// Tags are applied to every note created in this category.
	Tags []string `json:"tags,omitempty"`

	// Template is the body a new note starts with, below its heading.
	Template string `json:"template,omitempty"`

	// Retired hides a category from creation and from listings without
	// orphaning the notes already filed under it, which stay addressable.
	Retired bool `json:"retired,omitempty"`
}

// Facet is one of the notes a scoped category holds.
type Facet struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// Creatable reports whether new notes of this category can be made through the
// tools.
func (c Category) Creatable() bool {
	return !c.Retired && c.Rule != RuleExplicit
}

// FacetFile returns the filename for a facet.
func (c Category) FacetFile(name string) (string, bool) {
	for _, f := range c.Facets {
		if strings.EqualFold(f.Name, name) {
			return f.File, true
		}
	}
	return "", false
}

// FacetNames lists a scoped category's facets.
func (c Category) FacetNames() []string {
	out := make([]string, 0, len(c.Facets))
	for _, f := range c.Facets {
		out = append(out, f.Name)
	}
	return out
}

// Reserved names cannot be categories: refs use them for things the vault does
// not file. Taking one would make those refs ambiguous.
var Reserved = []string{"path", "topic", "all", "latest"}

// Validate reports what is wrong with a category definition.
func (c Category) Validate() error {
	for _, reserved := range Reserved {
		if strings.EqualFold(c.Name, reserved) || strings.EqualFold(c.Plural, reserved) {
			return fmt.Errorf("%q is reserved and cannot name a category", reserved)
		}
	}

	switch {
	case c.Name == "":
		return fmt.Errorf("a category needs a name")
	case !nameOK(c.Name):
		return fmt.Errorf("category name %q must be lowercase letters, digits, and hyphens", c.Name)
	case c.Plural == "":
		return fmt.Errorf("category %q needs a plural", c.Name)
	case !nameOK(c.Plural):
		return fmt.Errorf("category plural %q must be lowercase letters, digits, and hyphens", c.Plural)
	case !c.Rule.Valid():
		return fmt.Errorf("category %q has unknown rule %q (want one of %s)", c.Name, c.Rule, ruleList())
	}

	switch c.Rule {
	case RuleDated, RuleNamed, RuleScoped:
		if c.Folder == "" {
			return fmt.Errorf("category %q is filed by %s and needs a folder", c.Name, c.Rule)
		}
	case RuleSingleton:
		if c.Folder == "" || !strings.HasSuffix(c.Folder, ".md") {
			return fmt.Errorf("singleton category %q needs a folder naming its file, ending in .md", c.Name)
		}
	}

	if c.Rule == RuleScoped {
		if len(c.Facets) == 0 {
			return fmt.Errorf("scoped category %q needs at least one facet", c.Name)
		}
		seen := map[string]bool{}
		for _, f := range c.Facets {
			switch {
			case !nameOK(f.Name):
				return fmt.Errorf("category %q: facet name %q must be lowercase letters, digits, and hyphens", c.Name, f.Name)
			case !strings.HasSuffix(f.File, ".md"):
				return fmt.Errorf("category %q: facet %q needs a filename ending in .md", c.Name, f.Name)
			case seen[f.Name]:
				return fmt.Errorf("category %q: duplicate facet %q", c.Name, f.Name)
			}
			seen[f.Name] = true
		}
	} else if len(c.Facets) > 0 {
		return fmt.Errorf("category %q is filed by %s, so it cannot have facets", c.Name, c.Rule)
	}

	return nil
}

// nameOK reports whether a name is safe to use in a ref and on a filesystem.
func nameOK(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// ruleList renders the rules for an error message.
func ruleList() string {
	names := make([]string, 0, len(Rules()))
	for _, r := range Rules() {
		names = append(names, string(r))
	}
	return strings.Join(names, ", ")
}
