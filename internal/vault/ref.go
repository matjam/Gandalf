package vault

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/matjam/gandalf/internal/category"
)

// KindPath addresses a note that follows none of the vault's filing
// conventions. Refs of this kind are produced by listings and lint so such
// notes can be read; they are not accepted for writes, which confines the
// write surface to the conventions by construction.
const KindPath = "path"

// Latest addresses the most recent note of its kind, as in "session:latest".
// It has to be asked for; nothing defaults to it.
const Latest = "latest"

// Ref addresses a note by what it is rather than where it lives, so that
// nothing outside this package needs to know the filing conventions — and so a
// model cannot invent a path.
//
// The textual form is kind:scope:name, with the fields the kind's category
// actually needs:
//
//	session:2026-08-17-memory-toolset
//	project:gandalf:design
//	standard:language-go
//	glossary
type Ref struct {
	// Kind is a category name, or "path".
	Kind string

	// Scope identifies which group a scoped note belongs to, such as a project
	// name.
	Scope string

	// Name is the slug, dated slug, or facet, depending on the category's rule.
	Name string
}

// String returns the ref's textual form.
func (r Ref) String() string {
	switch {
	case r.Kind == "":
		return ""
	case r.Scope != "":
		return fmt.Sprintf("%s:%s:%s", r.Kind, r.Scope, r.Name)
	case r.Name == "":
		return r.Kind
	default:
		return fmt.Sprintf("%s:%s", r.Kind, r.Name)
	}
}

// Writable reports whether notes addressed this way may be written. Path refs
// are read-only: they exist to reach notes that follow no convention, and
// writing to one would put the tool outside the rules it enforces.
func (r Ref) Writable() bool { return r.Kind != KindPath }

// ParseRef reads a ref, checking its shape against the category it names.
func (v *Vault) ParseRef(s string) (Ref, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Ref{}, fmt.Errorf("a ref is required")
	}

	fields := strings.SplitN(trimmed, ":", 3)
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	kind := fields[0]

	if kind == KindPath {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, KindPath+":"))
		if rest == "" {
			return Ref{}, fmt.Errorf("ref %q: want path:<vault path>", s)
		}
		return Ref{Kind: KindPath, Name: rest}, nil
	}

	cat, ok := v.categories.Lookup(kind)
	if !ok {
		if looksLikePath(trimmed) {
			return Ref{}, fmt.Errorf(
				"%q is a file path; notes are addressed by ref, such as %s. "+
					"Refs come from boot, list, or the tool that created the note",
				s, v.refExamples())
		}
		return Ref{}, fmt.Errorf("ref %q: unknown category %q (want one of %s)",
			s, kind, strings.Join(v.categories.Names(), ", "))
	}

	switch cat.Rule {
	case category.RuleSingleton:
		if len(fields) != 1 {
			return Ref{}, fmt.Errorf("ref %q: %s takes no further fields", s, kind)
		}
		return Ref{Kind: kind}, nil

	case category.RuleScoped:
		if len(fields) != 3 || fields[1] == "" || fields[2] == "" {
			return Ref{}, fmt.Errorf("ref %q: want %s:<scope>:<%s>",
				s, kind, strings.Join(cat.FacetNames(), "|"))
		}
		if _, ok := cat.FacetFile(fields[2]); !ok {
			return Ref{}, fmt.Errorf("ref %q: %s has no %q (want %s)",
				s, kind, fields[2], strings.Join(cat.FacetNames(), ", "))
		}
		return Ref{Kind: kind, Scope: fields[1], Name: fields[2]}, nil

	case category.RuleExplicit:
		return Ref{}, fmt.Errorf("ref %q: %s notes are filed explicitly and are reached by path", s, kind)

	default:
		if len(fields) != 2 || fields[1] == "" {
			return Ref{}, fmt.Errorf("ref %q: want %s:<name>", s, kind)
		}
		return Ref{Kind: kind, Name: fields[1]}, nil
	}
}

