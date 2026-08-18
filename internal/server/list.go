package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/vault"
)

// ListInput selects what to enumerate.
type ListInput struct {
	Kind string `json:"kind" jsonschema:"what to list; the categories this vault declares, plus topics and all"`

	Scope string `json:"scope,omitempty" jsonschema:"restrict to one scope of a scoped category, such as a project name"`

	Limit int `json:"limit,omitempty" jsonschema:"maximum entries to return; defaults to 50"`
}

// Summary is one note, without its content.
type Summary struct {
	Ref     string   `json:"ref"`
	Title   string   `json:"title"`
	Updated string   `json:"updated"`
	Tags    []string `json:"tags,omitempty"`
	Status  string   `json:"status,omitempty"`
}

// ProjectSummary is one project and the notes it has.
type ProjectSummary struct {
	Name string `json:"name"`

	// Notes are the refs that exist for this project, so a caller can see at a
	// glance whether a design, decisions log, or backlog is there to read.
	Notes []string `json:"notes"`
}

// ListOutput is what the vault holds, in summary.
type ListOutput struct {
	Kind      string           `json:"kind"`
	Notes     []Summary        `json:"notes,omitempty"`
	Projects  []ProjectSummary `json:"projects,omitempty"`
	Topics    []TopicSummary   `json:"topics,omitempty"`
	Total     int              `json:"total"`
	Truncated bool             `json:"truncated,omitempty"`
}

// defaultLimit caps a listing unless the caller asks for more. Summaries are
// cheap individually and expensive in aggregate: an unbounded listing of a
// long-lived vault would spend a session's context on filenames. The total and
// truncated fields tell a caller what it did not see, so the cap costs
// information rather than hiding it.
const defaultLimit = 20

// listNames returns everything list accepts for this vault: each
// category's plural, plus the two listings that are not categories.
func (s *Server) listNames() []string {
	out := []string{"all", "topics"}
	for _, c := range s.vault.Categories().Categories {
		if !c.Retired {
			out = append(out, c.Plural)
		}
	}
	slices.Sort(out)
	return out
}

// list enumerates the vault without returning any note's content.
func (s *Server) list(ctx context.Context, _ *sdk.CallToolRequest, in ListInput) (*sdk.CallToolResult, ListOutput, error) {
	kind := strings.TrimSpace(strings.ToLower(in.Kind))
	if kind == "" {
		kind = "all"
	}

	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}

	out := ListOutput{Kind: kind}

	switch kind {
	case "topics":
		for _, doc := range instructions.Topics() {
			out.Topics = append(out.Topics, TopicSummary{
				Ref:   s.canonical(doc.Path).String(),
				Title: doc.Title,
				When:  doc.When,
			})
		}
		out.Total = len(out.Topics)
		return nil, out, nil

	case "all":
		notes, err := s.summaries(nil, in.Scope)
		if err != nil {
			return nil, ListOutput{}, err
		}
		out.Total = len(notes)
		out.Notes, out.Truncated = truncate(notes, limit)
		return nil, out, nil
	}

	cat, ok := s.vault.Categories().ByPlural(kind)
	if !ok {
		return nil, ListOutput{}, fmt.Errorf("unknown kind %q (want one of %s)",
			in.Kind, strings.Join(s.listNames(), ", "))
	}

	// A scoped category is summarised by scope rather than by note: what a
	// caller wants from "projects" is which projects exist, not three rows per
	// project.
	if cat.Rule == category.RuleScoped {
		groups, err := s.scopes(cat, in.Scope)
		if err != nil {
			return nil, ListOutput{}, err
		}
		out.Projects = groups
		out.Total = len(groups)
		return nil, out, nil
	}

	notes, err := s.summaries(&cat.Name, in.Scope)
	if err != nil {
		return nil, ListOutput{}, err
	}
	out.Total = len(notes)
	out.Notes, out.Truncated = truncate(notes, limit)
	return nil, out, nil
}

// summaries collects every note of a kind, or of any kind when kind is nil.
//
// Dated notes come back newest first, since recent work is what a caller is
// nearly always after; everything else is ordered by ref so the listing is
// stable between runs.
func (s *Server) summaries(kind *string, project string) ([]Summary, error) {
	paths, err := s.vault.List()
	if err != nil {
		return nil, err
	}

	var out []Summary
	for _, path := range paths {
		ref := s.canonical(path)
		if kind != nil && ref.Kind != *kind {
			continue
		}
		if kind == nil && ref.Kind == vault.KindPath {
			// Notes following no convention are reachable but not worth
			// listing: they are nobody's idea of vault structure.
			continue
		}
		if project != "" && !strings.EqualFold(ref.Scope, project) {
			continue
		}

		note, err := s.vault.Read(path)
		if err != nil {
			// A note too broken to parse is lint's business, not this tool's.
			continue
		}

		out = append(out, Summary{
			Ref:     ref.String(),
			Title:   note.Title(),
			Updated: note.FM.Updated.String(),
			Tags:    note.FM.Tags,
			Status:  string(note.FM.Status),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if s.dated(out[i].Ref) && s.dated(out[j].Ref) {
			return out[i].Ref > out[j].Ref
		}
		return out[i].Ref < out[j].Ref
	})

	return out, nil
}

// scopes lists the groups a scoped category holds, and the notes in each.
func (s *Server) scopes(cat category.Category, only string) ([]ProjectSummary, error) {
	paths, err := s.vault.List()
	if err != nil {
		return nil, err
	}

	byName := map[string][]string{}
	for _, path := range paths {
		ref := s.canonical(path)
		if ref.Kind != cat.Name {
			continue
		}
		if only != "" && !strings.EqualFold(ref.Scope, only) {
			continue
		}
		byName[ref.Scope] = append(byName[ref.Scope], ref.String())
	}

	out := make([]ProjectSummary, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		notes := byName[name]
		sort.Strings(notes)
		out = append(out, ProjectSummary{Name: name, Notes: notes})
	}

	return out, nil
}

// dated reports whether a ref addresses a category filed by date, and so sorts
// chronologically rather than alphabetically.
func (s *Server) dated(ref string) bool {
	kind, _, ok := strings.Cut(ref, ":")
	if !ok {
		return false
	}
	cat, ok := s.vault.Categories().Lookup(kind)
	return ok && cat.Rule == category.RuleDated
}

// truncate applies the limit, reporting whether anything was cut.
func truncate(notes []Summary, limit int) ([]Summary, bool) {
	if len(notes) <= limit {
		return notes, false
	}
	return notes[:limit], true
}
