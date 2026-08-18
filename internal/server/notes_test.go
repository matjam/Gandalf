package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/schema"
)

func TestSessionStart(t *testing.T) {
	h := newHarness(t)

	var out SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{
		Title: "Memory Toolset Design",
		Tags:  []string{"agent", "memory"},
	}, &out)

	if !out.Created {
		t.Error("created = false on a fresh vault")
	}
	want := "session:" + schema.Today().String() + "-memory-toolset-design"
	if out.Ref != want {
		t.Errorf("ref = %q, want %q", out.Ref, want)
	}

	// The ref must address the note it just created.
	var note NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: out.Ref}, &note)
	if note.Title != "Memory Toolset Design" {
		t.Errorf("title = %q", note.Title)
	}
	if note.Status != string(schema.StatusInProgress) {
		t.Errorf("status = %q, want in-progress", note.Status)
	}
	if note.Type != sessionCategory {
		t.Errorf("type = %q, want session", note.Type)
	}
}

// TestSessionStartTwiceContinues checks that a repeated call returns the
// existing note rather than replacing it. Losing a session's record to a
// duplicate title would be the worst possible failure of a memory tool.
func TestSessionStartTwiceContinues(t *testing.T) {
	h := newHarness(t)

	var first SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Same Work", Tags: []string{"work"}}, &first)

	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     first.Ref,
		Content: "Something worth keeping.",
	}, nil)

	var second SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Same Work", Tags: []string{"work"}}, &second)

	if second.Ref != first.Ref {
		t.Fatalf("second ref = %q, want %q", second.Ref, first.Ref)
	}
	if second.Created {
		t.Error("created = true for an existing note")
	}

	var note NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: first.Ref}, &note)
	if !strings.Contains(note.Content, "Something worth keeping.") {
		t.Error("the existing session note was overwritten")
	}
}

// TestBootReportsOpenSessions is what lets a model recover a ref it lost.
func TestBootReportsOpenSessions(t *testing.T) {
	h := newHarness(t)

	var started SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Open Work", Tags: []string{"work"}}, &started)

	var out BootOutput
	h.call("gandalf_boot", BootInput{}, &out)

	if len(out.OpenToday) != 1 {
		t.Fatalf("open sessions = %+v, want exactly one", out.OpenToday)
	}
	if out.OpenToday[0].Ref != started.Ref {
		t.Errorf("ref = %q, want %q", out.OpenToday[0].Ref, started.Ref)
	}
	if out.OpenToday[0].Title != "Open Work" {
		t.Errorf("title = %q", out.OpenToday[0].Title)
	}

	// Once complete, it stops being offered for resumption.
	h.call("gandalf_note_update", NoteUpdateInput{
		Ref:    started.Ref,
		Status: string(schema.StatusComplete),
	}, nil)

	h.call("gandalf_boot", BootInput{}, &out)
	if len(out.OpenToday) != 0 {
		t.Errorf("a completed session is still reported open: %+v", out.OpenToday)
	}
}

func TestNoteNew(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name    string
		in      NoteNewInput
		wantRef string
	}{
		{
			name:    "design",
			in:      NoteNewInput{Kind: "project", Facet: "design", Title: "Gandalf", Scope: "gandalf", Tags: []string{"design"}},
			wantRef: "project:gandalf:design",
		},
		{
			name:    "decisions",
			in:      NoteNewInput{Kind: "project", Facet: "decisions", Title: "Gandalf", Scope: "gandalf", Tags: []string{"design"}},
			wantRef: "project:gandalf:decisions",
		},
		{
			name:    "todo",
			in:      NoteNewInput{Kind: "project", Facet: "todo", Title: "Gandalf", Scope: "gandalf", Tags: []string{"todo"}},
			wantRef: "project:gandalf:todo",
		},
		{
			name:    "standard",
			in:      NoteNewInput{Kind: "standard", Title: "Language Rust", Tags: []string{"standards"}},
			wantRef: "standard:language-rust",
		},
		{
			name:    "meeting",
			in:      NoteNewInput{Kind: "meeting", Title: "Planning", Tags: []string{"meeting"}},
			wantRef: "meeting:" + schema.Today().String() + "-planning",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out NoteOutput
			h.call("gandalf_note_new", tc.in, &out)

			if out.Ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", out.Ref, tc.wantRef)
			}

			// Whatever it returned must be readable by that ref.
			var read NoteOutput
			h.call("gandalf_note_read", NoteReadInput{Ref: out.Ref}, &read)
			if read.Title != tc.in.Title {
				t.Errorf("title = %q, want %q", read.Title, tc.in.Title)
			}
		})
	}
}

