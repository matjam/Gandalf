package vault

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// Finding is a single lint result, located in the vault.
type Finding struct {
	Path     string
	Line     int // 1-based within the body; 0 when the finding is not line-specific
	Field    string
	Severity schema.Severity
	Message  string
}

// String renders a finding in the compiler-style form agents and humans both
// already know how to read.
func (f Finding) String() string {
	loc := f.Path
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	if f.Field != "" {
		return fmt.Sprintf("%s: %s: %s: %s", loc, f.Severity, f.Field, f.Message)
	}
	return fmt.Sprintf("%s: %s: %s", loc, f.Severity, f.Message)
}

// Lint validates the given notes, or every note in the vault when no paths are
// given. Findings are returned sorted by path so output is stable between
// runs; an unreadable note yields a finding rather than aborting the run.
func (v *Vault) Lint(paths ...string) ([]Finding, error) {
	all, err := v.List()
	if err != nil {
		return nil, err
	}

	targets := paths
	if len(targets) == 0 {
		targets = all
	}

	index := newIndex(all)

	// Backlinks are maintained incrementally on write, so nothing repairs them
	// if a note is changed outside these tools. Checking them here is what
	// makes that trade safe.
	inbound, err := v.inboundLinks(all)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	for _, rel := range targets {
		rel = path.Clean(rel)
		note, err := v.Read(rel)
		if err != nil {
			findings = append(findings, Finding{
				Path:     rel,
				Severity: schema.SeverityError,
				Message:  err.Error(),
			})
			continue
		}
		findings = append(findings, v.lintNote(note, index, inbound[rel])...)
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})

	return findings, nil
}

// HasErrors reports whether any finding is an error rather than a warning.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == schema.SeverityError {
			return true
		}
	}
	return false
}

// lintNote runs every check that applies to a single note.
func (v *Vault) lintNote(n *Note, index *noteIndex, inbound []string) []Finding {
	var findings []Finding

	at := func(issue schema.Issue, line int) {
		findings = append(findings, Finding{
			Path:     n.Path,
			Line:     line,
			Field:    issue.Field,
			Severity: issue.Severity,
			Message:  issue.Message,
		})
	}

	for _, issue := range n.Issues {
		at(issue, 0)
	}
	for _, issue := range n.FM.Validate() {
		at(issue, 0)
	}

	// Whether a type names a declared category is a vault-level fact, so the
	// schema leaves it alone and it is checked here.
	if t := string(n.FM.Type); t != "" {
		if _, ok := v.categories.Lookup(t); !ok {
			at(schema.Issue{
				Field:    "type",
				Severity: schema.SeverityError,
				Message: fmt.Sprintf("unknown category %q (this vault declares %s)",
					t, strings.Join(v.categories.Names(), ", ")),
			}, 0)
		}
	}

	if n.Title() == "" {
		findings = append(findings, Finding{
			Path:     n.Path,
			Severity: schema.SeverityWarning,
			Message:  "no level-one heading",
		})
	}

	findings = append(findings, lintLinks(n, index)...)

	if !equal(n.Backlinks(), inbound) {
		findings = append(findings, Finding{
			Path:     n.Path,
			Severity: schema.SeverityWarning,
			Message: fmt.Sprintf("backlinks are out of date: %d recorded, %d actual. Run `gandalf reindex` to rebuild them",
				len(n.Backlinks()), len(inbound)),
		})
	}

	return findings
}

// inboundLinks maps each note to the notes linking at it, as the link targets
// a backlinks block records.
func (v *Vault) inboundLinks(paths []string) (map[string][]string, error) {
	resolver := newIndex(paths)
	out := map[string][]string{}

	for _, source := range paths {
		note, err := v.Read(source)
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(source, path.Ext(source))
		for target := range resolve(resolver, source, note.OutgoingLinks()) {
			out[target] = append(out[target], name)
		}
	}

	return out, nil
}

// lintLinks reports related entries and body wikilinks whose targets are not
// in the vault. A link to a note that does not exist is either a typo or a
// note someone meant to write, and both are worth surfacing.
func lintLinks(n *Note, index *noteIndex) []Finding {
	var findings []Finding

	for _, target := range n.FM.Related {
		if index.resolve(target) == "" {
			findings = append(findings, Finding{
				Path:     n.Path,
				Field:    "related",
				Severity: schema.SeverityError,
				Message:  fmt.Sprintf("link target %q does not exist in the vault", target),
			})
		}
	}

	for _, link := range ParseWikilinks(n.Body) {
		if index.resolve(link.Target) == "" {
			findings = append(findings, Finding{
				Path:     n.Path,
				Line:     link.Line,
				Severity: schema.SeverityWarning,
				Message:  fmt.Sprintf("link target %q does not exist in the vault", link.Target),
			})
		}
	}

	return findings
}

// noteIndex resolves wikilink targets to note paths. Obsidian accepts both a
// vault-relative path and a bare note name, so both are indexed; lookups are
// case-insensitive because the vaults these notes live in usually are.
type noteIndex struct {
	byPath map[string]string
	byName map[string]string
}

// newIndex builds a lookup over the vault's note paths.
func newIndex(paths []string) *noteIndex {
	idx := &noteIndex{
		byPath: make(map[string]string, len(paths)),
		byName: make(map[string]string, len(paths)),
	}
	for _, p := range paths {
		trimmed := strings.TrimSuffix(p, path.Ext(p))
		idx.byPath[strings.ToLower(trimmed)] = p

		// An ambiguous bare name resolves to whichever note sorts first; the
		// path form disambiguates, and List returns sorted paths.
		name := strings.ToLower(path.Base(trimmed))
		if _, taken := idx.byName[name]; !taken {
			idx.byName[name] = p
		}
	}
	return idx
}

// resolve returns the note path a link target refers to, or the empty string
// when nothing in the vault matches.
func (idx *noteIndex) resolve(target string) string {
	key := strings.ToLower(strings.TrimSuffix(strings.Trim(target, "/"), ".md"))
	if key == "" {
		return ""
	}
	if p, ok := idx.byPath[key]; ok {
		return p
	}
	return idx.byName[key]
}
