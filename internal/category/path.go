package category

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// nonSlug matches every run of characters that cannot appear in a slug.
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// DateLayout is the date format used in filenames and dated refs.
const DateLayout = "2006-01-02"

// Depth is how far dated notes are nested before their filename.
type Depth string

const (
	// DepthMonth files a dated note as YYYY/MM/. The default: the filename
	// already carries the full date, so a month of notes in one directory
	// scans better than thirty directories holding one file each.
	DepthMonth Depth = "month"

	// DepthDay files a dated note as YYYY/MM/DD/, for vaults producing enough
	// notes per month that a month directory stops being browsable.
	DepthDay Depth = "day"
)

// Path returns where a note belongs.
//
// The name is the note's filename without its extension, which for a dated
// category already carries the date: DatedName composes one. Keeping a single
// meaning for "name" is what makes Path and Owns exact inverses, and getting
// that wrong files notes at paths their own refs cannot resolve.
//
// The arguments a category actually uses depend on its rule, and supplying the
// wrong ones is an error rather than something to guess around: filing a note
// in the wrong place is hard to notice and annoying to undo.
func (c Category) Path(scope, name string, on time.Time, depth Depth) (string, error) {
	switch c.Rule {
	case RuleDated:
		if name == "" {
			return "", fmt.Errorf("%s notes need a name", c.Name)
		}
		if on.IsZero() {
			return "", fmt.Errorf("%s notes need a date", c.Name)
		}
		return path.Join(c.datedDir(on, depth), name+".md"), nil

	case RuleScoped:
		if scope == "" {
			return "", fmt.Errorf("%s notes need a scope, such as the project name", c.Name)
		}
		if name == "" {
			return "", fmt.Errorf("%s notes need a facet (%s)", c.Name, strings.Join(c.FacetNames(), ", "))
		}
		file, ok := c.FacetFile(name)
		if !ok {
			return "", fmt.Errorf("%s has no facet %q (want %s)", c.Name, name, strings.Join(c.FacetNames(), ", "))
		}
		return path.Join(c.Folder, scope, file), nil

	case RuleNamed:
		if name == "" {
			return "", fmt.Errorf("%s notes need a title or slug", c.Name)
		}
		return path.Join(c.Folder, name+".md"), nil

	case RuleSingleton:
		return c.Folder, nil

	default:
		return "", fmt.Errorf("%s notes are filed explicitly and need a path", c.Name)
	}
}

// datedDir returns the directory a dated note is filed in.
func (c Category) datedDir(on time.Time, depth Depth) string {
	parts := []string{c.Folder, on.Format("2006"), on.Format("01")}
	if depth == DepthDay {
		parts = append(parts, on.Format("02"))
	}
	return path.Join(parts...)
}

// Owns reports whether a note path belongs to this category, and returns the
// scope and name identifying it within the category.
//
// It is the inverse of Path, and it is what lets a listing or a lint finding
// report something a caller can address.
func (c Category) Owns(notePath string) (scope, name string, ok bool) {
	clean := path.Clean(notePath)
	base := strings.TrimSuffix(path.Base(clean), path.Ext(clean))
	dir := path.Dir(clean)

	// A folder's README describes the folder; it is not a note of whatever
	// kind the folder holds. Without this, Standards/README.md answers to
	// standard:README — a folder description dressed as an engineering
	// standard.
	if strings.EqualFold(base, "README") {
		return "", "", false
	}

	switch c.Rule {
	case RuleSingleton:
		return "", "", clean == path.Clean(c.Folder)

	case RuleNamed:
		return "", base, dir == c.Folder

	case RuleDated:
		if !within(dir, c.Folder) {
			return "", "", false
		}
		if _, err := time.Parse(DateLayout, datePrefix(base)); err != nil {
			return "", "", false
		}
		return "", base, true

	case RuleScoped:
		if !within(dir, c.Folder) || dir == c.Folder {
			return "", "", false
		}
		for _, f := range c.Facets {
			if path.Base(clean) == f.File {
				return path.Base(dir), f.Name, true
			}
		}
		return "", "", false

	default:
		return "", "", false
	}
}

// Owner returns the category a note path belongs to.
func (s *Set) Owner(notePath string) (Category, string, string, bool) {
	for _, c := range s.Categories {
		if scope, name, ok := c.Owns(notePath); ok {
			return c, scope, name, true
		}
	}
	return Category{}, "", "", false
}

// datePrefix returns the leading YYYY-MM-DD of a name, or the empty string.
func datePrefix(name string) string {
	if len(name) < len(DateLayout) {
		return ""
	}
	return name[:len(DateLayout)]
}

// within reports whether dir is root or sits beneath it.
func within(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+"/")
}

// DatedName composes the filename stem for a dated note: the date it was
// created followed by a slug, which is what its ref then addresses.
func DatedName(on time.Time, slug string) string {
	return on.Format(DateLayout) + "-" + slug
}

// Slugify reduces a title to a filename-safe slug: lowercase words joined by
// single hyphens. It returns the empty string when nothing usable remains.
func Slugify(title string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
}
