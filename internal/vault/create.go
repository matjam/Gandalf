package vault

import (
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/schema"
)

// NewNoteRequest describes a note to create. Everything the schema requires is
// either supplied here or filled in by Gandalf; callers never write a
// frontmatter block themselves.
type NewNoteRequest struct {
	// Type is the category the note belongs to.
	Type schema.NoteType

	Title string

	// Scope names the group a scoped note belongs to, such as a project.
	Scope string

	// Name overrides the name derived from the title. For a scoped category it
	// is the facet.
	Name string

	// Path files the note explicitly, bypassing the category's filing rule.
	Path string

	Tags    []string
	Related []string
	Author  schema.Author
	Status  schema.Status

	// Body replaces the category's template. The title heading is added when
	// the body does not already open with one.
	Body string

	// On is the note's creation date. It defaults to today.
	On schema.Date
}

// NewNote builds a note from a request: filing it by its category, generating
// valid frontmatter, and filling in that category's template. The note is
// returned rather than written, so a caller can inspect it or refuse to
// overwrite.
func (v *Vault) NewNote(req NewNoteRequest) (*Note, error) {
	cat, ok := v.categories.Lookup(string(req.Type))
	if !ok {
		return nil, fmt.Errorf("unknown category %q (want one of %s)",
			req.Type, strings.Join(v.categories.CreatableNames(), ", "))
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, fmt.Errorf("a note needs a title")
	}

	on := req.On
	if on.IsZero() {
		on = schema.Today()
	}

	author := req.Author
	if author == "" {
		author = schema.AuthorAgent
	}
	if !author.Valid() {
		return nil, fmt.Errorf("unknown author %q", req.Author)
	}
	if req.Status != "" && !req.Status.Valid() {
		return nil, fmt.Errorf("unknown status %q", req.Status)
	}

	notePath := req.Path
	if notePath == "" {
		name := req.Name
		if name == "" && cat.Rule != category.RuleScoped {
			slug := category.Slugify(req.Title)
			if slug == "" {
				return nil, fmt.Errorf("title %q produces an empty name; pass one explicitly", req.Title)
			}

			name = slug
			if cat.Rule == category.RuleDated {
				name = category.DatedName(on.Time(), slug)
			}
		}

		var err error
		if notePath, err = cat.Path(req.Scope, name, on.Time(), v.depth); err != nil {
			return nil, err
		}
	}

	related := make([]string, 0, len(req.Related))
	for _, r := range req.Related {
		if target := LinkTarget(r); target != "" {
			related = append(related, target)
		}
	}

	return &Note{
		Path: notePath,
		FM: schema.Frontmatter{
			Type:    req.Type,
			Created: on,
			Updated: on,
			Tags:    mergeTags(cat.Tags, req.Tags),
			Related: related,
			Author:  author,
			Status:  req.Status,
		},
		Body: body(req, cat),
	}, nil
}

// body returns the note's starting content: the caller's own text when given,
// otherwise the category's template. Either way it opens with an H1.
func body(req NewNoteRequest, cat category.Category) string {
	heading := "# " + strings.TrimSpace(req.Title)

	if custom := strings.TrimSpace(req.Body); custom != "" {
		if strings.HasPrefix(custom, "# ") {
			return custom + "\n"
		}
		return heading + "\n\n" + custom + "\n"
	}

	if template := strings.TrimRight(cat.Template, "\n"); template != "" {
		return heading + "\n" + template + "\n"
	}

	return heading + "\n"
}

// mergeTags combines a category's standing tags with the ones a caller asked
// for, keeping the caller's order and dropping duplicates.
func mergeTags(standing, requested []string) []string {
	out := make([]string, 0, len(standing)+len(requested))
	seen := map[string]bool{}

	for _, tag := range append(append([]string{}, standing...), requested...) {
		if tag = strings.TrimSpace(tag); tag != "" && !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}

	return out
}
