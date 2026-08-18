package vault

import (
	"fmt"
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
func parseFrontmatter(block string) (schema.Frontmatter, []schema.Issue, error) {
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
		tags, err := stringList(v)
		if err != nil {
			bad("tags", "%s", err)
		} else {
			fm.Tags = tags
		}
	}

	if v, ok := raw["related"]; ok {
		related, err := stringList(v)
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
// as a one-element list.
func stringList(v any) ([]string, error) {
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
				return nil, fmt.Errorf("entry %d: expected a string, got %s", i+1, yamlKind(item))
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected a list, got %s", yamlKind(v))
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
