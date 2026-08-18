package importer

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// Result reports what an import did.
type Result struct {
	Imported int

	// Rewritten is the number of links repointed at their new homes.
	Rewritten int

	// Dangling are links whose targets were not part of the import, left as
	// they were written.
	Dangling []string
}

// Apply writes a plan into the destination vault.
//
// Links are rewritten against the whole plan rather than note by note, because
// a migrated vault is full of references in every direction and no order of
// writing makes them resolvable one at a time. The note tools refuse a link to
// a note that does not exist yet, which is right for a session and wrong here,
// so the importer writes through the vault directly and rebuilds backlinks at
// the end.
func Apply(dst *vault.Vault, plan *Plan) (Result, error) {
	var result Result
	dangling := map[string]bool{}

	// Nothing is written until every note is known to be writable. Build
	// already routes notes with unresolved issues away from Moves, so this is
	// a guard against a hand-built plan rather than the expected path — but it
	// is what makes apply all-or-nothing: a note the vault would refuse can no
	// longer abort the run partway and leave the destination half-migrated.
	var refused []string
	for _, move := range plan.Moves {
		if len(move.Note.Issues) > 0 {
			refused = append(refused, fmt.Sprintf("%s (%s)", move.Source, issuesReason(move.Note.Issues)))
		}
	}
	if len(refused) > 0 {
		sort.Strings(refused)
		return result, fmt.Errorf("refusing to import: %d note(s) carry unresolved frontmatter issues and would fail to write:\n  %s",
			len(refused), strings.Join(refused, "\n  "))
	}

	for _, move := range plan.Moves {
		note := move.Note
		note.Path = move.Target

		// The note's type has to name the category it now lives in. A source
		// vault calling it "design" while this one files it under "project"
		// would arrive filed correctly and describing itself wrongly, which
		// lint reports and nothing fixes.
		note.FM.Type = schema.NoteType(move.Category)

		// Frontmatter links and prose links both point at old locations.
		note.FM.Related = rewriteTargets(note.FM.Related, plan.Links, dangling, &result.Rewritten)
		note.Body = vault.RewriteWikilinks(note.Body, func(target string) string {
			return rewriteTarget(target, plan.Links, dangling, &result.Rewritten)
		})

		// The source's backlinks describe the vault it came from, and are
		// rebuilt from scratch once everything has landed.
		note.SetBacklinks(nil)

		// A note carrying unresolved parse issues would be refused on write;
		// the pre-flight check above has already excluded those, so anything
		// reaching here is sound enough to store.
		if err := dst.Write(note); err != nil {
			return result, fmt.Errorf("import %q: %w", move.Source, err)
		}
		result.Imported++
	}

	if _, err := dst.RebuildBacklinks(); err != nil {
		return result, err
	}

	for link := range dangling {
		result.Dangling = append(result.Dangling, link)
	}
	sort.Strings(result.Dangling)

	return result, nil
}

// rewriteTargets repoints a list of related link targets, dropping any whose
// target was not part of the import.
//
// A related entry names a note this one is about; once its target is not in
// the vault, it is a broken metadata reference rather than prose a reader can
// judge for themselves, so it is removed. Body wikilinks are treated the other
// way — left as written — because deleting words from a note is a heavier act
// than pruning a link list.
func rewriteTargets(targets []string, links map[string]string, dangling map[string]bool, count *int) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		if moved := rewriteTarget(target, links, dangling, count); moved != "" {
			out = append(out, moved)
		}
	}
	return out
}

// rewriteTarget returns a link's new target, or the empty string when it does
// not move.
func rewriteTarget(target string, links map[string]string, dangling map[string]bool, count *int) string {
	clean := strings.TrimSuffix(path.Clean(vault.LinkTarget(target)), ".md")

	if moved, ok := links[clean]; ok {
		*count++
		return moved
	}

	// A bare note name rather than a path: match on the filename, which is how
	// most hand-written links are spelled.
	if !strings.Contains(clean, "/") {
		for source, moved := range links {
			if strings.EqualFold(path.Base(source), clean) {
				*count++
				return moved
			}
		}
	}

	dangling[clean] = true
	return ""
}