// Resolve returns the vault path a ref addresses, including the "latest" form,
// which can only be answered by looking at what the vault contains.
func (v *Vault) Resolve(r Ref) (string, error) {
	if r.Kind == KindPath {
		clean := path.Clean(r.Name)
		if path.Ext(clean) == "" {
			// RefFor drops the extension when it builds a path ref.
			clean += ".md"
		}
		return clean, nil
	}

	if r.Name == Latest {
		found, err := v.Latest(r.Kind)
		if err != nil {
			return "", err
		}
		r = found
	}

	cat, ok := v.categories.Lookup(r.Kind)
	if !ok {
		return "", fmt.Errorf("ref %q: unknown category %q", r, r.Kind)
	}

	on, err := refDate(cat, r.Name)
	if err != nil {
		return "", fmt.Errorf("ref %q: %w", r, err)
	}

	return cat.Path(r.Scope, r.Name, on, v.depth)
}

// RefFor returns the ref addressing a vault path. A note the categories do not
// account for gets a path ref, which is readable but not writable.
func (v *Vault) RefFor(notePath string) Ref {
	clean := path.Clean(notePath)

	if cat, scope, name, ok := v.categories.Owner(clean); ok {
		return Ref{Kind: cat.Name, Scope: scope, Name: name}
	}

	return Ref{Kind: KindPath, Name: strings.TrimSuffix(clean, path.Ext(clean))}
}

// Latest returns the most recent note of a dated category. Dated names sort
// chronologically as text, so the last one is the newest.
func (v *Vault) Latest(kind string) (Ref, error) {
	paths, err := v.List()
	if err != nil {
		return Ref{}, err
	}

	var newest Ref
	for _, p := range paths {
		if ref := v.RefFor(p); ref.Kind == kind && ref.Name > newest.Name {
			newest = ref
		}
	}
	if newest.Name == "" {
		return Ref{}, fmt.Errorf("no %s notes in the vault", kind)
	}

	return newest, nil
}

// ReadRef parses and reads the note a ref addresses.
func (v *Vault) ReadRef(r Ref) (*Note, error) {
	rel, err := v.Resolve(r)
	if err != nil {
		return nil, err
	}
	return v.Read(rel)
}

// refDate extracts the date a dated ref's name begins with. Other rules do not
// use it.
func refDate(cat category.Category, name string) (time.Time, error) {
	if cat.Rule != category.RuleDated {
		return time.Time{}, nil
	}
	if len(name) < len(category.DateLayout) {
		return time.Time{}, fmt.Errorf("name %q does not begin with a date", name)
	}
	return time.Parse(category.DateLayout, name[:len(category.DateLayout)])
}

// refExamples renders example refs drawn from this vault's own categories,
// for an error message that has to teach the scheme rather than merely reject
// input. Examples from the shipped defaults would be wrong in a vault that had
// changed them.
func (v *Vault) refExamples() string {
	var out []string
	for _, c := range v.categories.Creatable() {
		switch c.Rule {
		case category.RuleDated:
			out = append(out, c.Name+":YYYY-MM-DD-slug")
		case category.RuleScoped:
			out = append(out, fmt.Sprintf("%s:<scope>:%s", c.Name, strings.Join(c.FacetNames(), "|")))
		case category.RuleNamed:
			out = append(out, c.Name+":<name>")
		case category.RuleSingleton:
			out = append(out, c.Name)
		}
		if len(out) == 3 {
			break
		}
	}
	return strings.Join(out, ", ")
}

// looksLikePath reports whether the input is a file path rather than a ref.
// Telling a model it passed a path is worth the special case: it is the one
// mistake this addressing scheme exists to prevent, and "unknown category"
// would send it looking for a different category rather than a different idea.
func looksLikePath(s string) bool {
	return strings.Contains(s, "/") ||
		strings.EqualFold(path.Ext(s), ".md") ||
		strings.HasPrefix(s, ".")
}
