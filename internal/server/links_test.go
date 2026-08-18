package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// diskOf returns the raw bytes of the note a ref addresses, bypassing the
// tools, so a test can assert what Obsidian and git will actually see rather
// than what the tools report.
func diskOf(t *testing.T, h *harness, ref string) string {
	t.Helper()

	s := New(h.vault, "test")
	_, path, err := s.resolve(ref)
	if err != nil {
		t.Fatalf("resolve %q: %v", ref, err)
	}

	data, err := os.ReadFile(filepath.Join(h.vault.Root(), filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

// TestBodyLinksAreStoredAsPaths is the property that keeps the vault usable
// outside Gandalf: a model writes refs, Obsidian gets paths.
func TestBodyLinksAreStoredAsPaths(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Linking Work", Tags: []string{"work"}}, &session)

	h.call("gandalf_note_append", NoteAppendInput{
		Ref: session.Ref,
		Content: "Applied [[standard:language-go]] and the shipping topic " +
			"[[topic:shipping|as written]], plus a heading link [[standard:privacy#Classification]].",
	}, nil)

	raw := diskOf(t, h, session.Ref)

	for _, want := range []string{
		"[[Standards/language-go]]",
		"[[Gandalf/Shipping|as written]]",
		"[[Standards/privacy#Classification]]",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("file does not contain %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "standard:") || strings.Contains(raw, "topic:") {
		t.Errorf("a ref was written to disk:\n%s", raw)
	}
}

// TestBodyLinksAreReturnedAsRefs is the other half: what the model reads back
// is addressable.
func TestBodyLinksAreReturnedAsRefs(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Linking Work", Tags: []string{"work"}}, &session)
	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Applied [[standard:language-go]] and [[topic:shipping|as written]].",
	}, nil)

	var out NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: session.Ref}, &out)

	for _, want := range []string{"[[standard:language-go]]", "[[topic:shipping|as written]]"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("content does not contain %q:\n%s", want, out.Content)
		}
	}
	if strings.Contains(out.Content, "Standards/") || strings.Contains(out.Content, "Gandalf/") {
		t.Errorf("a path leaked into the tool result:\n%s", out.Content)
	}
}

func TestLinkRoundTripIsStable(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Round Trip", Tags: []string{"work"}}, &session)

	const content = "See [[standard:language-go]] for detail."
	h.call("gandalf_note_append", NoteAppendInput{Ref: session.Ref, Content: content}, nil)

	var first NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: session.Ref}, &first)

	// Feeding a tool's own output back in must not double-translate.
	h.call("gandalf_note_append", NoteAppendInput{Ref: session.Ref, Content: first.Content}, nil)

	raw := diskOf(t, h, session.Ref)
	if strings.Contains(raw, "standard:language-go") {
		t.Errorf("a ref survived to disk on the second pass:\n%s", raw)
	}
	if n := strings.Count(raw, "[[Standards/language-go]]"); n != 2 {
		t.Errorf("expected two path links, got %d:\n%s", n, raw)
	}
}

// TestLinksToMissingNotesAreRefused covers what translation makes possible:
// at the moment a ref becomes a path, we know whether the note is there.
func TestLinksToMissingNotesAreRefused(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &session)

	tests := []struct {
		name string
		tool string
		args any
		want string
	}{
		{
			name: "body link from append",
			tool: "gandalf_note_append",
			args: NoteAppendInput{Ref: session.Ref, Content: "See [[project:nonexistent:design]]."},
			want: "project:nonexistent:design",
		},
		{
			name: "related on a new note",
			tool: "gandalf_note_new",
			args: NoteNewInput{
				Kind: "standard", Title: "Thing", Tags: []string{"standards"},
				Related: []string{"standard:imaginary"},
			},
			want: "standard:imaginary",
		},
		{
			name: "body link on a new note",
			tool: "gandalf_note_new",
			args: NoteNewInput{
				Kind: "standard", Title: "Other Thing", Tags: []string{"standards"},
				Content: "Refer to [[standard:also-imaginary]].",
			},
			want: "standard:also-imaginary",
		},
		{
			name: "related added by update",
			tool: "gandalf_note_update",
			args: NoteUpdateInput{Ref: session.Ref, AddRelated: []string{"session:2020-01-01-never-happened"}},
			want: "session:2020-01-01-never-happened",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := h.callErr(tc.tool, tc.args)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("message does not name the missing target %q: %q", tc.want, msg)
			}
			if !strings.Contains(msg, "do not exist") {
				t.Errorf("message does not explain the problem: %q", msg)
			}
		})
	}
}

// TestMissingLinksAreReportedTogether keeps the failure to one round trip.
func TestMissingLinksAreReportedTogether(t *testing.T) {
	h := newHarness(t)

	msg := h.callErr("gandalf_note_new", NoteNewInput{
		Kind: "standard", Title: "Thing", Tags: []string{"standards"},
		Related: []string{"standard:missing-one"},
		Content: "Also [[standard:missing-two]] and [[project:ghost:todo]].",
	})

	for _, want := range []string{"standard:missing-one", "standard:missing-two", "project:ghost:todo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message omits %q: %q", want, msg)
		}
	}
}

// TestNonRefLinksAreLeftAlone protects prose. Gandalf owns refs, not every
// bracket a user ever typed.
func TestNonRefLinksAreLeftAlone(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Prose", Tags: []string{"work"}}, &session)

	const content = "A hand-written link [[Some Other Note]], an array index arr[[0]], " +
		"and a fenced block:\n\n```\n[[standard:not-translated-in-code]]\n```\n"
	h.call("gandalf_note_append", NoteAppendInput{Ref: session.Ref, Content: content}, nil)

	raw := diskOf(t, h, session.Ref)
	for _, want := range []string{
		"[[Some Other Note]]",
		"arr[[0]]",
		"[[standard:not-translated-in-code]]",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("file does not preserve %q:\n%s", want, raw)
		}
	}
}

// TestSeededVaultStillLintsCleanAfterLinking guards against the translation
// producing links the vault's own linter rejects.
func TestSeededVaultStillLintsCleanAfterLinking(t *testing.T) {
	h := newHarness(t)

	var session SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{
		Title:   "Linked Work",
		Tags:    []string{"work"},
		Related: []string{"topic:operating", "standard:language-go"},
	}, &session)

	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     session.Ref,
		Content: "Following [[standard:code-quality]].",
	}, nil)

	var out LintOutput
	h.call("gandalf_lint", LintInput{}, &out)
	if !out.Clean {
		t.Errorf("linking produced findings: %+v", out.Findings)
	}
}