func TestNoteNewRejects(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name string
		in   NoteNewInput
		want string
	}{
		{
			name: "unknown category",
			in:   NoteNewInput{Kind: "diary", Title: "Today"},
			want: "unknown category",
		},
		{
			name: "project note without a project",
			in:   NoteNewInput{Kind: "project", Facet: "design", Title: "Something"},
			want: "scope",
		},
		{
			name: "session, which has its own tool",
			in:   NoteNewInput{Kind: "session", Title: "Work"},
			want: "gandalf_session_start",
		},
		{
			name: "unusable title",
			in:   NoteNewInput{Kind: "standard", Title: "!!!"},
			want: "empty name",
		},
		{
			name: "a category that cannot be created through the tools",
			in:   NoteNewInput{Kind: "readme", Title: "Folder"},
			want: "cannot be created",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if msg := h.callErr("gandalf_note_new", tc.in); !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tc.want)
			}
		})
	}
}

// TestNoteNewRefusesToReplace matters most for decisions notes, which are
// append-only by convention and irreplaceable in practice.
func TestNoteNewRefusesToReplace(t *testing.T) {
	h := newHarness(t)

	in := NoteNewInput{Kind: "project", Facet: "decisions", Title: "Gandalf", Scope: "gandalf", Tags: []string{"decisions"}}
	h.call("gandalf_note_new", in, nil)

	msg := h.callErr("gandalf_note_new", in)
	if !strings.Contains(msg, "already exists") || !strings.Contains(msg, "append") {
		t.Errorf("error = %q, want it to refuse and point at append", msg)
	}
}

func TestNoteAppend(t *testing.T) {
	h := newHarness(t)

	var created NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "project", Facet: "decisions", Title: "Gandalf", Scope: "gandalf", Tags: []string{"decisions"},
	}, &created)

	var out NoteOutput
	h.call("gandalf_note_append", NoteAppendInput{
		Ref:     created.Ref,
		Heading: "2026-08-17 — Refs, not paths",
		Content: "Tools address notes by what they are.",
	}, &out)

	if !strings.Contains(out.Content, "## 2026-08-17 — Refs, not paths") {
		t.Errorf("heading was not added:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "Tools address notes by what they are.") {
		t.Errorf("content was not appended:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "# Gandalf") {
		t.Errorf("appending destroyed the original body:\n%s", out.Content)
	}

	// A second append keeps the first.
	h.call("gandalf_note_append", NoteAppendInput{Ref: created.Ref, Content: "A later decision."}, &out)
	if !strings.Contains(out.Content, "Tools address notes by what they are.") {
		t.Error("the second append overwrote the first")
	}
	if !strings.Contains(out.Content, "A later decision.") {
		t.Error("the second append is missing")
	}
}

func TestNoteAppendRejectsEmpty(t *testing.T) {
	h := newHarness(t)

	var created SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Work", Tags: []string{"work"}}, &created)

	if msg := h.callErr("gandalf_note_append", NoteAppendInput{Ref: created.Ref, Content: "   "}); !strings.Contains(msg, "nothing to append") {
		t.Errorf("error = %q", msg)
	}
}

func TestNoteUpdate(t *testing.T) {
	h := newHarness(t)

	var created NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "standard", Title: "Language Rust", Tags: []string{"standards", "temporary"},
	}, &created)

	var out NoteOutput
	h.call("gandalf_note_update", NoteUpdateInput{
		Ref:        created.Ref,
		AddTags:    []string{"language-rust"},
		RemoveTags: []string{"temporary"},
		AddRelated: []string{"topic:operating"},
		Status:     string(schema.StatusComplete),
	}, &out)

	if got := strings.Join(out.Tags, ","); got != "standards,language-rust" {
		t.Errorf("tags = %q, want standards,language-rust", got)
	}
	// Refs at the tool boundary, paths on disk: the model gets something it
	// can pass back, Obsidian gets a link it can follow.
	if got := strings.Join(out.Related, ","); got != "topic:operating" {
		t.Errorf("related = %q, want a ref", got)
	}
	if raw := diskOf(t, h, created.Ref); !strings.Contains(raw, `"[[Gandalf/Operating]]"`) {
		t.Errorf("frontmatter does not hold an Obsidian-resolvable path:\n%s", raw)
	}
	if out.Status != string(schema.StatusComplete) {
		t.Errorf("status = %q", out.Status)
	}

	// The body is metadata's business only.
	if !strings.Contains(out.Content, "# Language Rust") {
		t.Error("update touched the body")
	}
}

