package vault

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// plainScalar matches strings safe to write unquoted: they start with a letter
// and contain no YAML punctuation. Leading digits are excluded so a value like
// a bare year is quoted rather than read back as a number.
var plainScalar = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 _./-]*$`)

// reservedWords are plain-looking scalars that YAML resolves to something
// other than a string, so they must be quoted.
var reservedWords = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true,
	"on": true, "off": true, "null": true, "y": true, "n": true,
}

// writeScalar emits a single key/value line, quoting the value when needed.
// Unset values are skipped so a malformed note does not gain empty keys when
// it is written back.
func writeScalar(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key + ": " + yamlScalar(value) + "\n")
}

// writeDate emits a date unquoted, which is what Obsidian and its Dataview
// queries expect of a YYYY-MM-DD value.
func writeDate(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key + ": " + value + "\n")
}

// writeRelated emits related links as quoted wikilinks, one per line, in the
// block style the vault convention uses.
func writeRelated(b *strings.Builder, related []string) {
	if len(related) == 0 {
		b.WriteString("related: []\n")
		return
	}
	b.WriteString("related:\n")
	for _, target := range related {
		b.WriteString("  - \"[[" + target + "]]\"\n")
	}
}

// writeExtra emits unmanaged keys in sorted order so output stays stable.
func writeExtra(b *strings.Builder, extra map[string]any) {
	for _, key := range slices.Sorted(maps.Keys(extra)) {
		out, err := yaml.Marshal(map[string]any{key: extra[key]})
		if err != nil {
			// A value decoded from YAML should always marshal back. If one
			// somehow does not, dropping it would lose metadata Gandalf did
			// not create, so fall back to its text form.
			writeScalar(b, key, fmt.Sprint(extra[key]))
			continue
		}
		b.Write(out)
	}
}

// flowList renders tags in YAML flow style: [one, two].
func flowList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = yamlScalar(v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// yamlScalar quotes a value unless it is safe to write plain.
func yamlScalar(s string) string {
	if plainScalar.MatchString(s) && !reservedWords[strings.ToLower(s)] {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
