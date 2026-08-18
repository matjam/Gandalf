package server

import (
	"strings"
	"testing"
)

// populate fills a vault with a little of everything, so listings have
// something to distinguish.
func populate(t *testing.T, h *harness) {
	t.Helper()

	for _, in := range []NoteNewInput{
		{Kind: "project", Facet: "design", Title: "Blitter", Scope: "blitter", Tags: []string{"design"}},
		{Kind: "project", Facet: "decisions", Title: "Blitter", Scope: "blitter", Tags: []string{"decisions"}},
		{Kind: "project", Facet: "todo", Title: "Blitter", Scope: "blitter", Tags: []string{"todo"}},
		{Kind: "project", Facet: "design", Title: "Egg", Scope: "egg", Tags: []string{"design"}},
		{Kind: "standard", Title: "Language Rust", Tags: []string{"standards"}},
		{Kind: "meeting", Title: "Planning", Tags: []string{"meeting"}},
	} {
		h.call("gandalf_note_new", in, nil)
	}

	h.call("gandalf_session_start", SessionStartInput{Title: "Earlier Work", Tags: []string{"work"}}, nil)
	h.call("gandalf_session_start", SessionStartInput{Title: "Later Work", Tags: []string{"work"}}, nil)
}

func TestListSessions(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "sessions"}, &out)

	if out.Total != 2 {
		t.Fatalf("total = %d, want 2: %+v", out.Total, out.Notes)
	}
	for _, n := range out.Notes {
		if !strings.HasPrefix(n.Ref, "session:") {
			t.Errorf("ref = %q, want a session", n.Ref)
		}
		if n.Title == "" || n.Updated == "" {
			t.Errorf("summary is missing fields: %+v", n)
		}
		if n.Status != "in-progress" {
			t.Errorf("status = %q", n.Status)
		}
	}
}

// TestListReturnsNoContent keeps a listing cheap: it is for finding a note,
// not for reading one.
func TestListReturnsNoContent(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	res, err := h.client.CallTool(h.context, callParams("gandalf_list", ListInput{Kind: "all"}))
	if err != nil {
		t.Fatalf("gandalf_list: %v", err)
	}
	if strings.Contains(text(res), "## ") {
		t.Error("a listing returned note content")
	}
}

func TestListProjects(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "projects"}, &out)

	if len(out.Projects) != 2 {
		t.Fatalf("projects = %+v, want two", out.Projects)
	}

	first := out.Projects[0]
	if first.Name != "blitter" {
		t.Errorf("first project = %q, want blitter (sorted)", first.Name)
	}
	want := "project:blitter:decisions,project:blitter:design,project:blitter:todo"
	if got := strings.Join(first.Notes, ","); got != want {
		t.Errorf("notes = %q, want %q", got, want)
	}

	// Every ref a listing reports must be readable.
	for _, ref := range first.Notes {
		var note NoteOutput
		h.call("gandalf_note_read", NoteReadInput{Ref: ref}, &note)
		if note.Ref != ref {
			t.Errorf("read %q returned ref %q", ref, note.Ref)
		}
	}
}

func TestListProjectFilter(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "projects", Scope: "egg"}, &out)

	if len(out.Projects) != 1 || out.Projects[0].Name != "egg" {
		t.Fatalf("projects = %+v, want only egg", out.Projects)
	}
	if got := strings.Join(out.Projects[0].Notes, ","); got != "project:egg:design" {
		t.Errorf("notes = %q, want only the design note", got)
	}
}

func TestListStandardsIncludesSeeded(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "standards", Limit: 100}, &out)

	found := map[string]bool{}
	for _, n := range out.Notes {
		found[n.Ref] = true
	}
	for _, want := range []string{"standard:language-go", "standard:privacy", "standard:language-rust"} {
		if !found[want] {
			t.Errorf("%s missing from the listing", want)
		}
	}
}

func TestListTopics(t *testing.T) {
	h := newHarness(t)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "topics"}, &out)

	if len(out.Topics) == 0 {
		t.Fatal("no topics listed")
	}

	// The listing must agree with what boot advertises.
	var boot BootOutput
	h.call("gandalf_boot", BootInput{}, &boot)
	if len(out.Topics) != len(boot.Topics) {
		t.Errorf("listed %d topics, boot advertises %d", len(out.Topics), len(boot.Topics))
	}
	for i, topic := range out.Topics {
		if topic.Ref != boot.Topics[i].Ref {
			t.Errorf("topic %d = %q, boot says %q", i, topic.Ref, boot.Topics[i].Ref)
		}
		if topic.When == "" {
			t.Errorf("%s has no guidance on when to read it", topic.Ref)
		}
	}
}

func TestListAllExcludesUnconventionalNotes(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	// A note following none of the conventions is readable but not listed.
	writeUnmanaged(t, h, "Personal/Something.md")

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "all", Limit: 200}, &out)

	for _, n := range out.Notes {
		if strings.HasPrefix(n.Ref, "path:") {
			t.Errorf("listing includes an unconventional note: %q", n.Ref)
		}
	}
}

func TestListLimit(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{Kind: "all", Limit: 3}, &out)

	if len(out.Notes) != 3 {
		t.Errorf("returned %d notes, want 3", len(out.Notes))
	}
	if !out.Truncated {
		t.Error("truncated = false despite the limit applying")
	}
	if out.Total <= 3 {
		t.Errorf("total = %d, want the count before truncation", out.Total)
	}
}

func TestListRejectsUnknownKind(t *testing.T) {
	h := newHarness(t)

	msg := h.callErr("gandalf_list", ListInput{Kind: "diaries"})
	for _, want := range []string{"unknown kind", "sessions", "projects", "topics"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message omits %q: %q", want, msg)
		}
	}
}

func TestListDefaultsToAll(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out ListOutput
	h.call("gandalf_list", ListInput{}, &out)

	if out.Kind != "all" {
		t.Errorf("kind = %q, want all", out.Kind)
	}
	if out.Total == 0 {
		t.Error("no notes listed")
	}
}
