package instructions

import (
	"path"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

// TestManifestIntegrity checks the shipped set is internally consistent. These
// are the mistakes a hand-maintained manifest actually makes.
func TestManifestIntegrity(t *testing.T) {
	ids := map[string]bool{}
	paths := map[string]bool{}
	sources := map[string]bool{}

	for _, doc := range Docs() {
		t.Run(doc.ID, func(t *testing.T) {
			switch {
			case doc.ID == "":
				t.Error("empty ID")
			case ids[doc.ID]:
				t.Errorf("duplicate ID %q", doc.ID)
			}
			ids[doc.ID] = true

			if paths[doc.Path] {
				t.Errorf("duplicate vault path %q", doc.Path)
			}
			paths[doc.Path] = true

			if sources[doc.Source] {
				t.Errorf("duplicate source %q", doc.Source)
			}
			sources[doc.Source] = true

			if !doc.Type.Valid() {
				t.Errorf("invalid note type %q", doc.Type)
			}
			if doc.Title == "" {
				t.Error("empty title")
			}
			if doc.When == "" {
				t.Error("empty When; the topic table needs it to be useful")
			}
			if !strings.HasSuffix(doc.Path, ".md") {
				t.Errorf("vault path %q is not a markdown file", doc.Path)
			}
			if len(doc.Tags) == 0 {
				t.Error("no tags; the linter warns about untagged notes")
			}

			switch doc.Role {
			case RoleCore, RoleTopic, RoleReference:
			default:
				t.Errorf("unknown role %q", doc.Role)
			}
		})
	}
}

// TestEveryDocumentHasContent catches a manifest entry pointing at a file that
// was renamed or never written — a failure that would otherwise surface as a
// seeding error on a user's first run.
func TestEveryDocumentHasContent(t *testing.T) {
	for _, doc := range Docs() {
		t.Run(doc.ID, func(t *testing.T) {
			body, err := doc.Body()
			if err != nil {
				t.Fatalf("Body: %v", err)
			}
			if strings.TrimSpace(body) == "" {
				t.Fatal("empty body")
			}
			if !strings.HasPrefix(strings.TrimSpace(body), "# ") {
				t.Error("body does not open with a level-one heading")
			}

			hash, err := doc.Hash()
			if err != nil {
				t.Fatalf("Hash: %v", err)
			}
			if len(hash) != 16 {
				t.Errorf("hash %q is not 16 characters", hash)
			}
		})
	}
}

// TestRelatedLinksResolve checks every cross-reference points at another
// shipped document. Lint catches this too, but only after seeding; this fails
// at build time instead.
func TestRelatedLinksResolve(t *testing.T) {
	targets := map[string]bool{}
	for _, doc := range Docs() {
		targets[strings.TrimSuffix(doc.Path, path.Ext(doc.Path))] = true
	}

	for _, doc := range Docs() {
		for _, target := range doc.Related {
			if !targets[target] {
				t.Errorf("%s links to %q, which is not a shipped document", doc.ID, target)
			}
		}
	}
}

func TestRolePartition(t *testing.T) {
	core, topics := Core(), Topics()
	if len(core) == 0 {
		t.Error("no core documents; boot would return nothing")
	}
	if len(topics) == 0 {
		t.Error("no topics; the dispatch table would be empty")
	}

	for _, d := range core {
		if d.Role != RoleCore {
			t.Errorf("Core() returned %q with role %q", d.ID, d.Role)
		}
	}
	for _, d := range topics {
		if d.Role != RoleTopic {
			t.Errorf("Topics() returned %q with role %q", d.ID, d.Role)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("operating"); !ok {
		t.Error("operating not found")
	}
	if _, ok := Lookup("nonexistent"); ok {
		t.Error("Lookup returned a document that does not exist")
	}
}

// TestCoreDocumentsAreReachable guards the property that makes lazy topics
// safe: everything not returned at boot must be reachable from something that
// is, or the model will never know it exists.
func TestCoreDocumentsAreReachable(t *testing.T) {
	var core []Doc
	for _, d := range Docs() {
		if d.Role == RoleCore {
			core = append(core, d)
		}
	}

	for _, d := range core {
		if d.Type != schema.TypeStandard {
			t.Errorf("core document %q has type %q", d.ID, d.Type)
		}
	}

	// The operating contract carries the topic table, so it must be core.
	operating, ok := Lookup("operating")
	if !ok || operating.Role != RoleCore {
		t.Error("the operating contract must be a core document")
	}
}

func TestHashBodyIgnoresSurroundingWhitespace(t *testing.T) {
	const body = "# Title\n\nSome content.\n"

	base := HashBody(body)
	for _, variant := range []string{body + "\n\n", "\n" + body, "  " + body + "  \n"} {
		if got := HashBody(variant); got != base {
			t.Errorf("HashBody(%q) = %q, want %q", variant, got, base)
		}
	}
	if HashBody(body+"real change\n") == base {
		t.Error("HashBody ignored a real change")
	}
}
