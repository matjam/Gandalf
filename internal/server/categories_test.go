package server

import (
	"strings"
	"testing"
)

// find returns the named category from a listing.
func find(t *testing.T, out CategoryListOutput, name string) CategoryView {
	t.Helper()

	for _, c := range out.Categories {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no category %q in %+v", name, out.Categories)
	return CategoryView{}
}

func TestCategoryList(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out CategoryListOutput
	h.call("gandalf_category_list", CategoryListInput{}, &out)

	session := find(t, out, "session")
	if session.Rule != "dated" || session.RefForm != "session:YYYY-MM-DD-slug" {
		t.Errorf("session = %+v", session)
	}
	if session.Notes != 2 {
		t.Errorf("session notes = %d, want 2", session.Notes)
	}

	project := find(t, out, "project")
	if strings.Join(project.Facets, ",") != "design,decisions,todo" {
		t.Errorf("facets = %v", project.Facets)
	}
	if project.RefForm != "project:<scope>:design|decisions|todo" {
		t.Errorf("ref form = %q", project.RefForm)
	}

	// Interviews were folded into meetings.
	for _, c := range out.Categories {
		if c.Name == "interview" {
			t.Error("interview is still a category")
		}
	}
}

func TestCategoryCreate(t *testing.T) {
	h := newHarness(t)

	var created CategoryChangeOutput
	h.call("gandalf_category_create", CategoryCreateInput{
		Name: "incident", Plural: "incidents", Rule: "dated",
		Folder: "Incidents", Description: "Something broke and why.",
		Tags: []string{"incident"},
	}, &created)

	if created.Category.RefForm != "incident:YYYY-MM-DD-slug" {
		t.Errorf("ref form = %q", created.Category.RefForm)
	}

	// The new category must be usable immediately: filing, addressing, and
	// listing all derive from the declaration.
	var note NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "incident", Title: "Disk Filled Up", Tags: []string{"storage"},
	}, &note)

	if !strings.HasPrefix(note.Ref, "incident:") {
		t.Errorf("ref = %q, want an incident ref", note.Ref)
	}
	if !strings.HasSuffix(note.Ref, "-disk-filled-up") {
		t.Errorf("ref = %q, want it to end with the slug", note.Ref)
	}

	// The category's standing tags are applied on top of the requested ones.
	if got := strings.Join(note.Tags, ","); got != "incident,storage" {
		t.Errorf("tags = %q, want the category's tag first", got)
	}

	var read NoteOutput
	h.call("gandalf_note_read", NoteReadInput{Ref: note.Ref}, &read)
	if read.Title != "Disk Filled Up" {
		t.Errorf("title = %q", read.Title)
	}

	var listed ListOutput
	h.call("gandalf_list", ListInput{Kind: "incidents"}, &listed)
	if listed.Total != 1 {
		t.Errorf("listed %d incidents, want 1", listed.Total)
	}

	// And it survives a restart, because it lives in the vault.
	var again CategoryListOutput
	h.call("gandalf_category_list", CategoryListInput{}, &again)
	if find(t, again, "incident").Notes != 1 {
		t.Error("the new category did not persist with its note")
	}
}

func TestCategoryCreateScoped(t *testing.T) {
	h := newHarness(t)

	h.call("gandalf_category_create", CategoryCreateInput{
		Name: "client", Plural: "clients", Rule: "scoped",
		Folder: "Clients", Description: "Per-client notes.",
		Facets: []string{"brief", "contact-log"},
	}, nil)

	var note NoteOutput
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "client", Facet: "contact-log", Scope: "acme",
		Title: "Acme", Tags: []string{"client"},
	}, &note)

	if note.Ref != "client:acme:contact-log" {
		t.Errorf("ref = %q", note.Ref)
	}

	// Filenames are derived so a caller declaring a category need not invent
	// them, but they still have to be readable in the vault.
	raw := diskOf(t, h, note.Ref)
	if !strings.Contains(raw, "# Acme") {
		t.Errorf("note content:\n%s", raw)
	}
}

