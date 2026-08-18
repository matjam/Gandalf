package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
)

func TestLintCleanVault(t *testing.T) {
	h := newHarness(t)

	var out LintOutput
	h.call("gandalf_lint", LintInput{}, &out)

	if !out.Clean {
		t.Errorf("a seeded vault is not clean: %+v", out.Findings)
	}
	if out.Errors != 0 || out.Warnings != 0 {
		t.Errorf("errors = %d, warnings = %d, want none", out.Errors, out.Warnings)
	}
}

// TestLintReportsRefsNotPaths is the property that keeps findings actionable:
// a model must be able to feed a finding straight into another tool.
func TestLintReportsRefsNotPaths(t *testing.T) {
	h := newHarness(t)

	const broken = "---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [Bad Tag]\nauthor: agent\n---\n\n# Broken\n"
	if err := os.WriteFile(filepath.Join(h.vault.Root(), "Standards", "broken.md"), []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out LintOutput
	h.call("gandalf_lint", LintInput{}, &out)

	if out.Clean {
		t.Fatal("lint found nothing")
	}

	var found bool
	for _, f := range out.Findings {
		if strings.Contains(f.Ref, "/") || strings.HasSuffix(f.Ref, ".md") {
			t.Errorf("finding carries a path, not a ref: %q", f.Ref)
		}
		if f.Ref == "standard:broken" {
			found = true

			// The ref it reported must be usable against another tool.
			var note NoteOutput
			h.call("gandalf_note_read", NoteReadInput{Ref: f.Ref}, &note)
			if note.Title != "Broken" {
				t.Errorf("title = %q", note.Title)
			}
		}
	}
	if !found {
		t.Errorf("no finding for standard:broken: %+v", out.Findings)
	}
}

func TestLintSingleNote(t *testing.T) {
	h := newHarness(t)

	var created NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "standard", Title: "Fine", Tags: []string{"standards"},
	}, &created)

	var out LintOutput
	h.call("gandalf_lint", LintInput{Ref: created.Ref}, &out)
	if !out.Clean {
		t.Errorf("a tool-created note failed lint: %+v", out.Findings)
	}
}

func TestCorrect(t *testing.T) {
	h := newHarness(t)

	const guidance = "Prefer table-driven tests over repeated near-identical cases."

	var out CorrectOutput
	h.call("gandalf_correct", CorrectInput{
		Target:   "topic:code-quality",
		Guidance: guidance,
		Reason:   "Three near-identical tests were written where a table would have been clearer.",
	}, &out)

	// A seeded standard is reachable by its topic id, but what comes back is
	// the canonical ref for where it lives.
	if out.Ref != "standard:code-quality" {
		t.Errorf("ref = %q, want the canonical ref", out.Ref)
	}
	if out.History == "" {
		t.Error("a reason was given but no history entry was recorded")
	}

	var standard NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:code-quality"}, &standard)
	if !strings.Contains(standard.Content, guidance) {
		t.Error("the guidance was not written to the standard")
	}
	if standard.Updated != schema.Today().String() {
		t.Errorf("updated = %q, want today", standard.Updated)
	}

	// The reasoning goes to the history, and only there: the contract is read
	// every session and the incident behind a rule is not needed then.
	var history NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:corrections"}, &history)
	if !strings.Contains(history.Content, "Three near-identical tests") {
		t.Error("the reason was not recorded in the history")
	}
	if strings.Contains(standard.Content, "Three near-identical tests") {
		t.Error("the reason leaked into the standard")
	}
}

func TestCorrectDefaultsToTheContract(t *testing.T) {
	h := newHarness(t)

	const guidance = "Never comment on how long a session has run."

	var out CorrectOutput
	h.call("gandalf_correct", CorrectInput{Guidance: guidance}, &out)

	if out.Ref != "topic:operating" {
		t.Errorf("ref = %q, want the contract", out.Ref)
	}
	if out.History != "" {
		t.Error("history was written without a reason")
	}

	// A correction must reach the model on the next boot, or it did nothing.
	var boot BootOutput
	h.call("gandalf_boot", BootInput{}, &boot)
	for _, doc := range boot.Contract {
		if doc.ID == "operating" && strings.Contains(doc.Content, guidance) {
			return
		}
	}
	t.Error("the correction is not in the contract returned by boot")
}

