package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/vault"
)

// source builds a vault of raw notes to import from.
func source(t *testing.T, notes map[string]string) *vault.Vault {
	t.Helper()

	root := t.TempDir()
	for rel, body := range notes {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}

	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	return v
}

// destination returns an empty vault to import into.
func destination(t *testing.T) *vault.Vault {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open destination: %v", err)
	}
	return v
}

// note renders a source note with the given frontmatter type and body.
func note(noteType, created, body string) string {
	return "---\ntype: " + noteType + "\ncreated: " + created + "\nupdated: " + created +
		"\ntags: [imported]\nauthor: user\n---\n\n" + body
}

func TestRuleMatching(t *testing.T) {
	rules := &Rules{Rules: []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1", Facet: "design"},
		{Match: "Sessions/**", Category: "session"},
		{Match: "**/README.md", Skip: true},
		{Match: "docs/**", Skip: true},
	}}

	tests := []struct {
		path      string
		wantMatch bool
		wantScope string
		wantSkip  bool
	}{
		{path: "Apps/BlitterAmp/Design.md", wantMatch: true, wantScope: "BlitterAmp"},
		{path: "Sessions/2026/08/2026-08-14-work.md", wantMatch: true},
		{path: "Apps/BlitterAmp/README.md", wantMatch: true, wantSkip: true},
		{path: "docs/plans/a-plan.md", wantMatch: true, wantSkip: true},

		{path: "Apps/BlitterAmp/Decisions.md", wantMatch: false},
		{path: "Glossary.md", wantMatch: false},
		{path: "Apps/Design.md", wantMatch: false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rule, ok := rules.Match(tc.path)
			if ok != tc.wantMatch {
				t.Fatalf("matched = %v, want %v", ok, tc.wantMatch)
			}
			if !ok {
				return
			}
			if rule.Skip != tc.wantSkip {
				t.Errorf("skip = %v, want %v", rule.Skip, tc.wantSkip)
			}
			if rule.Scope != tc.wantScope {
				t.Errorf("scope = %q, want %q", rule.Scope, tc.wantScope)
			}
		})
	}
}

func TestFirstRuleWins(t *testing.T) {
	rules := &Rules{Rules: []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1", Facet: "design"},
		{Match: "Apps/**", Skip: true},
	}}

	rule, ok := rules.Match("Apps/Egg/Design.md")
	if !ok || rule.Skip {
		t.Errorf("the specific rule lost to the catch-all: %+v", rule)
	}
	if rule, ok = rules.Match("Apps/Egg/Notes.md"); !ok || !rule.Skip {
		t.Errorf("the catch-all did not apply: %+v", rule)
	}
}

// TestDatesArePreserved is the reason the importer exists rather than a script
// driving the note tools: those create notes dated today.
func TestDatesArePreserved(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/03/2026-03-09-early-work.md": note("session", "2026-03-09", "# Early Work\n\nProse.\n"),
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("moves = %+v, want one", plan.Moves)
	}
	if got := plan.Moves[0].Ref; got != "session:2026-03-09-early-work" {
		t.Errorf("ref = %q, want the original date kept", got)
	}

	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	imported, err := dst.Read("Sessions/2026/03/2026-03-09-early-work.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if imported.FM.Created.String() != "2026-03-09" {
		t.Errorf("created = %q", imported.FM.Created)
	}
}

