package vault

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/matjam/gandalf/internal/schema"
)

// managedKeys are the frontmatter keys Gandalf owns. Every other key is
// carried through untouched in Frontmatter.Extra.
var managedKeys = map[string]bool{
	"type": true, "created": true, "updated": true,
	"tags": true, "related": true, "author": true, "status": true,
	"index": true,
}

// parseFrontmatter decodes a YAML frontmatter block into typed frontmatter.
// Values of the wrong shape become issues rather than errors, so a caller can
// report everything wrong with a note in one pass. An error is returned only
// when the block is not valid YAML at all.
//
// With coerce set, a list value the schema wants as strings accepts bare
// numbers and booleans as their string form. YAML reads a tag written 7695 as
// a number, but the author unambiguously meant the string "7695"; the importer
// turns this on so such a note migrates rather than being refused, while
// ordinary reads stay strict so nothing is silently reshaped in place.
func parseFrontmatter(block string, coerce bool) (schema.Frontmatter, []schema.Issue, error) {
	raw := map[string]any{}
	if strings.TrimSpace(block) != "" {
		if err := yaml.Unmarshal([]byte(block), &raw); err != nil {
			return schema.Frontmatter{}, nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}

	var (
		fm     schema.Frontmatter
		issues []schema.Issue
	)
	bad := func(field, format string, args ...any) {
		issues = append(issues, schema.Issue{
			Field:    field,
			Severity: schema.SeverityError,
			Message:  fmt.Sprintf(format, args...),
		})
	}

	if v, ok := raw["type"]; ok {
		if s, err := scalarString(v); err == nil {
			fm.Type = schema.NoteType(s)
		} else {
			bad("type", "%s", err)
		}
	}
	if v, ok := raw["author"]; ok {
		if s, err := scalarString(v); err == nil {
			fm.Author = schema.Author(s)
		} else {
			bad("author", "%s", err)
		}
	}
	if v, ok := raw["status"]; ok {
		if s, err := scalarString(v); err == nil {
			if coerce {
				s = coerceStatus(s)
			}
			fm.Status = schema.Status(s)
		} else {
			bad("status", "%s", err)
		}
	}

	for _, f := range []struct {
		key  string
		dest *schema.Date
	}{{"created", &fm.Created}, {"updated", &fm.Updated}} {
		v, ok := raw[f.key]
		if !ok {
			continue
		}
		d, err := dateValue(v)
		if err != nil {
			bad(f.key, "%s", err)
			continue
		}
		*f.dest = d
	}

	if v, ok := raw["index"]; ok {
		switch b := v.(type) {
		case bool:
			fm.Index = &b
		default:
			bad("index", "expected true or false, got %s", yamlKind(v))
		}
	}

	if v, ok := raw["tags"]; ok {
		tags, err := stringList(v, coerce)
		if err != nil {
			bad("tags", "%s", err)
		} else {
			if coerce {
				for i := range tags {
					tags[i] = normalizeTag(tags[i])
				}
			}
			fm.Tags = tags
		}
	}

	if v, ok := raw["related"]; ok {
		related, err := stringList(v, coerce)
		if err != nil {
			bad("related", "%s", err)
		} else {
			for _, r := range related {
				fm.Related = append(fm.Related, LinkTarget(r))
			}
		}
	}

	for k, v := range raw {
		if managedKeys[k] {
			continue
		}
		if fm.Extra == nil {
			fm.Extra = map[string]any{}
		}
		fm.Extra[k] = v
	}

	return fm, issues, nil
}

// scalarString requires a plain string value.
func scalarString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected a string, got %s", yamlKind(v))
	}
	return s, nil
}

// dateValue accepts either a YYYY-MM-DD string or a value the YAML decoder
// already resolved to a timestamp.
func dateValue(v any) (schema.Date, error) {
	switch t := v.(type) {
	case string:
		return schema.ParseDate(t)
	case time.Time:
		return schema.NewDate(t), nil
	default:
		return schema.Date{}, fmt.Errorf("expected YYYY-MM-DD, got %s", yamlKind(v))
	}
}

// stringList accepts a YAML sequence of strings, and tolerates a bare string
// as a one-element list. With coerce set, bare numbers and booleans are read
// as their string form rather than rejected.
func stringList(v any, coerce bool) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				if coerce {
					if c, cok := coerceScalar(item); cok {
						out = append(out, c)
						continue
					}
				}
				return nil, fmt.Errorf("entry %d: expected a string, got %s", i+1, yamlKind(item))
			}
			out = append(out, s)
		}
		return out, nil
	default:
		if coerce {
			if c, ok := coerceScalar(v); ok {
				return []string{c}, nil
			}
		}
		return nil, fmt.Errorf("expected a list, got %s", yamlKind(v))
	}
}

// tagSeparators is any run of characters a tag is not allowed to contain:
// everything but a lowercase letter or digit.
var tagSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// normalizeTag reshapes a tag into the lowercase-hyphenated form the schema
// requires, so a vault that wrote "PP-7819" or "account_aggregation" keeps the
// meaning while gaining the mechanical uniformity that makes tags usable as
// filters. A tag with nothing usable in it is returned unchanged, to be
// reported rather than silently emptied.
func normalizeTag(tag string) string {
	shaped := strings.Trim(tagSeparators.ReplaceAllString(strings.ToLower(tag), "-"), "-")
	if shaped == "" {
		return tag
	}
	return shaped
}

// statusSynonyms maps the free-form status values a hand-kept vault
// accumulates onto the closed set the schema allows. Only unambiguous synonyms
// are mapped; an unrecognised status is left untouched, to be reported rather
// than guessed at.
var statusSynonyms = map[string]schema.Status{
	"completed":       schema.StatusComplete,
	"done":            schema.StatusComplete,
	"closed":          schema.StatusComplete,
	"verified":        schema.StatusComplete,
	"verified-fix":    schema.StatusComplete,
	"verified-closed": schema.StatusComplete,
	"fixed":           schema.StatusComplete,
	"resolved":        schema.StatusComplete,
	"wip":             schema.StatusInProgress,
	"in progress":     schema.StatusInProgress,
	"ongoing":         schema.StatusInProgress,
	"deprecated":      schema.StatusSuperseded,
	"obsolete":        schema.StatusSuperseded,
}

// coerceStatus maps a known status synonym onto the schema's value, leaving
// anything already valid or unrecognised as it is.
func coerceStatus(s string) string {
	key := strings.ToLower(strings.TrimSpace(s))
	if schema.Status(key).Valid() {
		return key
	}
	if mapped, ok := statusSynonyms[key]; ok {
		return string(mapped)
	}
	return s
}

// coerceScalar renders a YAML scalar the schema wanted as a string. It covers
// the numbers and booleans a bare, unquoted value decodes to; anything more
// structured has no unambiguous string form and is left for the caller to
// report.
func coerceScalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int, int64, uint64, float64, bool:
		return fmt.Sprintf("%v", t), true
	default:
		return "", false
	}
}

// yamlKind names a decoded value's YAML type for use in error messages.
func yamlKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, int64, uint64, float64:
		return "a number"
	case time.Time:
		return "a timestamp"
	case []any:
		return "a list"
	case map[string]any:
		return "a mapping"
	default:
		return fmt.Sprintf("%T", v)
	}
}