func TestCategoryCreateRejects(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name string
		in   CategoryCreateInput
		want string
	}{
		{
			name: "a name already taken",
			in:   CategoryCreateInput{Name: "session", Plural: "sittings", Rule: "dated", Folder: "Sittings"},
			want: "already exists",
		},
		{
			name: "a folder already used",
			in:   CategoryCreateInput{Name: "note", Plural: "notes", Rule: "named", Folder: "Standards"},
			want: "already uses",
		},
		{
			name: "a reserved name",
			in:   CategoryCreateInput{Name: "path", Plural: "paths", Rule: "named", Folder: "Paths"},
			want: "reserved",
		},
		{
			name: "an unknown rule",
			in:   CategoryCreateInput{Name: "note", Plural: "notes", Rule: "whenever", Folder: "Notes"},
			want: "unknown rule",
		},
		{
			name: "scoped with no facets",
			in:   CategoryCreateInput{Name: "client", Plural: "clients", Rule: "scoped", Folder: "Clients"},
			want: "at least one facet",
		},
		{
			name: "capitals in the name",
			in:   CategoryCreateInput{Name: "Incident", Plural: "incidents", Rule: "dated", Folder: "Incidents"},
			want: "lowercase",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if msg := h.callErr("gandalf_category_create", tc.in); !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tc.want)
			}
		})
	}
}

func TestCategoryRetire(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	var out CategoryChangeOutput
	h.call("gandalf_category_retire", CategoryNameInput{Name: "meeting"}, &out)

	if !out.Category.Retired {
		t.Error("the category is not marked retired")
	}

	// No new notes.
	if msg := h.callErr("gandalf_note_new", NoteNewInput{
		Kind: "meeting", Title: "Another", Tags: []string{"meeting"},
	}); !strings.Contains(msg, "cannot be created") {
		t.Errorf("error = %q", msg)
	}

	// The ones already filed stay readable and writable, which is the whole
	// difference between retiring and deleting.
	var listed ListOutput
	h.call("gandalf_list", ListInput{Kind: "meetings"}, &listed)
	if listed.Total != 1 {
		t.Fatalf("listed %d meetings, want the existing one", listed.Total)
	}

	ref := listed.Notes[0].Ref
	h.call("gandalf_note_append", NoteAppendInput{Ref: ref, Content: "Still editable."}, nil)

	if msg := h.callErr("gandalf_category_retire", CategoryNameInput{Name: "meeting"}); !strings.Contains(msg, "already retired") {
		t.Errorf("error = %q", msg)
	}
}

// TestCategoryDeleteRequiresEmpty is the safeguard: deleting a category that
// still holds notes would leave them addressable only by path, which loses
// them quietly.
func TestCategoryDeleteRequiresEmpty(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	msg := h.callErr("gandalf_category_delete", CategoryNameInput{Name: "session"})
	for _, want := range []string{"still holds", "retire"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to mention %q", msg, want)
		}
	}

	// Sessions are untouched.
	var listed ListOutput
	h.call("gandalf_list", ListInput{Kind: "sessions"}, &listed)
	if listed.Total != 2 {
		t.Errorf("listed %d sessions, want them all still there", listed.Total)
	}
}

func TestCategoryDelete(t *testing.T) {
	h := newHarness(t)

	h.call("gandalf_category_create", CategoryCreateInput{
		Name: "incident", Plural: "incidents", Rule: "dated",
		Folder: "Incidents", Description: "Something broke.",
	}, nil)

	var out CategoryChangeOutput
	h.call("gandalf_category_delete", CategoryNameInput{Name: "incident"}, &out)
	if out.Category.Name != "incident" {
		t.Errorf("deleted %q", out.Category.Name)
	}

	var listing CategoryListOutput
	h.call("gandalf_category_list", CategoryListInput{}, &listing)
	for _, c := range listing.Categories {
		if c.Name == "incident" {
			t.Error("the deleted category is still declared")
		}
	}

	if msg := h.callErr("gandalf_category_delete", CategoryNameInput{Name: "incident"}); !strings.Contains(msg, "no category") {
		t.Errorf("error = %q", msg)
	}
	if msg := h.callErr("gandalf_note_new", NoteNewInput{Kind: "incident", Title: "Gone"}); !strings.Contains(msg, "unknown category") {
		t.Errorf("error = %q", msg)
	}
}

// TestSeededVaultStaysCleanAfterCategoryChanges guards the invariant that the
// vault's own linter must keep passing while its shape is edited.
func TestSeededVaultStaysCleanAfterCategoryChanges(t *testing.T) {
	h := newHarness(t)
	populate(t, h)

	h.call("gandalf_category_create", CategoryCreateInput{
		Name: "incident", Plural: "incidents", Rule: "dated",
		Folder: "Incidents", Description: "Something broke.",
	}, nil)
	h.call("gandalf_note_new", NoteNewInput{
		Kind: "incident", Title: "Disk Filled Up", Tags: []string{"storage"},
	}, nil)
	h.call("gandalf_category_retire", CategoryNameInput{Name: "meeting"}, nil)

	var out LintOutput
	h.call("gandalf_lint", LintInput{}, &out)
	if !out.Clean {
		t.Errorf("changing categories left the vault unclean: %+v", out.Findings)
	}
}
