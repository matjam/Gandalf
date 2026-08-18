package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
)

// CategoryView describes one category, with enough detail to decide where a
// note belongs and how to address it.
type CategoryView struct {
	Name        string   `json:"name"`
	Plural      string   `json:"plural"`
	Rule        string   `json:"rule"`
	Folder      string   `json:"folder,omitempty"`
	Facets      []string `json:"facets,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"default_tags,omitempty"`
	Retired     bool     `json:"retired,omitempty"`

	// Mutability says whether these notes may be rewritten in place or only
	// added to.
	Mutability string `json:"mutability"`

	// FacetMutability names the facets that answer differently from the
	// category as a whole, so the exception is visible without trying it.
	FacetMutability map[string]string `json:"facet_mutability,omitempty"`

	// RefForm shows how notes of this category are addressed.
	RefForm string `json:"ref_form"`

	// Notes is how many notes the vault currently holds here.
	Notes int `json:"notes"`
}

// CategoryListInput takes no arguments.
type CategoryListInput struct{}

// CategoryListOutput is the vault's declared categories.
type CategoryListOutput struct {
	Categories []CategoryView `json:"categories"`
}

// categoryList reports what kinds of note this vault holds.
func (s *Server) categoryList(ctx context.Context, _ *sdk.CallToolRequest, _ CategoryListInput) (*sdk.CallToolResult, CategoryListOutput, error) {
	counts, err := s.categoryCounts()
	if err != nil {
		return nil, CategoryListOutput{}, err
	}

	var out CategoryListOutput
	for _, c := range s.vault.Categories().Categories {
		out.Categories = append(out.Categories, view(c, counts[c.Name]))
	}

	return nil, out, nil
}

// CategoryCreateInput declares a new kind of note.
type CategoryCreateInput struct {
	Name   string `json:"name" jsonschema:"singular, lowercase and hyphenated; used as the first field of a ref"`
	Plural string `json:"plural" jsonschema:"what gandalf_list is asked for"`

	Rule string `json:"rule" jsonschema:"dated for notes filed by day, scoped for notes grouped under a name, named for one file per slug, or singleton for a single file"`

	Folder string `json:"folder" jsonschema:"the directory notes are filed in; for a singleton, the filename itself"`

	Facets []string `json:"facets,omitempty" jsonschema:"for scoped categories, the notes each scope holds, such as design and decisions"`

	Description string   `json:"description" jsonschema:"what belongs here; shown when deciding where to put a note"`
	Tags        []string `json:"default_tags,omitempty" jsonschema:"tags applied to every note in this category"`
	Template    string   `json:"template,omitempty" jsonschema:"the body a new note starts with, below its heading"`

	Mutability string `json:"mutability,omitempty" jsonschema:"append-only for a chronological record such as a log, replaceable for a note describing current state; defaults to append-only"`
}

// CategoryChangeOutput reports the resulting category.
type CategoryChangeOutput struct {
	Category CategoryView `json:"category"`
	Note     string       `json:"note,omitempty"`
}

// categoryCreate declares a new category.
func (s *Server) categoryCreate(ctx context.Context, _ *sdk.CallToolRequest, in CategoryCreateInput) (*sdk.CallToolResult, CategoryChangeOutput, error) {
	cat := category.Category{
		Name:        strings.TrimSpace(in.Name),
		Plural:      strings.TrimSpace(in.Plural),
		Rule:        category.Rule(strings.TrimSpace(in.Rule)),
		Folder:      strings.TrimSpace(in.Folder),
		Description: in.Description,
		Tags:        in.Tags,
		Template:    in.Template,
		Mutability:  category.Mutability(strings.TrimSpace(in.Mutability)),
	}

	for _, name := range in.Facets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cat.Facets = append(cat.Facets, category.Facet{
			Name: name,
			File: facetFile(name),
		})
	}

	set := s.vault.Categories()
	if err := set.Add(cat); err != nil {
		return nil, CategoryChangeOutput{}, err
	}
	if err := s.vault.SetCategories(set); err != nil {
		return nil, CategoryChangeOutput{}, err
	}

	s.record("gandalf: category create " + cat.Name)
	return nil, CategoryChangeOutput{Category: view(cat, 0)}, nil
}

// CategoryNameInput names an existing category.
type CategoryNameInput struct {
	Name string `json:"name"`
}

