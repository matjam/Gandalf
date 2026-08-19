package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/instructions"
)

// The seeded instructions cannot be corrected by a release: once a document is
// in a vault, it wins over the binary, which is what makes a user's edits
// durable and what makes a stale description of the tools permanent. The
// defence is to keep the tool surface out of those documents, and this is the
// test that says so — a shipped document may name a tool, but only one that
// exists.

// toolName matches a name of the shape Gandalf's multi-word tools use. Single
// words are left alone: `list` and `force` read as prose or as parameters as
// readily as they do as tools, and guessing at them would fail on the wrong
// things.
var toolName = regexp.MustCompile("`([a-z]+(?:_[a-z]+)+)`")

func TestShippedInstructionsNameNoToolThatDoesNotExist(t *testing.T) {
	h := newHarness(t)

	tools, err := h.client.ListTools(h.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	registered := map[string]bool{}
	for _, tool := range tools.Tools {
		registered[tool.Name] = true
	}

	for _, doc := range instructions.Docs() {
		body, err := doc.Body()
		if err != nil {
			t.Fatalf("%s: %v", doc.ID, err)
		}

		for _, match := range toolName.FindAllStringSubmatch(body, -1) {
			name := match[1]

			// Only names that look like this tool surface's are checked, so
			// prose about a `snake_case` field somewhere else does not fail.
			if !strings.HasPrefix(name, "note_") && !strings.HasPrefix(name, "category_") &&
				!strings.HasPrefix(name, "session_") && !strings.HasPrefix(name, "git_") {
				continue
			}

			if !registered[name] {
				t.Errorf("%s names %q, which is not a registered tool", doc.Path, name)
			}
		}
	}
}

// TestTheMemoryProtocolDoesNotEnumerateTheTools is the narrower rule the
// protocol itself follows: describing the surface there put a copy of it in
// every vault, where it went stale twice and could not be fixed by shipping a
// new one. What a tool does belongs on the tool.
func TestTheMemoryProtocolDoesNotEnumerateTheTools(t *testing.T) {
	doc, ok := instructions.Lookup("memory")
	if !ok {
		t.Fatal("no memory protocol is shipped")
	}

	body, err := doc.Body()
	if err != nil {
		t.Fatalf("Body: %v", err)
	}

	var named []string
	for _, match := range toolName.FindAllStringSubmatch(body, -1) {
		named = append(named, match[1])
	}

	// One or two are unavoidable — the startup procedure has to say what to
	// call first. A list of them is the thing being prevented.
	if len(named) > 2 {
		t.Errorf("the memory protocol names %d tools (%s); boot and the tool descriptions carry that now",
			len(named), strings.Join(named, ", "))
	}
}