// TestLinksAreRewritten is the other reason: a migrated vault refers to itself
// in every direction, and the note tools refuse links to notes that do not
// exist yet.
func TestLinksAreRewritten(t *testing.T) {
	src := source(t, map[string]string{
		"Apps/Egg/Design.md": "---\ntype: design\ncreated: 2026-05-01\nupdated: 2026-05-01\ntags: [egg]\n" +
			"related:\n  - \"[[Apps/Egg/Decisions]]\"\nauthor: user\n---\n\n" +
			"# Egg\n\nSee [[Apps/Egg/Decisions]] and [[Sessions/2026/05/2026-05-01-egg-work]].\n",
		"Apps/Egg/Decisions.md":                   note("decisions", "2026-05-01", "# Egg\n\nDecisions.\n"),
		"Sessions/2026/05/2026-05-01-egg-work.md": note("session", "2026-05-01", "# Egg Work\n\nProse.\n"),
	})
	dst := destination(t)

	rules := &Rules{Rules: []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1", Facet: "design"},
		{Match: "Apps/*/Decisions.md", Category: "project", Scope: "$1", Facet: "decisions"},
		{Match: "Sessions/**", Category: "session"},
	}}

	plan, err := Build(src, dst, rules)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result, err := Apply(dst, plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Imported != 3 {
		t.Errorf("imported = %d, want 3", result.Imported)
	}
	if result.Rewritten < 3 {
		t.Errorf("rewrote %d links, want every one", result.Rewritten)
	}

	design, err := dst.Read("Projects/Egg/Design.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"[[Projects/Egg/Decisions]]", "[[Sessions/2026/05/2026-05-01-egg-work]]"} {
		if !strings.Contains(design.Body, want) {
			t.Errorf("body does not contain %q:\n%s", want, design.Body)
		}
	}
	if strings.Contains(design.Body, "Apps/Egg") {
		t.Errorf("an old path survived:\n%s", design.Body)
	}
	if got := strings.Join(design.FM.Related, ","); got != "Projects/Egg/Decisions" {
		t.Errorf("related = %q, want it repointed", got)
	}

	// The whole vault must satisfy its own linter afterwards.
	findings, err := dst.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if vault.HasErrors(findings) {
		t.Errorf("the imported vault has errors: %v", findings)
	}
}