func TestNoteUpdateRejectsInvalid(t *testing.T) {
	h := newHarness(t)

	var created NoteOutput
	h.call("gandalf_note_new", NoteNewInput{Kind: "standard", Title: "Thing", Tags: []string{"standards"}}, &created)

	if msg := h.callErr("gandalf_note_update", NoteUpdateInput{Ref: created.Ref, Status: "wip"}); !strings.Contains(msg, "status") {
		t.Errorf("error = %q, want it to reject the status", msg)
	}
	if msg := h.callErr("gandalf_note_update", NoteUpdateInput{
		Ref:     created.Ref,
		AddTags: []string{"Not A Tag"},
	}); !strings.Contains(msg, "invalid") {
		t.Errorf("error = %q, want it to refuse an invalid tag", msg)
	}
}

// TestPathRefsAreReadOnly checks the property that keeps the write surface
// equal to the filing conventions: notes outside them can be read but not
// written.
func TestPathRefsAreReadOnly(t *testing.T) {
	h := newHarness(t)

	const note = "---\ntype: standard\ncreated: 2026-01-01\nupdated: 2026-01-01\ntags: [personal]\nauthor: user\n---\n\n# Not A Convention\n\nProse.\n"
	dir := filepath.Join(h.vault.Root(), "Personal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Something.md"), []byte(note), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	const ref = "path:Personal/Something"

	var out NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: ref}, &out)
	if out.Title != "Not A Convention" {
		t.Errorf("title = %q", out.Title)
	}

	for _, tc := range []struct {
		tool string
		args any
	}{
		{tool: "gandalf_note_append", args: NoteAppendInput{Ref: ref, Content: "More."}},
		{tool: "gandalf_note_update", args: NoteUpdateInput{Ref: ref, AddTags: []string{"edited"}}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			if msg := h.callErr(tc.tool, tc.args); !strings.Contains(msg, "filing conventions") {
				t.Errorf("error = %q, want a refusal citing the conventions", msg)
			}
		})
	}
}

func TestUnknownRefsAreRejected(t *testing.T) {
	h := newHarness(t)

	for _, ref := range []string{
		"Sessions/2026/08/2026-08-17-work.md", // a path, which is what we are preventing
		"diary:today",
		"project:gandalf",
		"session:latest", // nothing to be latest of yet
	} {
		t.Run(ref, func(t *testing.T) {
			if msg := h.callErr("gandalf_note_read", NoteReadInput{Ref: ref}); msg == "" {
				t.Error("no error message")
			}
		})
	}
}

func TestSessionLatest(t *testing.T) {
	h := newHarness(t)

	var first, second SessionStartOutput
	h.call("gandalf_session_start", SessionStartInput{Title: "Earlier Work", Tags: []string{"work"}}, &first)
	h.call("gandalf_session_start", SessionStartInput{Title: "Later Work", Tags: []string{"work"}}, &second)

	var out NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: "session:latest"}, &out)

	// Same-day names sort by slug, so assert only that it resolved to one of
	// them rather than pretending the ordering means recency within a day.
	if out.Ref != first.Ref && out.Ref != second.Ref {
		t.Errorf("ref = %q, want one of the two sessions", out.Ref)
	}
}
