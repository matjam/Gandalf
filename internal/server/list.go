package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/vault"
)

// ListInput selects what to enumerate.
type ListInput struct {
	Kind string `json:"kind" jsonschema:"sessions, projects, standards, topics, meetings, interviews, or all"`

	Project string `json:"project,omitempty" jsonschema:"restrict to one project"`

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

// listKinds maps the plural names a caller asks for to the ref kind they
// enumerate. Plural reads as "the list of these", which is what it is.
var listKinds = map[string]vault.Kind{
	"sessions":   vault.KindSession,
	"standards":  vault.KindStandard,
	"meetings":   vault.KindMeeting,
	"interviews": vault.KindInterview,
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

	case "projects":
		projects, err := s.projects(in.Project)
		if err != nil {
			return nil, ListOutput{}, err
		}
		out.Projects = projects
		out.Total = len(projects)
		return nil, out, nil

	case "all":
		notes, err := s.summaries(nil, in.Project)
		if err != nil {
			return nil, ListOutput{}, err
		}
		out.Total = len(notes)
		out.Notes, out.Truncated = truncate(notes, limit)
		return nil, out, nil

	default:
		refKind, ok := listKinds[kind]
		if !ok {
			return nil, ListOutput{}, fmt.Errorf(
				"unknown kind %q (want %s, projects, topics, or all)",
				in.Kind, strings.Join(slices.Sorted(maps.Keys(listKinds)), ", "))
		}

		notes, err := s.summaries(&refKind, in.Project)
		if err != nil {
			return nil, ListOutput{}, err
		}
		out.Total = len(notes)
		out.Notes, out.Truncated = truncate(notes, limit)
		return nil, out, nil
	}
}

// summaries collects every note of a kind, or of any kind when kind is nil.
//
// Dated notes come back newest first, since recent work is what a caller is
// nearly always after; everything else is ordered by ref so the listing is
// stable between runs.
func (s *Server) summaries(kind *vault.Kind, project string) ([]Summary, error) {
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
		if dated(out[i].Ref) && dated(out[j].Ref) {
			return out[i].Ref > out[j].Ref
		}
		return out[i].Ref < out[j].Ref
	})

	return out, nil
}

// projects lists the projects in the vault and which notes each one has.
func (s *Server) projects(only string) ([]ProjectSummary, error) {
	paths, err := s.vault.List()
	if err != nil {
		return nil, err
	}

	byName := map[string][]string{}
	for _, path := range paths {
		ref := s.canonical(path)
		if ref.Kind != vault.KindProject {
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

// dated reports whether a ref's name begins with a date, and so sorts
// chronologically.
func dated(ref string) bool {
	for _, prefix := range []vault.Kind{vault.KindSession, vault.KindMeeting, vault.KindInterview} {
		if strings.HasPrefix(ref, string(prefix)+":") {
			return true
		}
	}
	return false
}

// truncate applies the limit, reporting whether anything was cut.
func truncate(notes []Summary, limit int) ([]Summary, bool) {
	if len(notes) <= limit {
		return notes, false
	}
	return notes[:limit], true
}