// TestTypeMatchesDestinationCategory covers a note that arrives filed correctly
// and describing itself wrongly.
func TestTypeMatchesDestinationCategory(t *testing.T) {
	src := source(t, map[string]string{
		"Apps/Egg/Design.md": note("design", "2026-05-01", "# Egg\n\nProse.\n"),
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{Rules: []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1", Facet: "design"},
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	imported, err := dst.Read("Projects/Egg/Design.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(imported.FM.Type) != "project" {
		t.Errorf("type = %q, want the destination's category", imported.FM.Type)
	}
}

func TestPlanReportsProblems(t *testing.T) {
	src := source(t, map[string]string{
		"Loose.md":          "# No Frontmatter\n",
		"Unknown.md":        note("diary", "2026-05-01", "# Diary\n"),
		"Standards/keep.md": note("standard", "2026-05-01", "# Keep\n"),
	})
	dst := destination(t)

	// Something already at the destination path.
	existing, err := dst.NewNote(vault.NewNoteRequest{
		Type: "standard", Title: "Keep", Tags: []string{"standards"},
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if err := dst.Write(existing); err != nil {
		t.Fatalf("Write: %v", err)
	}

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(plan.Moves) != 0 {
		t.Errorf("moves = %+v, want none", plan.Moves)
	}
	if len(plan.Unmapped) != 2 {
		t.Errorf("unmapped = %+v, want the unreadable and the unknown category", plan.Unmapped)
	}
	if len(plan.Conflicts) != 1 {
		t.Errorf("conflicts = %+v, want the existing note", plan.Conflicts)
	}

	// Nothing is written by planning.
	if dst.Exists("Diary.md") {
		t.Error("planning wrote a note")
	}
}

func TestApplyNeverOverwrites(t *testing.T) {
	src := source(t, map[string]string{
		"Standards/keep.md": note("standard", "2026-05-01", "# Keep\n\nFrom the source.\n"),
	})
	dst := destination(t)

	existing, err := dst.NewNote(vault.NewNoteRequest{
		Type: "standard", Title: "Keep", Tags: []string{"standards"},
		Body: "# Keep\n\nAlready here.\n",
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if err := dst.Write(existing); err != nil {
		t.Fatalf("Write: %v", err)
	}

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	kept, err := dst.Read("Standards/keep.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(kept.Body, "Already here") {
		t.Errorf("the destination's note was replaced:\n%s", kept.Body)
	}
}

func TestLoadRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	const body = `{"rules":[{"match":"Apps/*/Design.md","category":"project","scope":"$1","facet":"design"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rules, err := LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	rule, ok := rules.Match("Apps/Egg/Design.md")
	if !ok || rule.Scope != "Egg" || rule.Facet != "design" {
		t.Errorf("rule = %+v", rule)
	}

	if _, err := LoadRules(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("LoadRules accepted a missing file")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadRules(bad); err == nil {
		t.Error("LoadRules accepted malformed JSON")
	}
}

// TestBareNameLinksAreMatched covers hand-written links that name a note
// rather than spelling out its path, which is how most people write them.
func TestBareNameLinksAreMatched(t *testing.T) {
	src := source(t, map[string]string{
		"Apps/Egg/Design.md":    note("design", "2026-05-01", "# Egg\n\nSee [[Decisions]].\n"),
		"Apps/Egg/Decisions.md": note("decisions", "2026-05-01", "# Egg\n\nProse.\n"),
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{Rules: []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1", Facet: "design"},
		{Match: "Apps/*/Decisions.md", Category: "project", Scope: "$1", Facet: "decisions"},
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	design, err := dst.Read("Projects/Egg/Design.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(design.Body, "[[Projects/Egg/Decisions]]") {
		t.Errorf("a bare-name link was not repointed:\n%s", design.Body)
	}
}

// TestNumericTagsAreCoercedOnImport covers a note whose tag YAML decoded as a
// number: strict parsing would refuse it, but the importer reads it as the
// string form so the note migrates intact.
func TestNumericTagsAreCoercedOnImport(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/04/2026-04-02-work.md": "---\ntype: session\ncreated: 2026-04-02\n" +
			"updated: 2026-04-02\ntags:\n  - pp\n  - 7695\nauthor: user\n---\n\n# Work\n\nProse.\n",
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("moves = %+v, want one; invalid = %+v", plan.Moves, plan.Invalid)
	}
	if len(plan.Invalid) != 0 {
		t.Errorf("invalid = %+v, want none", plan.Invalid)
	}

	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	imported, err := dst.Read("Sessions/2026/04/2026-04-02-work.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := strings.Join(imported.FM.Tags, ","); got != "pp,7695" {
		t.Errorf("tags = %q, want the numeric tag kept as a string", got)
	}
}

// TestNotesWithUnresolvableIssuesAreReported covers a note that parses but
// carries frontmatter the destination would refuse: it must appear in the plan
// as invalid rather than being counted as importable.
func TestNotesWithUnresolvableIssuesAreReported(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/04/2026-04-02-work.md": "---\ntype: session\ncreated: 2026-04-02\n" +
			"updated: 2026-04-02\ntags:\n  key: value\nauthor: user\n---\n\n# Work\n",
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Moves) != 0 {
		t.Errorf("moves = %+v, want none", plan.Moves)
	}
	if len(plan.Invalid) != 1 {
		t.Fatalf("invalid = %+v, want the one bad note", plan.Invalid)
	}
	if !strings.Contains(plan.Summary(), "invalid") {
		t.Errorf("summary omits the invalid group:\n%s", plan.Summary())
	}
}

// TestApplyRefusesIssueNotes checks apply is all-or-nothing: a plan carrying a
// note the vault would refuse writes nothing at all rather than aborting
// partway through.
func TestApplyRefusesIssueNotes(t *testing.T) {
	dst := destination(t)

	good, err := vault.ParseNote("Sessions/2026/05/2026-05-01-good.md",
		[]byte("---\ntype: session\ncreated: 2026-05-01\nupdated: 2026-05-01\ntags: [ok]\nauthor: user\n---\n\n# Good\n"))
	if err != nil {
		t.Fatalf("ParseNote(good): %v", err)
	}
	bad, err := vault.ParseNote("Sessions/2026/05/2026-05-02-bad.md",
		[]byte("---\ntype: session\ncreated: 2026-05-02\nupdated: 2026-05-02\ntags: [7]\nauthor: user\n---\n\n# Bad\n"))
	if err != nil {
		t.Fatalf("ParseNote(bad): %v", err)
	}
	if len(bad.Issues) == 0 {
		t.Fatal("expected the numeric tag to leave an issue under strict parsing")
	}

	plan := &Plan{
		Links: map[string]string{},
		Moves: []Move{
			{Source: "good.md", Target: good.Path, Category: "session", Note: good},
			{Source: "bad.md", Target: bad.Path, Category: "session", Note: bad},
		},
	}

	if _, err := Apply(dst, plan); err == nil {
		t.Fatal("Apply accepted a plan with an unwritable note")
	}
	if dst.Exists(good.Path) {
		t.Error("apply wrote the good note despite refusing the plan; it is not all-or-nothing")
	}
}

func TestPlanSummary(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/05/2026-05-01-work.md": note("session", "2026-05-01", "# Work\n"),
		"Loose.md":                            "# No Frontmatter\n",
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	summary := plan.Summary()
	for _, want := range []string{"session:2026-05-01-work", "Loose.md", "unmapped", "1 to import"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary omits %q:\n%s", want, summary)
		}
	}
}

// TestScopedNotesNeedTheirFields checks a rule that forgets what a scoped
// category requires fails in the plan rather than halfway through writing.
func TestScopedNotesNeedTheirFields(t *testing.T) {
	src := source(t, map[string]string{
		"Apps/Egg/Design.md": note("design", "2026-05-01", "# Egg\n"),
	})
	dst := destination(t)

	for _, rule := range []Rule{
		{Match: "Apps/*/Design.md", Category: "project", Facet: "design"},
		{Match: "Apps/*/Design.md", Category: "project", Scope: "$1"},
		{Match: "Apps/*/Design.md", Category: "readme"},
	} {
		plan, err := Build(src, dst, &Rules{Rules: []Rule{rule}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if len(plan.Moves) != 0 {
			t.Errorf("rule %+v produced a move it should not have", rule)
		}
		if len(plan.Unmapped) != 1 {
			t.Errorf("rule %+v did not report a problem", rule)
		}
	}
}

// TestDanglingRelatedLinksAreStripped covers the split policy: a related entry
// whose target is not in the import is removed as a broken metadata reference,
// while the same target in prose is left as written.
func TestDanglingRelatedLinksAreStripped(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/05/2026-05-01-work.md": "---\ntype: session\ncreated: 2026-05-01\n" +
			"updated: 2026-05-01\ntags: [work]\nrelated:\n  - \"[[Apps/Skipped/Design]]\"\n" +
			"author: user\n---\n\n# Work\n\nSee [[Apps/Skipped/Design]].\n",
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Apply(dst, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	imported, err := dst.Read("Sessions/2026/05/2026-05-01-work.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(imported.FM.Related) != 0 {
		t.Errorf("related = %v, want the out-of-import entry dropped", imported.FM.Related)
	}
	if !strings.Contains(imported.Body, "[[Apps/Skipped/Design]]") {
		t.Errorf("the prose link was removed:\n%s", imported.Body)
	}
}

func TestDanglingLinksAreReportedNotDeleted(t *testing.T) {
	src := source(t, map[string]string{
		"Sessions/2026/05/2026-05-01-work.md": note("session", "2026-05-01",
			"# Work\n\nSee [[Apps/Skipped/Design]].\n"),
	})
	dst := destination(t)

	plan, err := Build(src, dst, &Rules{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	result, err := Apply(dst, plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(result.Dangling) == 0 {
		t.Error("a link to a note outside the import was not reported")
	}

	imported, err := dst.Read("Sessions/2026/05/2026-05-01-work.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// Left as written: a migration that silently deletes references is worse
	// than one that leaves a lint finding.
	if !strings.Contains(imported.Body, "[[Apps/Skipped/Design]]") {
		t.Errorf("the dangling link was removed:\n%s", imported.Body)
	}
}
