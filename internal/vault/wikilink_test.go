package vault

import "testing"

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