// categoryRetire hides a category from creation without orphaning its notes.
func (s *Server) categoryRetire(ctx context.Context, _ *sdk.CallToolRequest, in CategoryNameInput) (*sdk.CallToolResult, CategoryChangeOutput, error) {
	set := s.vault.Categories()

	cat, ok := set.Lookup(in.Name)
	if !ok {
		return nil, CategoryChangeOutput{}, fmt.Errorf("no category %q", in.Name)
	}
	if cat.Retired {
		return nil, CategoryChangeOutput{}, fmt.Errorf("category %q is already retired", cat.Name)
	}

	cat.Retired = true
	if err := set.Replace(cat.Name, cat); err != nil {
		return nil, CategoryChangeOutput{}, err
	}
	if err := s.vault.SetCategories(set); err != nil {
		return nil, CategoryChangeOutput{}, err
	}

	counts, err := s.categoryCounts()
	if err != nil {
		return nil, CategoryChangeOutput{}, err
	}

	s.record("gandalf: category retire " + cat.Name)
	return nil, CategoryChangeOutput{
		Category: view(cat, counts[cat.Name]),
		Note:     "no new notes can be filed here; the ones already filed stay addressable",
	}, nil
}

// categoryDelete removes a category outright.
//
// Only an empty category can go. Deleting one that still holds notes would
// leave them addressable only by path — readable, unwritable, and absent from
// every listing — which is a quiet way to lose a year of work.
func (s *Server) categoryDelete(ctx context.Context, _ *sdk.CallToolRequest, in CategoryNameInput) (*sdk.CallToolResult, CategoryChangeOutput, error) {
	set := s.vault.Categories()

	cat, ok := set.Lookup(in.Name)
	if !ok {
		return nil, CategoryChangeOutput{}, fmt.Errorf("no category %q", in.Name)
	}

	counts, err := s.categoryCounts()
	if err != nil {
		return nil, CategoryChangeOutput{}, err
	}
	if n := counts[cat.Name]; n > 0 {
		return nil, CategoryChangeOutput{}, fmt.Errorf(
			"category %q still holds %d note(s); delete or move them first, or retire the category instead to stop new ones being filed there",
			cat.Name, n)
	}

	if err := set.Remove(cat.Name); err != nil {
		return nil, CategoryChangeOutput{}, err
	}
	if err := s.vault.SetCategories(set); err != nil {
		return nil, CategoryChangeOutput{}, err
	}

	s.record("gandalf: category delete " + cat.Name)
	return nil, CategoryChangeOutput{
		Category: view(cat, 0),
		Note:     "deleted; the folder itself is left alone",
	}, nil
}

// categoryCounts returns how many notes each category holds.
func (s *Server) categoryCounts() (map[string]int, error) {
	paths, err := s.vault.List()
	if err != nil {
		return nil, err
	}

	counts := map[string]int{}
	for _, p := range paths {
		counts[s.vault.RefFor(p).Kind]++
	}

	return counts, nil
}

// view renders a category for a tool result.
func view(c category.Category, notes int) CategoryView {
	overall := c.MutabilityOf("")

	var exceptions map[string]string
	for _, facet := range c.FacetNames() {
		if m := c.MutabilityOf(facet); m != overall {
			if exceptions == nil {
				exceptions = map[string]string{}
			}
			exceptions[facet] = string(m)
		}
	}

	return CategoryView{
		Name:            c.Name,
		Plural:          c.Plural,
		Rule:            string(c.Rule),
		Folder:          c.Folder,
		Facets:          c.FacetNames(),
		Description:     c.Description,
		Tags:            c.Tags,
		Retired:         c.Retired,
		Mutability:      string(overall),
		FacetMutability: exceptions,
		RefForm:         refForm(c),
		Notes:           notes,
	}
}

// refForm shows how a category's notes are addressed.
func refForm(c category.Category) string {
	switch c.Rule {
	case category.RuleDated:
		return c.Name + ":YYYY-MM-DD-slug"
	case category.RuleScoped:
		return fmt.Sprintf("%s:<scope>:%s", c.Name, strings.Join(c.FacetNames(), "|"))
	case category.RuleNamed:
		return c.Name + ":<name>"
	case category.RuleSingleton:
		return c.Name
	default:
		return "addressed by path"
	}
}

// facetFile derives a scoped category's filename from its facet name, so a
// caller declaring a category does not also have to invent filenames.
func facetFile(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ") + ".md"
}
