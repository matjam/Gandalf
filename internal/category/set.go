package category

import (
	"fmt"
	"slices"
	"strings"
)

// Set is the categories a vault declares.
type Set struct {
	Categories []Category `json:"categories"`
}

// Defaults returns the categories Gandalf ships.
//
// They are a starting point. A vault that never touches them behaves as
// Gandalf always has; a vault that adds, renames, or retires them is doing
// what the design intends rather than working around it.
func Defaults() *Set {
	return &Set{Categories: []Category{
		{
			Name:        "session",
			Plural:      "sessions",
			Rule:        RuleDated,
			Folder:      "Sessions",
			Description: "One note per logical unit of work, written as the work happens.",
			Template: `
## Goal

What this work is trying to achieve, and why.

## Context

Prior work, constraints, and anything that would be hard to reconstruct later.

## Decisions

Decisions taken and the reasoning behind them, including alternatives rejected.

## Notes

Assumptions that may need revisiting; verified state versus intended behaviour.
`,
		},
		{
			Name:        "project",
			Plural:      "projects",
			Rule:        RuleScoped,
			Folder:      "Projects",
			Description: "A project's current design, its decision log, and its backlog.",
			Facets: []Facet{
				{Name: "design", File: "Design.md"},
				{Name: "decisions", File: "Decisions.md"},
				{Name: "todo", File: "Todo.md"},
			},
			Template: `
## Overview

Current state only. Remove history rather than letting it accumulate here.
`,
		},
		{
			Name:        "standard",
			Plural:      "standards",
			Rule:        RuleNamed,
			Folder:      "Standards",
			Description: "An engineering standard applied when writing code.",
			Tags:        []string{"standards"},
			Template: `
## Rules

## Rationale
`,
		},
		{
			Name:        "meeting",
			Plural:      "meetings",
			Rule:        RuleDated,
			Folder:      "Meetings",
			Description: "Notes from a conversation with other people, including interviews.",
			Template: `
## Attendees

## Notes

## Actions
`,
		},
		{
			Name:        "glossary",
			Plural:      "glossary",
			Rule:        RuleSingleton,
			Folder:      "Glossary.md",
			Description: "Terms and what they mean in this vault.",
		},
		{
			Name:        "readme",
			Plural:      "readmes",
			Rule:        RuleExplicit,
			Description: "A folder's own description of what belongs in it.",
		},
	}}
}

// Lookup returns the category with the given singular name.
func (s *Set) Lookup(name string) (Category, bool) {
	for _, c := range s.Categories {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Category{}, false
}

// ByPlural returns the category a listing name refers to.
func (s *Set) ByPlural(plural string) (Category, bool) {
	for _, c := range s.Categories {
		if strings.EqualFold(c.Plural, plural) {
			return c, true
		}
	}
	return Category{}, false
}

// Creatable returns the categories a new note can be filed under.
func (s *Set) Creatable() []Category {
	var out []Category
	for _, c := range s.Categories {
		if c.Creatable() {
			out = append(out, c)
		}
	}
	return out
}

// Names lists every declared category, retired ones included, since notes
// filed under a retired category are still addressable.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.Categories))
	for _, c := range s.Categories {
		out = append(out, c.Name)
	}
	return out
}

// CreatableNames lists the categories a model may create notes in.
func (s *Set) CreatableNames() []string {
	var out []string
	for _, c := range s.Creatable() {
		out = append(out, c.Name)
	}
	slices.Sort(out)
	return out
}

// Add appends a category, refusing anything that would collide with one
// already declared.
func (s *Set) Add(c Category) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := s.checkCollisions(c, ""); err != nil {
		return err
	}

	s.Categories = append(s.Categories, c)
	return nil
}

// Replace updates the category with the given name.
func (s *Set) Replace(name string, c Category) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := s.checkCollisions(c, name); err != nil {
		return err
	}

	for i, existing := range s.Categories {
		if strings.EqualFold(existing.Name, name) {
			s.Categories[i] = c
			return nil
		}
	}

	return fmt.Errorf("no category %q", name)
}

// Remove deletes a category outright. Callers are responsible for checking
// that no notes remain in it: this package cannot see the vault.
func (s *Set) Remove(name string) error {
	for i, c := range s.Categories {
		if strings.EqualFold(c.Name, name) {
			s.Categories = slices.Delete(s.Categories, i, i+1)
			return nil
		}
	}
	return fmt.Errorf("no category %q", name)
}

// Validate reports what is wrong with the set as a whole.
func (s *Set) Validate() error {
	if len(s.Categories) == 0 {
		return fmt.Errorf("a vault needs at least one category")
	}

	seen := map[string]bool{}
	for _, c := range s.Categories {
		if err := c.Validate(); err != nil {
			return err
		}
		// A category whose plural matches its name is fine — "glossary" has no
		// sensible plural — so only collisions between categories count.
		keys := []string{strings.ToLower(c.Name)}
		if plural := strings.ToLower(c.Plural); plural != keys[0] {
			keys = append(keys, plural)
		}

		for _, key := range keys {
			if seen[key] {
				return fmt.Errorf("category name or plural %q is declared twice", key)
			}
			seen[key] = true
		}
	}

	return nil
}

// checkCollisions rejects a category whose name, plural, or location is
// already taken. Ignoring is the name being replaced, if any.
func (s *Set) checkCollisions(c Category, ignoring string) error {
	for _, existing := range s.Categories {
		if strings.EqualFold(existing.Name, ignoring) {
			continue
		}
		switch {
		case strings.EqualFold(existing.Name, c.Name):
			return fmt.Errorf("category %q already exists", c.Name)
		case strings.EqualFold(existing.Plural, c.Plural),
			strings.EqualFold(existing.Name, c.Plural),
			strings.EqualFold(existing.Plural, c.Name):
			return fmt.Errorf("category %q collides with %q; names and plurals share one namespace", c.Name, existing.Name)
		case c.Folder != "" && strings.EqualFold(existing.Folder, c.Folder):
			return fmt.Errorf("category %q would file notes in %q, which %q already uses", c.Name, c.Folder, existing.Name)
		}
	}

	return nil
}
