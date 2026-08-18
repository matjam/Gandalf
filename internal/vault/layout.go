package vault

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// nonSlug matches every run of characters that cannot appear in a slug.
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Layout holds the vault's filing conventions: which directory each kind of
// note belongs in. It is a value rather than a set of constants so a vault can
// be seeded with different folder names without changing the routing logic.
type Layout struct {
	Sessions   string
	Projects   string
	Standards  string
	Meetings   string
	Interviews string
	Glossary   string // a file, not a directory

	// DesignFile and DecisionsFile are the note names used inside a project
	// directory.
	DesignFile    string
	DecisionsFile string
}

// DefaultLayout returns the folder conventions GandalfOS seeds.
func DefaultLayout() Layout {
	return Layout{
		Sessions:      "Sessions",
		Projects:      "Projects",
		Standards:     "Standards",
		Meetings:      "Meetings",
		Interviews:    "Interviews",
		Glossary:      "Glossary.md",
		DesignFile:    "Design.md",
		DecisionsFile: "Decisions.md",
	}
}

// Path derives where a note of the given type belongs.
//
// Dated types are filed as <dir>/YYYY/MM/YYYY-MM-DD-<slug>.md; project types
// as <projects>/<scope>/<file>; standards by slug; the glossary is a single
// well-known file. Callers that want to file a note somewhere else pass an
// explicit path instead of calling this.
func (l Layout) Path(t schema.NoteType, scope, slug string, on schema.Date) (string, error) {
	switch t {
	case schema.TypeSession, schema.TypeMeeting, schema.TypeInterview:
		if slug == "" {
			return "", fmt.Errorf("%s notes need a title or slug", t)
		}
		if on.IsZero() {
			return "", fmt.Errorf("%s notes need a date", t)
		}
		dir := map[schema.NoteType]string{
			schema.TypeSession:   l.Sessions,
			schema.TypeMeeting:   l.Meetings,
			schema.TypeInterview: l.Interviews,
		}[t]
		return path.Join(dir,
			fmt.Sprintf("%04d", on.Year()),
			fmt.Sprintf("%02d", int(on.Month())),
			fmt.Sprintf("%s-%s.md", on, slug),
		), nil

	case schema.TypeDesign, schema.TypeDecisions:
		if scope == "" {
			return "", fmt.Errorf("%s notes need a project scope", t)
		}
		file := l.DesignFile
		if t == schema.TypeDecisions {
			file = l.DecisionsFile
		}
		return path.Join(l.Projects, scope, file), nil

	case schema.TypeStandard:
		if slug == "" {
			return "", fmt.Errorf("standard notes need a title or slug")
		}
		return path.Join(l.Standards, slug+".md"), nil

	case schema.TypeGlossary:
		return l.Glossary, nil

	case schema.TypeReadme:
		// A README belongs to whichever folder it documents, which the layout
		// cannot infer.
		return "", fmt.Errorf("readme notes need an explicit path")

	default:
		return "", fmt.Errorf("unknown note type %q", t)
	}
}

// Slugify reduces a title to a filename-safe slug: lowercase words joined by
// single hyphens. It returns the empty string when nothing usable remains.
func Slugify(title string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
}
