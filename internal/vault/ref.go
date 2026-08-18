package vault

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// Kind is the first field of a ref: what sort of thing is being addressed.
type Kind string

// The addressable kinds.
const (
	KindSession   Kind = "session"
	KindMeeting   Kind = "meeting"
	KindInterview Kind = "interview"
	KindProject   Kind = "project"
	KindStandard  Kind = "standard"
	KindTopic     Kind = "topic"
	KindGlossary  Kind = "glossary"

	// KindPath addresses a note that follows none of the vault's conventions.
	// Refs of this kind are produced by search and lint so such notes can be
	// read; they are not accepted for writes, which confines the write surface
	// to the filing conventions by construction.
	KindPath Kind = "path"
)

// Latest addresses the most recent note of its kind, as in "session:latest".
// It has to be asked for; nothing defaults to it.
const Latest = "latest"

// Ref addresses a note by what it is rather than where it lives, so that
// nothing outside this package needs to know the filing conventions — and so a
// model cannot invent a path.
//
// The textual form is kind:scope:name, with the fields a kind actually needs:
//
//	session:2026-08-17-memory-toolset
//	project:gandalf:design
//	standard:language-go
//	topic:shipping
//	glossary
type Ref struct {
	Kind Kind

	// Scope is the project name, for project refs.
	Scope string

	// Name is the slug, dated slug, document ID, or facet, depending on kind.
	Name string
}

// projectFacets are the notes a project ref can address.
var projectFacets = map[string]schema.NoteType{
	"design":    schema.TypeDesign,
	"decisions": schema.TypeDecisions,
	"todo":      schema.TypeTodo,
}

// datedKinds maps the kinds filed by date to their note type.
var datedKinds = map[Kind]schema.NoteType{
	KindSession:   schema.TypeSession,
	KindMeeting:   schema.TypeMeeting,
	KindInterview: schema.TypeInterview,
}

// ParseRef reads a ref's textual form.
func ParseRef(s string) (Ref, error) {
	fields := strings.Split(strings.TrimSpace(s), ":")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}

	kind := Kind(fields[0])
	switch kind {
	case KindGlossary:
		if len(fields) != 1 {
			return Ref{}, fmt.Errorf("ref %q: glossary takes no further fields", s)
		}
		return Ref{Kind: kind}, nil

	case KindSession, KindMeeting, KindInterview, KindStandard, KindTopic:
		if len(fields) != 2 || fields[1] == "" {
			return Ref{}, fmt.Errorf("ref %q: want %s:<name>", s, kind)
		}
		return Ref{Kind: kind, Name: fields[1]}, nil

	case KindProject:
		if len(fields) != 3 || fields[1] == "" || fields[2] == "" {
			return Ref{}, fmt.Errorf("ref %q: want project:<name>:<%s>", s, facetList())
		}
		if _, ok := projectFacets[fields[2]]; !ok {
			return Ref{}, fmt.Errorf("ref %q: unknown project note %q (want %s)", s, fields[2], facetList())
		}
		return Ref{Kind: kind, Scope: fields[1], Name: fields[2]}, nil

	case KindPath:
		// The path may itself contain colons, so take everything after the
		// first field verbatim.
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), string(KindPath)+":"))
		if rest == "" {
			return Ref{}, fmt.Errorf("ref %q: want path:<vault path>", s)
		}
		return Ref{Kind: kind, Name: rest}, nil

	default:
		return Ref{}, fmt.Errorf("ref %q: unknown kind %q", s, fields[0])
	}
}

// String returns the ref's textual form.
func (r Ref) String() string {
	switch {
	case r.Kind == KindGlossary:
		return string(KindGlossary)
	case r.Scope != "":
		return fmt.Sprintf("%s:%s:%s", r.Kind, r.Scope, r.Name)
	default:
		return fmt.Sprintf("%s:%s", r.Kind, r.Name)
	}
}

// Writable reports whether notes addressed this way may be written. Path refs
// are read-only: they exist to reach notes that follow no convention, and
// writing to one would put the tool outside the filing rules it enforces.
func (r Ref) Writable() bool { return r.Kind != KindPath }

