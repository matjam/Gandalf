// Package importer moves an existing markdown vault into a Gandalf vault.
//
// Migration is the one job the note tools cannot do. They create notes dated
// today, refuse links to notes that do not exist yet, and deliberately keep a
// model away from the filesystem — all correct for a session, all fatal for a
// bulk move where dates must be preserved, links point in every direction, and
// nothing exists until it all does.
//
// So the importer works on whole vaults at once: it plans every note first,
// rewrites links against that plan, and only then writes. A model decides the
// mapping; this decides nothing and performs all of it.
package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

// Rule maps source notes onto a category in the destination vault.
type Rule struct {
	// Match is a path pattern against the source vault. A single * matches one
	// path segment, ** matches any number. Both capture, and captures are
	// referenced as $1, $2 in the fields below.
	Match string `json:"match"`

	// Category is the destination category name. Empty means the note's own
	// frontmatter type is used.
	Category string `json:"category,omitempty"`

	// Scope and Facet fill in a scoped category's fields.
	Scope string `json:"scope,omitempty"`
	Facet string `json:"facet,omitempty"`

	// Name overrides the note's derived name. Dated categories keep the date
	// from the note's own frontmatter, so a name given here is the slug alone.
	Name string `json:"name,omitempty"`

	// Skip leaves matching notes where they are, for source files that belong
	// to the old system rather than to the user.
	Skip bool `json:"skip,omitempty"`
}

// Rules are applied in order; the first match wins.
type Rules struct {
	Rules []Rule `json:"rules"`
}

// LoadRules reads a rules file.
func LoadRules(path string) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read import rules: %w", err)
	}

	rules := &Rules{}
	if err := json.Unmarshal(data, rules); err != nil {
		return nil, fmt.Errorf("parse import rules at %s: %w", path, err)
	}
	return rules, nil
}

// Match returns the first rule matching a source path, with its captures
// substituted.
func (r *Rules) Match(sourcePath string) (Rule, bool) {
	for _, rule := range r.Rules {
		captures, ok := match(rule.Match, sourcePath)
		if !ok {
			continue
		}

		resolved := rule
		resolved.Category = substitute(rule.Category, captures)
		resolved.Scope = substitute(rule.Scope, captures)
		resolved.Facet = substitute(rule.Facet, captures)
		resolved.Name = substitute(rule.Name, captures)
		return resolved, true
	}

	return Rule{}, false
}

// match reports whether a path matches a pattern, returning what the wildcards
// captured.
func match(pattern, target string) ([]string, bool) {
	patternParts := strings.Split(strings.TrimSuffix(pattern, ".md"), "/")
	targetParts := strings.Split(strings.TrimSuffix(target, path.Ext(target)), "/")

	var captures []string
	return captures, walk(patternParts, targetParts, &captures)
}

// walk matches pattern segments against path segments, handling ** by trying
// each possible span.
func walk(pattern, target []string, captures *[]string) bool {
	switch {
	case len(pattern) == 0:
		return len(target) == 0
	case pattern[0] == "**":
		// Try the shortest span first so ** stays lazy, which keeps a trailing
		// literal segment matching the last segment rather than the first.
		for span := 0; span <= len(target); span++ {
			saved := *captures
			*captures = append(*captures, strings.Join(target[:span], "/"))
			if walk(pattern[1:], target[span:], captures) {
				return true
			}
			*captures = saved
		}
		return false
	case len(target) == 0:
		return false
	case pattern[0] == "*":
		*captures = append(*captures, target[0])
		return walk(pattern[1:], target[1:], captures)
	case pattern[0] == target[0]:
		return walk(pattern[1:], target[1:], captures)
	default:
		return false
	}
}

// substitute replaces $1, $2 and so on with the captured segments.
func substitute(template string, captures []string) string {
	if template == "" {
		return ""
	}

	out := template
	for i, capture := range captures {
		out = strings.ReplaceAll(out, fmt.Sprintf("$%d", i+1), capture)
	}
	return out
}
