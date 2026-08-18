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

// Depth is how far dated notes are nested before their filename.
type Depth string

const (
	// DepthMonth files a dated note as YYYY/MM/. The default: the filename
	// already carries the full date, and a month of notes is easier to scan
	// in one directory than spread across thirty holding one file each.
	DepthMonth Depth = "month"

	// DepthDay files a dated note as YYYY/MM/DD/, for vaults producing enough
	// notes per month that a month directory stops being browsable.
	DepthDay Depth = "day"
)

// Layout holds the vault's filing conventions: which directory each kind of
// note belongs in. It is a value rather than a set of constants so a vault can
// use different folder names without changing the routing logic.
type Layout struct {
	Instructions string
	Sessions     string
	Projects     string
	Standards    string
	Meetings     string
	Interviews   string
	Glossary     string // a file, not a directory

	// DesignFile, DecisionsFile, and TodoFile are the note names used inside
	// a project directory.
	DesignFile    string
	DecisionsFile string
	TodoFile      string

	// SessionDepth applies to every dated note type, not only sessions.
	SessionDepth Depth
}

// DefaultLayout returns the folder conventions GandalfOS seeds.
func DefaultLayout() Layout {
	return Layout{
		Instructions:  "Gandalf",
		Sessions:      "Sessions",
		Projects:      "Projects",
		Standards:     "Standards",
		Meetings:      "Meetings",
		Interviews:    "Interviews",
		Glossary:      "Glossary.md",
		DesignFile:    "Design.md",
		DecisionsFile: "Decisions.md",
		TodoFile:      "Todo.md",
		SessionDepth:  DepthMonth,
	}
}

// Path derives where a note of the given type belongs.
//
// Dated types are filed under a year and month directory and named for the day
// they were created; project types under the project's directory; standards by
// slug; the glossary is a single well-known file. Callers wanting a note
// somewhere else pass an explicit path instead of calling this.
func (l Layout) Path(t schema.NoteType, scope, slug string, on schema.Date) (string, error) {
	switch t {
	case schema.TypeSession, schema.TypeMeeting, schema.TypeInterview:
		if slug == "" {
			return "", fmt.Errorf("%s notes need a title or slug", t)
		}
		if on.IsZero() {
			return "", fmt.Errorf("%s notes need a date", t)
		}
		return path.Join(l.datedDir(t, on), fmt.Sprintf("%s-%s.md", on, slug)), nil

	case schema.TypeDesign, schema.TypeDecisions, schema.TypeTodo:
		if scope == "" {
			return "", fmt.Errorf("%s notes need a project scope", t)
		}
		return path.Join(l.Projects, scope, l.projectFile(t)), nil

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

// datedDir returns the directory a dated note of this type is filed in.
func (l Layout) datedDir(t schema.NoteType, on schema.Date) string {
	root := map[schema.NoteType]string{
		schema.TypeSession:   l.Sessions,
		schema.TypeMeeting:   l.Meetings,
		schema.TypeInterview: l.Interviews,
	}[t]

	parts := []string{root, fmt.Sprintf("%04d", on.Year()), fmt.Sprintf("%02d", int(on.Month()))}
	if l.SessionDepth == DepthDay {
		parts = append(parts, fmt.Sprintf("%02d", on.Day()))
	}
	return path.Join(parts...)
}

// projectFile returns the filename a project-scoped note type uses.
func (l Layout) projectFile(t schema.NoteType) string {
	switch t {
	case schema.TypeDecisions:
		return l.DecisionsFile
	case schema.TypeTodo:
		return l.TodoFile
	default:
		return l.DesignFile
	}
}

// Slugify reduces a title to a filename-safe slug: lowercase words joined by
// single hyphens. It returns the empty string when nothing usable remains.
func Slugify(title string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
}
