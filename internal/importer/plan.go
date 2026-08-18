package importer

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/vault"
)

// Move is one note's journey from the source vault to the destination.
type Move struct {
	Source string
	Target string
	Ref    string

	// Category is the destination category, which the note's type is set to
	// on arrival.
	Category string

	// Note is the parsed source note, carrying the dates and metadata the
	// import preserves.
	Note *vault.Note
}

// Problem is a note the import will not move, and why.
type Problem struct {
	Source string
	Reason string
}

// Plan is everything the import intends to do, worked out before anything is
// written so it can be read, argued with, and rejected.
type Plan struct {
	Moves []Move

	// Unmapped notes matched no rule and have no usable frontmatter type.
	Unmapped []Problem

	// Conflicts would overwrite something already in the destination.
	Conflicts []Problem

	// Skipped matched a rule that said to leave them alone.
	Skipped []Problem

	// Links maps a source path without its extension to the destination path
	// without its extension, which is what link rewriting is done against.
	Links map[string]string
}

// Build works out where every note in the source vault would go.
//
// Nothing is written. The plan is the artifact: a migration that cannot be
// inspected before it runs is one nobody should run.
func Build(src, dst *vault.Vault, rules *Rules) (*Plan, error) {
	paths, err := src.List()
	if err != nil {
		return nil, err
	}

	plan := &Plan{Links: map[string]string{}}
	taken := map[string]string{}

	for _, sourcePath := range paths {
		note, err := src.Read(sourcePath)
		if err != nil {
			plan.Unmapped = append(plan.Unmapped, Problem{
				Source: sourcePath,
				Reason: fmt.Sprintf("cannot be read as a note: %v", err),
			})
			continue
		}

		rule, matched := rules.Match(sourcePath)
		if matched && rule.Skip {
			plan.Skipped = append(plan.Skipped, Problem{Source: sourcePath, Reason: "matched a skip rule"})
			continue
		}

		target, ref, categoryName, err := targetFor(dst, note, sourcePath, rule)
		if err != nil {
			plan.Unmapped = append(plan.Unmapped, Problem{Source: sourcePath, Reason: err.Error()})
			continue
		}

		switch {
		case dst.Exists(target):
			plan.Conflicts = append(plan.Conflicts, Problem{
				Source: sourcePath,
				Reason: fmt.Sprintf("%s already exists in the destination", target),
			})
			continue
		case taken[target] != "":
			plan.Conflicts = append(plan.Conflicts, Problem{
				Source: sourcePath,
				Reason: fmt.Sprintf("%s is already claimed by %s", target, taken[target]),
			})
			continue
		}

		taken[target] = sourcePath
		plan.Moves = append(plan.Moves, Move{
			Source: sourcePath, Target: target, Ref: ref, Category: categoryName, Note: note,
		})
		plan.Links[trimExt(sourcePath)] = trimExt(target)
	}

	sort.Slice(plan.Moves, func(i, j int) bool { return plan.Moves[i].Target < plan.Moves[j].Target })

	return plan, nil
}

// targetFor decides where one note belongs.
func targetFor(dst *vault.Vault, note *vault.Note, sourcePath string, rule Rule) (target, ref, categoryName string, err error) {
	name := strings.TrimSuffix(path.Base(sourcePath), path.Ext(sourcePath))

	categoryName = rule.Category
	if categoryName == "" {
		// Falling back to the note's own type means a vault already shaped
		// like this one needs no rules at all.
		categoryName = string(note.FM.Type)
	}
	if categoryName == "" {
		return "", "", "", fmt.Errorf("no rule matched and the note has no type")
	}

	cat, ok := dst.Categories().Lookup(categoryName)
	if !ok {
		return "", "", "", fmt.Errorf("category %q is not declared in the destination", categoryName)
	}

	on := note.FM.Created
	if on.IsZero() {
		on = note.FM.Updated
	}

	switch cat.Rule {
	case category.RuleDated:
		if on.IsZero() {
			return "", "", "", fmt.Errorf("a %s note needs a created date and has none", cat.Name)
		}

		slug := rule.Name
		if slug == "" {
			// A source name that already carries its date keeps it, so notes
			// filed the same way arrive unchanged.
			slug = strings.TrimPrefix(name, on.String()+"-")
		}
		name = category.DatedName(on.Time(), category.Slugify(slug))

	case category.RuleScoped:
		if rule.Scope == "" {
			return "", "", "", fmt.Errorf("a %s note needs a scope and the rule gives none", cat.Name)
		}
		if rule.Facet == "" {
			return "", "", "", fmt.Errorf("a %s note needs a facet (%s)", cat.Name, strings.Join(cat.FacetNames(), ", "))
		}
		name = rule.Facet

	case category.RuleNamed:
		if rule.Name != "" {
			name = rule.Name
		}
		name = category.Slugify(name)

	case category.RuleSingleton:

	default:
		return "", "", "", fmt.Errorf("%s notes are filed explicitly and cannot be imported", cat.Name)
	}

	target, err = cat.Path(rule.Scope, name, on.Time(), dst.Depth())
	if err != nil {
		return "", "", "", err
	}

	return target, dst.RefFor(target).String(), categoryName, nil
}

// Summary renders the plan for a human deciding whether to run it.
func (p *Plan) Summary() string {
	var b strings.Builder

	for _, m := range p.Moves {
		fmt.Fprintf(&b, "  %-44s -> %s\n", m.Source, m.Ref)
	}
	for _, group := range []struct {
		label    string
		problems []Problem
	}{
		{"skipped", p.Skipped},
		{"unmapped", p.Unmapped},
		{"conflicts", p.Conflicts},
	} {
		for _, problem := range group.problems {
			fmt.Fprintf(&b, "  %-44s %s: %s\n", problem.Source, group.label, problem.Reason)
		}
	}

	fmt.Fprintf(&b, "\n%d to import, %d skipped, %d unmapped, %d conflicting\n",
		len(p.Moves), len(p.Skipped), len(p.Unmapped), len(p.Conflicts))

	return b.String()
}

// trimExt removes a path's extension.
func trimExt(p string) string { return strings.TrimSuffix(p, path.Ext(p)) }
