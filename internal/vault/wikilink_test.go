package vault

import (
	"strings"
	"testing"
)

func TestParseWikilinks(t *testing.T) {
	const body = "# Note\n" +
		"See [[Agent/OS/Memory]] and [[Standards/privacy|the privacy standard]].\n" +
		"Also [[Design#Architecture]].\n" +
		"```go\n" +
		"// [[Not/A/Link]] in code\n" +
		"```\n" +
		"Trailing [[Glossary]].\n"

	links := ParseWikilinks(body)
	if len(links) != 4 {
		t.Fatalf("got %d links, want 4: %+v", len(links), links)
	}

	tests := []struct {
		target, heading, alias string
		line                   int
	}{
		{target: "Agent/OS/Memory", line: 2},
		{target: "Standards/privacy", alias: "the privacy standard", line: 2},
		{target: "Design", heading: "Architecture", line: 3},
		{target: "Glossary", line: 7},
	}
	for i, want := range tests {
		got := links[i]
		if got.Target != want.target || got.Heading != want.heading || got.Alias != want.alias {
			t.Errorf("link %d = %+v, want target=%q heading=%q alias=%q",
				i, got, want.target, want.heading, want.alias)
		}
		if got.Line != want.line {
			t.Errorf("link %d line = %d, want %d", i, got.Line, want.line)
		}
	}
}

func TestParseWikilinksSkipsCodeBlocks(t *testing.T) {
	const body = "~~~\n[[Hidden]]\n~~~\n[[Visible]]\n"

	links := ParseWikilinks(body)
	if len(links) != 1 || links[0].Target != "Visible" {
		t.Errorf("links = %+v, want only Visible", links)
	}
}

func TestRewriteWikilinks(t *testing.T) {
	// upper rewrites every target, so the test can see exactly which parts of
	// a link the rewriter touches.
	upper := func(target string) string { return strings.ToUpper(target) }

	tests := []struct {
		name    string
		in      string
		rewrite func(string) string
		want    string
	}{
		{
			name:    "plain target",
			in:      "See [[note]].",
			rewrite: upper,
			want:    "See [[NOTE]].",
		},
		{
			name:    "alias is preserved",
			in:      "See [[note|the note]].",
			rewrite: upper,
			want:    "See [[NOTE|the note]].",
		},
		{
			name:    "heading is preserved",
			in:      "See [[note#Section]].",
			rewrite: upper,
			want:    "See [[NOTE#Section]].",
		},
		{
			name:    "heading and alias together",
			in:      "See [[note#Section|there]].",
			rewrite: upper,
			want:    "See [[NOTE#Section|there]].",
		},
		{
			name:    "several links on one line",
			in:      "[[one]] and [[two]].",
			rewrite: upper,
			want:    "[[ONE]] and [[TWO]].",
		},
		{
			name:    "empty result leaves the link alone",
			in:      "See [[note]].",
			rewrite: func(string) string { return "" },
			want:    "See [[note]].",
		},
		{
			name:    "fenced code is untouched",
			in:      "[[one]]\n```\n[[two]]\n```\n[[three]]",
			rewrite: upper,
			want:    "[[ONE]]\n```\n[[two]]\n```\n[[THREE]]",
		},
		{
			name:    "prose without links is unchanged",
			in:      "No links here, just arr[0] and a stray bracket [.",
			rewrite: upper,
			want:    "No links here, just arr[0] and a stray bracket [.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RewriteWikilinks(tc.in, tc.rewrite); got != tc.want {
				t.Errorf("RewriteWikilinks() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestInlineCodeIsNotALink covers documents that explain link syntax. The
// instruction set does exactly this, and treating its examples as references
// had it linking to notes nobody meant to create.
func TestInlineCodeIsNotALink(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		links []string
	}{
		{
			name:  "a single example",
			in:    "Write links as `[[standard:language-go]]`.",
			links: nil,
		},
		{
			name:  "a real link beside an example",
			in:    "Use `[[standard:x]]` to reach [[Real Note]].",
			links: []string{"Real Note"},
		},
		{
			name:  "double backticks",
			in:    "Escaped: ``[[standard:x]]``",
			links: nil,
		},
		{
			name:  "an unclosed backtick does not swallow the line",
			in:    "A stray ` and then [[Real Note]].",
			links: []string{"Real Note"},
		},
		{
			name:  "backticks around other text",
			in:    "Call `gandalf_list` before [[Real Note]].",
			links: []string{"Real Note"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, l := range ParseWikilinks(tc.in) {
				got = append(got, l.Target)
			}
			if strings.Join(got, ",") != strings.Join(tc.links, ",") {
				t.Errorf("links = %v, want %v", got, tc.links)
			}
		})
	}
}

func TestRewriteSkipsInlineCode(t *testing.T) {
	const in = "Use `[[standard:x]]` to reach [[Real Note]]."

	got := RewriteWikilinks(in, func(target string) string { return strings.ToUpper(target) })
	if want := "Use `[[standard:x]]` to reach [[REAL NOTE]]."; got != want {
		t.Errorf("RewriteWikilinks() = %q, want %q", got, want)
	}
}

func TestLinkTarget(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "Agent/OS/Memory", want: "Agent/OS/Memory"},
		{in: "[[Agent/OS/Memory]]", want: "Agent/OS/Memory"},
		{in: "  [[Agent/OS/Memory]]  ", want: "Agent/OS/Memory"},
		{in: "[[Design#Architecture]]", want: "Design"},
		{in: "[[Standards/privacy|Privacy]]", want: "Standards/privacy"},
		{in: "[[]]", want: ""},
		{in: "", want: ""},
	}

	for _, tc := range tests {
		if got := LinkTarget(tc.in); got != tc.want {
			t.Errorf("LinkTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