// Type returns the note type a ref addresses.
func (r Ref) Type() (schema.NoteType, error) {
	switch r.Kind {
	case KindProject:
		return projectFacets[r.Name], nil
	case KindStandard, KindTopic:
		return schema.TypeStandard, nil
	case KindGlossary:
		return schema.TypeGlossary, nil
	default:
		if t, ok := datedKinds[r.Kind]; ok {
			return t, nil
		}
		return "", fmt.Errorf("ref %q addresses no note type", r)
	}
}

// Resolve returns the vault path a ref points at.
//
// Topic refs are not resolved here: they address documents whose paths come
// from the shipped instruction manifest, which this package deliberately does
// not know about. Callers handling topics resolve them before calling.
func (l Layout) Resolve(r Ref) (string, error) {
	if r.Name == Latest {
		return "", fmt.Errorf("ref %q must be resolved against the vault's contents", r)
	}

	switch r.Kind {
	case KindPath:
		// RefFor drops the extension when it builds a path ref, so put it back.
		clean := path.Clean(r.Name)
		if path.Ext(clean) == "" {
			clean += ".md"
		}
		return clean, nil

	case KindTopic:
		return "", fmt.Errorf("ref %q: topics resolve through the instruction manifest", r)

	case KindGlossary:
		return l.Glossary, nil

	case KindStandard:
		return path.Join(l.Standards, r.Name+".md"), nil

	case KindProject:
		t, ok := projectFacets[r.Name]
		if !ok {
			return "", fmt.Errorf("ref %q: unknown project note %q", r, r.Name)
		}
		return path.Join(l.Projects, r.Scope, l.projectFile(t)), nil

	default:
		t, ok := datedKinds[r.Kind]
		if !ok {
			return "", fmt.Errorf("ref %q: unknown kind", r)
		}
		on, err := datePrefix(r.Name)
		if err != nil {
			return "", fmt.Errorf("ref %q: %w", r, err)
		}
		return path.Join(l.datedDir(t, on), r.Name+".md"), nil
	}
}

// RefFor returns the ref addressing a vault path, and whether the path follows
// the conventions well enough to have one. It is the inverse of Resolve, and
// it is what lets search and lint hand back something a caller can use.
func (l Layout) RefFor(notePath string) Ref {
	clean := path.Clean(notePath)
	name := strings.TrimSuffix(path.Base(clean), path.Ext(clean))
	dir := path.Dir(clean)

	if clean == path.Clean(l.Glossary) {
		return Ref{Kind: KindGlossary}
	}

	if dir == l.Standards {
		return Ref{Kind: KindStandard, Name: name}
	}

	for kind, root := range map[Kind]string{
		KindSession:   l.Sessions,
		KindMeeting:   l.Meetings,
		KindInterview: l.Interviews,
	} {
		if within(dir, root) {
			if _, err := datePrefix(name); err == nil {
				return Ref{Kind: kind, Name: name}
			}
		}
	}

	if within(dir, l.Projects) {
		if scope := path.Base(dir); scope != "" && scope != l.Projects {
			for facet, t := range projectFacets {
				if path.Base(clean) == l.projectFile(t) {
					return Ref{Kind: KindProject, Scope: scope, Name: facet}
				}
			}
		}
	}

	return Ref{Kind: KindPath, Name: strings.TrimSuffix(clean, path.Ext(clean))}
}

// datePrefix reads the YYYY-MM-DD a dated note's name begins with.
func datePrefix(name string) (schema.Date, error) {
	if len(name) < len(schema.DateLayout) {
		return schema.Date{}, fmt.Errorf("name %q does not begin with a date", name)
	}
	return schema.ParseDate(name[:len(schema.DateLayout)])
}

// within reports whether dir is root or sits beneath it.
func within(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+"/")
}

// facetList renders the project note names for an error message, in a fixed
// order so the message does not depend on map iteration.
func facetList() string {
	return strings.Join(slices.Sorted(maps.Keys(projectFacets)), "|")
}