func TestCorrectRejects(t *testing.T) {
	h := newHarness(t)

	if msg := h.callErr("gandalf_correct", CorrectInput{Target: "topic:operating"}); !strings.Contains(msg, "guidance") {
		t.Errorf("error = %q, want it to require guidance", msg)
	}
	if msg := h.callErr("gandalf_correct", CorrectInput{
		Target:   "topic:nonexistent",
		Guidance: "Something.",
	}); !strings.Contains(msg, "correction target") {
		t.Errorf("error = %q, want it to name the bad target", msg)
	}
}

// TestCorrectionSurvivesReseeding is the whole premise: a release must not be
// able to revert what the user corrected.
func TestCorrectionSurvivesReseeding(t *testing.T) {
	h := newHarness(t)

	const guidance = "Always run the full suite before pushing."
	h.call("gandalf_correct", CorrectInput{Target: "topic:shipping", Guidance: guidance}, nil)

	if _, err := instructions.Seed(h.vault, schema.Today(), false); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	var out NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:shipping"}, &out)
	if !strings.Contains(out.Content, guidance) {
		t.Error("re-seeding reverted a correction")
	}
}

// TestCorrectFilesUnderItsOwnHeading pins the placement of a recorded rule.
//
// A correction used to be appended to the end of the document, which filed it
// under whatever heading happened to be last — in the shipped contract, the
// session checklist. The rule was then read as part of a section it had
// nothing to do with.
func TestCorrectFilesUnderItsOwnHeading(t *testing.T) {
	h := newHarness(t)

	const guidance = "Keep personal projects outside the work source tree."

	h.call("gandalf_correct", CorrectInput{Guidance: guidance}, nil)

	var contract NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:operating"}, &contract)

	heading := "## " + CorrectionsSection
	at := strings.Index(contract.Content, heading)
	if at < 0 {
		t.Fatalf("no %q section was created", heading)
	}

	rule := strings.Index(contract.Content, guidance)
	if rule < at {
		t.Error("the rule was written above its own heading")
	}

	// Everything the document already said must still be above the new
	// section: a correction adds to the contract, it does not reorganise it.
	if last := strings.LastIndex(contract.Content[:at], "## "); last < 0 {
		t.Error("the corrections section displaced the document's own headings")
	}

	// A second correction joins the first rather than starting a new section.
	const second = "Say what you verified, not what you assume."
	h.call("gandalf_correct", CorrectInput{Guidance: second}, nil)

	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:operating"}, &contract)

	// Count heading lines, not substrings: the contract names the section in
	// its own prose, and an inline mention is not a second section.
	var sections int
	for _, line := range strings.Split(contract.Content, "\n") {
		if strings.TrimSpace(line) == heading {
			sections++
		}
	}
	if sections != 1 {
		t.Errorf("%d corrections sections, want 1", sections)
	}
	if !strings.Contains(contract.Content, guidance) || !strings.Contains(contract.Content, second) {
		t.Error("a second correction displaced the first")
	}
}

// TestCorrectHonoursAnExplicitSection lets a caller that knows where a rule
// belongs put it there, rather than in the catch-all.
func TestCorrectHonoursAnExplicitSection(t *testing.T) {
	h := newHarness(t)

	const guidance = "Run the race detector in CI."

	h.call("gandalf_correct", CorrectInput{
		Target:   "topic:shipping",
		Guidance: guidance,
		Section:  "Verification",
	}, nil)

	var topic NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "topic:shipping"}, &topic)

	if strings.Contains(topic.Content, CorrectionsSection) {
		t.Error("an explicit section was ignored in favour of the catch-all")
	}

	verification := strings.Index(topic.Content, "## Verification")
	rule := strings.Index(topic.Content, guidance)
	if verification < 0 || rule < verification {
		t.Fatal("the rule was not filed under the named section")
	}

	// It has to land inside that section, not merely after its heading: the
	// next heading down marks where the section ends.
	if next := strings.Index(topic.Content[verification+len("## Verification"):], "\n## "); next >= 0 {
		if rule > verification+len("## Verification")+next {
			t.Error("the rule landed past the end of the named section")
		}
	}
}
