package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/git"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// testReach is a Reach over a personal vault and any number of team vaults,
// whose membership a test can end while a session is open.
type testReach struct {
	personal *Core

	mu      sync.Mutex
	order   []string
	teams   map[string]*Core
	names   map[string]string
	revoked map[string]bool
}

func newTestReach(personal *Core) *testReach {
	return &testReach{
		personal: personal,
		teams:    map[string]*Core{},
		names:    map[string]string{},
		revoked:  map[string]bool{},
	}
}

func (r *testReach) add(slug, name string, c *Core) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.order = append(r.order, slug)
	r.teams[slug] = c
	r.names[slug] = name
}

// revoke ends the caller's membership of a team, as an admin removing them
// would while their session is still connected.
func (r *testReach) revoke(slug string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.revoked[slug] = true
}

func (r *testReach) Personal() *Core { return r.personal }

func (r *testReach) Team(_ context.Context, slug string) (*Core, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// An unknown team and a revoked one are the same answer, as the port
	// requires.
	if c, ok := r.teams[slug]; ok && !r.revoked[slug] {
		return c, nil
	}
	return nil, ErrNoAccess
}

func (r *testReach) Teams(context.Context) ([]TeamInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []TeamInfo
	for _, slug := range r.order {
		if !r.revoked[slug] {
			out = append(out, TeamInfo{Slug: slug, Name: r.names[slug]})
		}
	}
	return out, nil
}

// testCore opens a core over a fresh temporary vault.
func testCore(t *testing.T, seeded, searchable bool) *Core {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if seeded {
		if _, err := instructions.Seed(v, schema.Today(), false); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}

	repo := git.Open(v.Root())
	if err := repo.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var embedder embed.Embedder
	if searchable {
		embedder = embed.Fake{}
	}

	c := NewCore(v, "test", embedder, repo)
	t.Cleanup(func() { c.Close() })
	return c
}

// multi is a client connected to one session that reaches a personal vault and
// two team vaults: platform, which is seeded like a real vault, and data,
// which is empty.
type multi struct {
	*harness

	reach    *testReach
	srv      *Server
	personal *Core
	platform *Core
	data     *Core
}

type multiOptions struct {
	search    bool
	author    *git.Author
	allow     func(string) bool
	configure func(*Server)
}

func newMulti(t *testing.T, opts multiOptions) *multi {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	m := &multi{
		personal: testCore(t, true, opts.search),
		platform: testCore(t, true, opts.search),
		data:     testCore(t, false, opts.search),
	}
	m.reach = newTestReach(m.personal)
	m.reach.add("platform", "Platform Engineering", m.platform)
	m.reach.add("data", "Data", m.data)

	m.srv = NewSession(m.reach, opts.author)
	if opts.configure != nil {
		opts.configure(m.srv)
	}

	m.harness = connect(t, m.srv.MCPFor(opts.allow), m.srv, m.personal.vault)
	return m
}

// connect attaches a client to an MCP server over an in-memory transport.
func connect(t *testing.T, mcp *sdk.Server, s *Server, v *vault.Vault) *harness {
	t.Helper()

	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	go func() { _ = mcp.Run(ctx, serverTransport) }()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return &harness{t: t, client: session, server: s, vault: v, context: ctx}
}

// another connects a second client with its own session over the same vaults,
// so what the first has read means nothing to it.
func (m *multi) another() *harness {
	m.t.Helper()
	s := m.srv.Session()
	return connect(m.t, s.MCP(), s, m.personal.vault)
}

// diskIn reads a note's file from a core's vault, as Obsidian would see it.
func diskIn(t *testing.T, c *Core, ref string) string {
	t.Helper()

	_, path, err := (&scoped{Core: c}).resolve(ref)
	if err != nil {
		t.Fatalf("resolve %q: %v", ref, err)
	}
	data, err := os.ReadFile(filepath.Join(c.vault.Root(), filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func TestSplitScope(t *testing.T) {
	tests := []struct {
		raw      string
		wantSlug string
		wantRest string
		wantErr  bool
	}{
		{raw: "standard:testing", wantRest: "standard:testing"},
		{raw: "  session:latest ", wantRest: "session:latest"},
		{raw: "@platform:standard:testing", wantSlug: "platform", wantRest: "standard:testing"},
		{raw: "@platform:project:api:design", wantSlug: "platform", wantRest: "project:api:design"},
		{raw: "@platform:contract", wantSlug: "platform", wantRest: "contract"},
		{raw: "@my-team.2:topic:review", wantSlug: "my-team.2", wantRest: "topic:review"},
		{raw: "@platform", wantErr: true},
		{raw: "@platform:", wantErr: true},
		{raw: "@:standard:x", wantErr: true},
		{raw: "@bad slug:standard:x", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			slug, rest, err := splitScope(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if slug != tc.wantSlug || rest != tc.wantRest {
				t.Errorf("= (%q, %q), want (%q, %q)", slug, rest, tc.wantSlug, tc.wantRest)
			}
		})
	}
}

// TestTeamNotesAreReadAndWrittenInTheTeamVault is the basic routing property:
// a vault parameter or a ref prefix puts the work in the team's vault and
// nowhere else, and every ref that comes back says which vault it is in.
func TestTeamNotesAreReadAndWrittenInTheTeamVault(t *testing.T) {
	m := newMulti(t, multiOptions{})

	var created NoteOutput
	m.call("note_new", NoteNewInput{
		Kind: "standard", Title: "Testing", Content: "Write table-driven tests.", Vault: "platform",
	}, &created)
	if created.Ref != "@platform:standard:testing" {
		t.Fatalf("ref = %q, want the team-scoped ref", created.Ref)
	}

	// It landed in the team's vault, and the personal vault never heard of it.
	if !strings.Contains(diskIn(t, m.platform, "standard:testing"), "table-driven") {
		t.Error("the note is not in the team vault")
	}
	if m.personal.vault.Exists("Standards/testing.md") {
		t.Error("the note leaked into the personal vault")
	}

	var read NoteOutput
	m.call("note_read", NoteReadInput{Ref: created.Ref}, &read)
	if read.Ref != "@platform:standard:testing" || !strings.Contains(read.Content, "table-driven") {
		t.Errorf("read = %+v", read)
	}

	var appended NoteOutput
	m.call("note_append", NoteAppendInput{Ref: created.Ref, Heading: "Coverage", Content: "Aim for the branches."}, &appended)
	if appended.Ref != created.Ref {
		t.Errorf("append ref = %q", appended.Ref)
	}

	var replaced NoteReplaceOutput
	m.call("note_replace", NoteReplaceInput{Ref: created.Ref, Section: "Coverage", Content: "Aim for behaviour."}, &replaced)
	if replaced.Ref != created.Ref || !strings.Contains(replaced.Removed, "branches") {
		t.Errorf("replace = %+v", replaced)
	}

	var updated NoteOutput
	m.call("note_update", NoteUpdateInput{Ref: created.Ref, AddTags: []string{"quality"}}, &updated)
	if updated.Ref != created.Ref || !slices.Contains(updated.Tags, "quality") {
		t.Errorf("update = %+v", updated)
	}

	disk := diskIn(t, m.platform, "standard:testing")
	if !strings.Contains(disk, "Aim for behaviour.") || strings.Contains(disk, "branches") {
		t.Errorf("the team note was not rewritten:\n%s", disk)
	}

	// And a delete goes to the same place.
	var deleted NoteDeleteOutput
	m.call("note_delete", NoteDeleteInput{Ref: created.Ref}, &deleted)
	if deleted.Ref != created.Ref || m.platform.vault.Exists("Standards/testing.md") {
		t.Errorf("delete = %+v", deleted)
	}
}

// TestScopedRefsInEveryOutput covers the places a ref leaves the server: the
// links inside content, the backlinks block, related, and the results of the
// tools that list or find notes.
func TestScopedRefsInEveryOutput(t *testing.T) {
	m := newMulti(t, multiOptions{search: true})

	m.call("note_new", NoteNewInput{
		Kind: "project", Scope: "api", Facet: "design", Title: "API", Content: "How the API works.", Vault: "platform",
	}, nil)

	var standard NoteOutput
	m.call("note_new", NoteNewInput{
		Kind: "standard", Title: "Testing", Vault: "platform",
		Content: "See [[@platform:project:api:design]] and [[project:api:design#Intro|the design]].",
	}, &standard)

	t.Run("content links are translated on read", func(t *testing.T) {
		var read NoteOutput
		m.call("note_read", NoteReadInput{Ref: standard.Ref}, &read)

		for _, want := range []string{
			"[[@platform:project:api:design]]",
			"[[@platform:project:api:design#Intro|the design]]",
		} {
			if !strings.Contains(read.Content, want) {
				t.Errorf("content lacks %q:\n%s", want, read.Content)
			}
		}
	})

	t.Run("the disk form carries no scope", func(t *testing.T) {
		disk := diskIn(t, m.platform, "standard:testing")
		if strings.Contains(disk, "@platform") {
			t.Errorf("a scope reached the vault, where Obsidian cannot resolve it:\n%s", disk)
		}
		if !strings.Contains(disk, "[[Projects/") {
			t.Errorf("the link is not stored as a path:\n%s", disk)
		}
	})

	t.Run("backlinks in content are scoped", func(t *testing.T) {
		var read NoteOutput
		m.call("note_read", NoteReadInput{Ref: "@platform:project:api:design"}, &read)

		if !strings.Contains(read.Content, "## Backlinks") ||
			!strings.Contains(read.Content, "[[@platform:standard:testing]]") {
			t.Errorf("backlinks are not scoped:\n%s", read.Content)
		}
	})

	t.Run("related is scoped, with or without the prefix", func(t *testing.T) {
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Other", Vault: "platform"}, nil)

		for _, related := range []string{"@platform:project:api:design", "project:api:design"} {
			var out NoteOutput
			m.call("note_update", NoteUpdateInput{
				Ref: "@platform:standard:other", AddRelated: []string{related},
			}, &out)
			if !slices.Equal(out.Related, []string{"@platform:project:api:design"}) {
				t.Errorf("add_related %q: related = %v", related, out.Related)
			}
		}

		// It can be taken away again by either spelling.
		var out NoteOutput
		m.call("note_update", NoteUpdateInput{
			Ref: "@platform:standard:other", RemoveRelated: []string{"project:api:design"},
		}, &out)
		if len(out.Related) != 0 {
			t.Errorf("related = %v after removal", out.Related)
		}
	})

	t.Run("replace anchors accept scoped links and report them scoped", func(t *testing.T) {
		// The caller copies an anchor out of what it read, scope and all.
		var out NoteReplaceOutput
		m.call("note_replace", NoteReplaceInput{
			Ref:  standard.Ref,
			From: "See ", To: " and [[@platform:project:api:design#Intro|the design]]",
			Content: "[[@platform:project:api:design]]",
		}, &out)
		if out.Ref != standard.Ref || strings.TrimSpace(out.Removed) != "[[@platform:project:api:design]]" {
			t.Errorf("replace = %+v", out)
		}
	})

	t.Run("history and versions", func(t *testing.T) {
		var vaultWide HistoryOutput
		m.call("history", HistoryInput{Vault: "platform"}, &vaultWide)
		var seen bool
		for _, c := range vaultWide.Commits {
			for _, n := range c.Notes {
				if n.Ref == "@platform:standard:testing" {
					seen = true
				}
				if !strings.HasPrefix(n.Ref, "@platform:") {
					t.Errorf("unscoped ref in the team's history: %q", n.Ref)
				}
			}
		}
		if !seen {
			t.Errorf("the team's history does not show the note: %+v", vaultWide)
		}

		var perNote HistoryOutput
		m.call("history", HistoryInput{Ref: "@platform:standard:testing"}, &perNote)
		if perNote.Ref != "@platform:standard:testing" || len(perNote.Commits) == 0 {
			t.Fatalf("history = %+v", perNote)
		}
		commit := perNote.Commits[len(perNote.Commits)-1].Commit

		var version NoteVersionOutput
		m.call("note_version", NoteVersionInput{Ref: "@platform:standard:testing", Commit: commit}, &version)
		if version.Ref != "@platform:standard:testing" ||
			!strings.Contains(version.Content, "[[@platform:project:api:design]]") {
			t.Errorf("version = %+v", version)
		}

		var diff NoteDiffOutput
		m.call("note_diff", NoteDiffInput{Ref: "@platform:standard:testing", From: commit}, &diff)
		if diff.Ref != "@platform:standard:testing" {
			t.Errorf("diff ref = %q", diff.Ref)
		}

		var restored NoteRestoreOutput
		m.call("note_restore", NoteRestoreInput{Ref: "@platform:standard:testing", Commit: commit}, &restored)
		if restored.Ref != "@platform:standard:testing" || restored.RestoredFrom == "" {
			t.Errorf("restore = %+v", restored)
		}
	})

	t.Run("search list and lint", func(t *testing.T) {
		var found SearchOutput
		m.call("search", SearchInput{Query: "API design", Vault: "platform"}, &found)
		if len(found.Hits) == 0 {
			t.Fatal("no hits")
		}
		for _, hit := range found.Hits {
			if !strings.HasPrefix(hit.Ref, "@platform:") {
				t.Errorf("hit ref %q is not scoped", hit.Ref)
			}
		}

		var listed ListOutput
		m.call("list", ListInput{Kind: "standards", Vault: "platform"}, &listed)
		for _, n := range listed.Notes {
			if !strings.HasPrefix(n.Ref, "@platform:") {
				t.Errorf("listed ref %q is not scoped", n.Ref)
			}
		}

		const broken = "---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [Bad Tag]\nauthor: agent\n---\n\n# Broken\n"
		if err := os.WriteFile(filepath.Join(m.platform.vault.Root(), "Standards", "broken.md"), []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		var linted LintOutput
		m.call("lint", LintInput{Vault: "platform"}, &linted)
		var flagged bool
		for _, f := range linted.Findings {
			if !strings.HasPrefix(f.Ref, "@platform:") {
				t.Errorf("finding ref %q is not scoped", f.Ref)
			}
			if f.Ref == "@platform:standard:broken" {
				flagged = true
			}
		}
		if !flagged {
			t.Errorf("lint did not flag the planted note: %+v", linted.Findings)
		}
	})
}

// TestLinksBetweenVaultsAreRefused pins the rule that a note's links resolve
// inside its own vault, since that is the only place Obsidian can resolve them.
func TestLinksBetweenVaultsAreRefused(t *testing.T) {
	m := newMulti(t, multiOptions{})

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Vault: "platform"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Only Personal"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Other Team", Vault: "data"}, nil)

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Holder", Vault: "platform"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Mine"}, nil)

	tests := []struct {
		name string
		tool string
		args any
	}{
		{"team content to another team", "note_new",
			NoteNewInput{Kind: "standard", Title: "A", Vault: "platform", Content: "See [[@data:standard:other-team]]."}},
		{"team content to a personal note", "note_new",
			NoteNewInput{Kind: "standard", Title: "B", Vault: "platform", Content: "See [[standard:only-personal]]."}},
		{"personal content to a team note", "note_new",
			NoteNewInput{Kind: "standard", Title: "C", Content: "See [[@platform:standard:testing]]."}},
		{"team append to another team", "note_append",
			NoteAppendInput{Ref: "@platform:standard:holder", Content: "See [[@data:standard:other-team]]."}},
		{"personal append to a team note", "note_append",
			NoteAppendInput{Ref: "standard:mine", Content: "See [[@platform:standard:testing]]."}},
		{"team replace to a personal note", "note_replace",
			NoteReplaceInput{Ref: "@platform:standard:holder", Content: "See [[standard:only-personal]]."}},
		{"team related to another team", "note_update",
			NoteUpdateInput{Ref: "@platform:standard:holder", AddRelated: []string{"@data:standard:other-team"}}},
		{"team related to a personal note", "note_update",
			NoteUpdateInput{Ref: "@platform:standard:holder", AddRelated: []string{"standard:only-personal"}}},
		{"personal related to a team note", "note_update",
			NoteUpdateInput{Ref: "standard:mine", AddRelated: []string{"@platform:standard:testing"}}},
		{"session related", "session_start",
			SessionStartInput{Title: "D", Vault: "platform", Related: []string{"@data:standard:other-team"}}},
		{"topic related", "topic_new",
			TopicNewInput{Title: "E", When: "never", Vault: "data", Related: []string{"@platform:standard:testing"}}},
	}

	// Read the notes the replace cases aim at, so the refusal tested is the
	// link rule and not the read-before-write gate.
	m.call("note_read", NoteReadInput{Ref: "@platform:standard:holder"}, nil)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := m.callErr(tc.tool, tc.args)
			for _, want := range []string{"Links between vaults are not allowed", "Obsidian"} {
				if !strings.Contains(msg, want) {
					t.Errorf("message lacks %q: %s", want, msg)
				}
			}
		})
	}

	// Nothing was written by the refused calls.
	for _, ref := range []string{"standard:a", "standard:b"} {
		if m.platform.vault.Exists("Standards/" + strings.TrimPrefix(ref, "standard:") + ".md") {
			t.Errorf("%s was written despite the refusal", ref)
		}
	}
	if m.personal.vault.Exists("Standards/c.md") {
		t.Error("standard:c was written despite the refusal")
	}

	t.Run("the team's own prefix is stripped", func(t *testing.T) {
		var out NoteOutput
		m.call("note_new", NoteNewInput{
			Kind: "standard", Title: "Same Team", Vault: "platform",
			Content: "See [[@platform:standard:testing]] and [[standard:testing]].",
		}, &out)

		disk := diskIn(t, m.platform, "standard:same-team")
		if strings.Contains(disk, "@") && strings.Contains(disk, "[[@") {
			t.Errorf("a scope was stored:\n%s", disk)
		}
		if strings.Count(disk, "[[Standards/testing]]") != 2 {
			t.Errorf("both spellings should be stored as the same path link:\n%s", disk)
		}
	})

	t.Run("a link to nothing is still reported as missing", func(t *testing.T) {
		msg := m.callErr("note_new", NoteNewInput{
			Kind: "standard", Title: "F", Vault: "platform", Content: "See [[standard:nowhere]].",
		})
		if !strings.Contains(msg, "do not exist") || strings.Contains(msg, "between vaults") {
			t.Errorf("message = %s", msg)
		}
	})
}

// TestAccessIsCheckedOnEveryCall covers a team the caller never belonged to, a
// team they have left, and that the two cannot be told apart.
func TestAccessIsCheckedOnEveryCall(t *testing.T) {
	m := newMulti(t, multiOptions{search: true})

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Vault: "platform"}, nil)
	m.call("note_read", NoteReadInput{Ref: "@platform:standard:testing"}, nil)

	// Everything that touches a team, aimed at a team the caller cannot use.
	touches := func(slug string) map[string]func() string {
		scoped := "@" + slug + ":standard:testing"
		return map[string]func() string{
			"note_read":    func() string { return m.callErr("note_read", NoteReadInput{Ref: scoped}) },
			"note_append":  func() string { return m.callErr("note_append", NoteAppendInput{Ref: scoped, Content: "x"}) },
			"note_replace": func() string { return m.callErr("note_replace", NoteReplaceInput{Ref: scoped, Content: "x"}) },
			"note_update":  func() string { return m.callErr("note_update", NoteUpdateInput{Ref: scoped, AddTags: []string{"x"}}) },
			"note_delete":  func() string { return m.callErr("note_delete", NoteDeleteInput{Ref: scoped}) },
			"note_new":     func() string { return m.callErr("note_new", NoteNewInput{Kind: "standard", Title: "X", Vault: slug}) },
			"session":      func() string { return m.callErr("session_start", SessionStartInput{Title: "X", Vault: slug}) },
			"history":      func() string { return m.callErr("history", HistoryInput{Vault: slug}) },
			"search":       func() string { return m.callErr("search", SearchInput{Query: "x", Vault: slug}) },
			"list":         func() string { return m.callErr("list", ListInput{Kind: "standards", Vault: slug}) },
			"lint":         func() string { return m.callErr("lint", LintInput{Vault: slug}) },
			"correct": func() string {
				return m.callErr("correct", CorrectInput{Target: "@" + slug + ":contract", Guidance: "x"})
			},
			"git_remote": func() string { return m.callErr("git_remote", GitRemoteInput{Vault: slug}) },
			"category":   func() string { return m.callErr("category_list", CategoryListInput{Vault: slug}) },
		}
	}

	// An unknown team.
	unknown := map[string]string{}
	for name, call := range touches("ghost") {
		msg := call()
		unknown[name] = msg
		if want := "you are not a member of team ghost"; !strings.Contains(msg, want) {
			t.Errorf("%s: message = %q, want %q", name, msg, want)
		}
	}

	// A team the caller was in a moment ago.
	m.reach.revoke("platform")
	for name, call := range touches("platform") {
		msg := call()
		if want := "you are not a member of team platform"; !strings.Contains(msg, want) {
			t.Errorf("%s after removal: message = %q, want %q", name, msg, want)
		}

		// Same wording, so the caller learns nothing about which it was.
		if strings.ReplaceAll(msg, "platform", "X") != strings.ReplaceAll(unknown[name], "ghost", "X") {
			t.Errorf("%s: a removed team and an unknown team answer differently:\n  %q\n  %q", name, msg, unknown[name])
		}
	}

	t.Run("the tools that default to every vault skip it", func(t *testing.T) {
		var found SearchOutput
		m.call("search", SearchInput{Query: "testing"}, &found)
		for _, hit := range found.Hits {
			if strings.HasPrefix(hit.Ref, "@platform:") {
				t.Errorf("search returned %q from a team the caller has left", hit.Ref)
			}
		}

		var listed ListOutput
		m.call("list", ListInput{Kind: "standards"}, &listed)
		for _, n := range listed.Notes {
			if strings.HasPrefix(n.Ref, "@platform:") {
				t.Errorf("list returned %q from a team the caller has left", n.Ref)
			}
		}

		var booted BootOutput
		m.call("boot", BootInput{}, &booted)
		for _, team := range booted.Teams {
			if team.Slug == "platform" {
				t.Error("boot still offers a team the caller has left")
			}
		}
	})

	t.Run("the other team is unaffected", func(t *testing.T) {
		var out NoteOutput
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Fine", Vault: "data"}, &out)
		if out.Ref != "@data:standard:fine" {
			t.Errorf("ref = %q", out.Ref)
		}
	})
}

// TestReadingIsScopedToTheVault pins that having read a note in one vault says
// nothing about a note of the same name in another.
func TestReadingIsScopedToTheVault(t *testing.T) {
	m := newMulti(t, multiOptions{})

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Shared", Content: "personal body"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Shared", Content: "team body", Vault: "platform"}, nil)

	t.Run("reading the personal note does not unlock the team's", func(t *testing.T) {
		h := m.another()
		h.call("note_read", NoteReadInput{Ref: "standard:shared"}, nil)

		msg := h.callErr("note_replace", NoteReplaceInput{Ref: "@platform:standard:shared", Content: "x"})
		if !strings.Contains(msg, "read @platform:standard:shared with note_read") {
			t.Errorf("message = %s", msg)
		}

		h.call("note_read", NoteReadInput{Ref: "@platform:standard:shared"}, nil)
		h.call("note_replace", NoteReplaceInput{Ref: "@platform:standard:shared", Content: "rewritten"}, nil)
		if !strings.Contains(diskIn(t, m.platform, "standard:shared"), "rewritten") {
			t.Error("the team note was not replaced once it had been read")
		}
	})

	t.Run("reading the team note does not unlock the personal one", func(t *testing.T) {
		h := m.another()
		h.call("note_read", NoteReadInput{Ref: "@platform:standard:shared"}, nil)

		msg := h.callErr("note_replace", NoteReplaceInput{Ref: "standard:shared", Content: "x"})
		if !strings.Contains(msg, "read standard:shared with note_read") {
			t.Errorf("message = %s", msg)
		}
	})

	t.Run("a version read in one vault does not unlock a restore in another", func(t *testing.T) {
		h := m.another()

		var perNote HistoryOutput
		h.call("history", HistoryInput{Ref: "standard:shared"}, &perNote)
		personalCommit := perNote.Commits[0].Commit
		h.call("note_version", NoteVersionInput{Ref: "standard:shared", Commit: personalCommit}, nil)

		var team HistoryOutput
		h.call("history", HistoryInput{Ref: "@platform:standard:shared"}, &team)
		msg := h.callErr("note_restore", NoteRestoreInput{Ref: "@platform:standard:shared", Commit: team.Commits[0].Commit})
		if !strings.Contains(msg, "with note_version before restoring") {
			t.Errorf("message = %s", msg)
		}
	})
}

// TestSearchListAndLintCoverEveryVault covers the default of looking in every
// vault the session can reach, and narrowing it with the vault parameter.
func TestSearchListAndLintCoverEveryVault(t *testing.T) {
	m := newMulti(t, multiOptions{search: true})

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Cache Policy",
		Content: "cache invalidation stampede single-flight"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Cache Rules", Vault: "platform",
		Content: "cache invalidation stampede single-flight"}, nil)
	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Pipelines", Vault: "data",
		Content: "batch pipelines retry with backoff"}, nil)
	m.call("note_new", NoteNewInput{Kind: "project", Scope: "api", Facet: "design", Title: "API", Vault: "platform"}, nil)
	m.call("note_new", NoteNewInput{Kind: "project", Scope: "api", Facet: "design", Title: "API", Tags: []string{"api"}}, nil)

	t.Run("search merges by score with scoped refs", func(t *testing.T) {
		var out SearchOutput
		m.call("search", SearchInput{Query: "cache invalidation stampede", Limit: 20}, &out)

		refs := map[string]bool{}
		for i, hit := range out.Hits {
			refs[hit.Ref] = true
			if i > 0 && hit.Score > out.Hits[i-1].Score {
				t.Errorf("hits are not ordered by score: %v then %v", out.Hits[i-1].Score, hit.Score)
			}
		}
		for _, want := range []string{"standard:cache-policy", "@platform:standard:cache-rules"} {
			if !refs[want] {
				t.Errorf("no hit for %s in %v", want, refs)
			}
		}

		var names []string
		for _, v := range out.Vaults {
			names = append(names, v.Vault)
		}
		if !slices.Equal(names, []string{"personal", "platform", "data"}) {
			t.Errorf("vault breakdown = %v", names)
		}
	})

	t.Run("search is limited after merging", func(t *testing.T) {
		var out SearchOutput
		m.call("search", SearchInput{Query: "cache", Limit: 2}, &out)
		if len(out.Hits) != 2 {
			t.Errorf("hits = %d, want 2", len(out.Hits))
		}
	})

	t.Run("search restricted by vault", func(t *testing.T) {
		var out SearchOutput
		m.call("search", SearchInput{Query: "cache invalidation", Vault: "platform", Limit: 20}, &out)
		if len(out.Hits) == 0 {
			t.Fatal("no hits")
		}
		for _, hit := range out.Hits {
			if !strings.HasPrefix(hit.Ref, "@platform:") {
				t.Errorf("hit %q is from outside the vault asked for", hit.Ref)
			}
		}
		if len(out.Vaults) != 0 {
			t.Errorf("a single-vault search reports a breakdown: %+v", out.Vaults)
		}

		m.call("search", SearchInput{Query: "cache invalidation", Vault: "personal", Limit: 20}, &out)
		if len(out.Hits) == 0 {
			t.Fatal("no personal hits")
		}
		for _, hit := range out.Hits {
			if strings.HasPrefix(hit.Ref, "@") {
				t.Errorf("hit %q is from outside the personal vault", hit.Ref)
			}
		}
	})

	t.Run("list covers every vault, each with its own limit", func(t *testing.T) {
		var out ListOutput
		m.call("list", ListInput{Kind: "standards"}, &out)

		refs := map[string]bool{}
		for _, n := range out.Notes {
			refs[n.Ref] = true
		}
		for _, want := range []string{"standard:cache-policy", "@platform:standard:cache-rules", "@data:standard:pipelines"} {
			if !refs[want] {
				t.Errorf("list lacks %s", want)
			}
		}

		var one ListOutput
		m.call("list", ListInput{Kind: "standards", Vault: "data"}, &one)
		if one.Total != 1 || len(one.Notes) != 1 || one.Notes[0].Ref != "@data:standard:pipelines" {
			t.Errorf("list restricted to data = %+v", one)
		}

		var limited ListOutput
		m.call("list", ListInput{Kind: "standards", Limit: 1}, &limited)
		if len(limited.Notes) != 3 || !limited.Truncated {
			t.Errorf("a limit of 1 should keep one note from each of three vaults: %d notes, truncated %v",
				len(limited.Notes), limited.Truncated)
		}
	})

	t.Run("list of projects names the vault", func(t *testing.T) {
		var out ListOutput
		m.call("list", ListInput{Kind: "projects"}, &out)

		got := map[string][]string{}
		for _, p := range out.Projects {
			got[p.Vault] = p.Notes
		}
		if !slices.Equal(got["platform"], []string{"@platform:project:api:design"}) ||
			!slices.Equal(got[""], []string{"project:api:design"}) {
			t.Errorf("projects = %+v", out.Projects)
		}
	})

	t.Run("list of topics is scoped and shows only what a team holds", func(t *testing.T) {
		var out ListOutput
		m.call("list", ListInput{Kind: "topics", Vault: "data"}, &out)
		if len(out.Topics) != 0 {
			t.Errorf("an empty team vault lists topics: %+v", out.Topics)
		}

		m.call("list", ListInput{Kind: "topics", Vault: "platform"}, &out)
		if len(out.Topics) == 0 {
			t.Fatal("a seeded team vault lists no topics")
		}
		for _, topic := range out.Topics {
			if !strings.HasPrefix(topic.Ref, "@platform:") {
				t.Errorf("topic ref %q is not scoped", topic.Ref)
			}
		}
	})

	t.Run("list of an unknown kind is an error", func(t *testing.T) {
		if msg := m.callErr("list", ListInput{Kind: "diaries"}); !strings.Contains(msg, "unknown kind") {
			t.Errorf("message = %s", msg)
		}
	})

	t.Run("lint covers every vault", func(t *testing.T) {
		const broken = "---\ntype: standard\ncreated: 2026-08-17\nupdated: 2026-08-17\ntags: [Bad Tag]\nauthor: agent\n---\n\n# Broken\n"
		if err := os.WriteFile(filepath.Join(m.data.vault.Root(), "Standards", "broken.md"), []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}

		var all LintOutput
		m.call("lint", LintInput{}, &all)
		var flagged bool
		for _, f := range all.Findings {
			flagged = flagged || f.Ref == "@data:standard:broken"
		}
		if !flagged {
			t.Errorf("lint across vaults missed the planted note: %+v", all.Findings)
		}

		var personal LintOutput
		m.call("lint", LintInput{Vault: "personal"}, &personal)
		if !personal.Clean {
			t.Errorf("the personal vault is not clean: %+v", personal.Findings)
		}

		var one LintOutput
		m.call("lint", LintInput{Ref: "@data:standard:broken"}, &one)
		if one.Clean || one.Findings[0].Ref != "@data:standard:broken" {
			t.Errorf("lint of one scoped note = %+v", one)
		}
	})
}

func TestBootListsTheTeamsTheSessionCanReach(t *testing.T) {
	m := newMulti(t, multiOptions{})

	m.call("topic_new", TopicNewInput{Title: "Code Review", When: "Reviewing a change", Vault: "platform"}, nil)
	var teamSession SessionStartOutput
	m.call("session_start", SessionStartInput{Title: "Team Work", Vault: "platform"}, &teamSession)
	var ownSession SessionStartOutput
	m.call("session_start", SessionStartInput{Title: "My Work"}, &ownSession)

	var out BootOutput
	m.call("boot", BootInput{}, &out)

	if len(out.Teams) != 2 {
		t.Fatalf("teams = %+v", out.Teams)
	}
	platform, data := out.Teams[0], out.Teams[1]

	if platform.Slug != "platform" || platform.Name != "Platform Engineering" {
		t.Errorf("platform = %s / %s", platform.Slug, platform.Name)
	}
	if platform.Operating == nil || !strings.HasPrefix(platform.Operating.Content, "# Operating Contract") {
		t.Errorf("the team's operating document is missing: %+v", platform.Operating)
	}
	if platform.Operating != nil && strings.Contains(platform.Operating.Content, "## Backlinks") {
		t.Error("the operating document carries its backlinks block")
	}

	topics := map[string]bool{}
	for _, topic := range platform.Topics {
		topics[topic.Ref] = true
		if !strings.HasPrefix(topic.Ref, "@platform:") {
			t.Errorf("topic ref %q is not scoped", topic.Ref)
		}
	}
	for _, want := range []string{"@platform:topic:code-review", "@platform:standard:language-go"} {
		if !topics[want] {
			t.Errorf("the team's topic table lacks %s: %v", want, topics)
		}
	}

	standards := map[string]string{}
	for _, s := range platform.Standards {
		standards[s.Ref] = s.Title
	}
	if standards["@platform:standard:language-go"] != "Go" {
		t.Errorf("standards = %v", standards)
	}

	if data.Slug != "data" || data.Operating != nil || len(data.Standards) != 0 || len(data.Topics) != 0 {
		t.Errorf("an empty team vault should have nothing to show: %+v", data)
	}

	// Open sessions: the team's, scoped, beside the caller's own.
	open := map[string]bool{}
	for _, s := range out.OpenToday {
		open[s.Ref] = true
	}
	if !open[teamSession.Ref] || !open[ownSession.Ref] || !strings.HasPrefix(teamSession.Ref, "@platform:session:") {
		t.Errorf("open sessions = %v (team %q, own %q)", open, teamSession.Ref, ownSession.Ref)
	}

	// The contract is still the personal one.
	if out.Vault != m.personal.vault.Root() || len(out.Contract) == 0 {
		t.Errorf("boot no longer describes the personal vault: %q", out.Vault)
	}

	var convention bool
	for _, c := range out.Conventions {
		if strings.Contains(c, "@<team>:") {
			convention = true
		}
	}
	if !convention || !strings.Contains(strings.Join(out.Conventions, "\n"), "Links between vaults are refused") ||
		!strings.Contains(strings.Join(out.Conventions, "\n"), "govern work on that team's projects") {
		t.Errorf("boot does not explain team refs: %v", out.Conventions)
	}

	if platform.Search.State != IndexUnavailable {
		t.Errorf("search state = %q without an embedder", platform.Search.State)
	}
}

func TestBootReportsEachTeamsIndex(t *testing.T) {
	m := newMulti(t, multiOptions{search: true})

	var out BootOutput
	m.call("boot", BootInput{}, &out)

	if len(out.Teams) != 2 {
		t.Fatalf("teams = %+v", out.Teams)
	}
	for _, team := range out.Teams {
		if team.Search.State == IndexUnavailable || team.Search.State == "" {
			t.Errorf("%s: search state = %q with an embedder", team.Slug, team.Search.State)
		}
	}
	if out.Search.State == IndexUnavailable {
		t.Errorf("personal search state = %q", out.Search.State)
	}
}

func TestCorrectionsGoWhereTheirTargetPoints(t *testing.T) {
	var asked []string
	m := newMulti(t, multiOptions{configure: func(s *Server) {
		s.CorrectPolicy = func(_ context.Context, scope string) error {
			asked = append(asked, scope)
			if scope == "data" {
				return errors.New("only team admins may do that")
			}
			return nil
		}
	}})

	m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Vault: "platform"}, nil)

	t.Run("a team contract", func(t *testing.T) {
		var out CorrectOutput
		m.call("correct", CorrectInput{Target: "@platform:contract", Guidance: "Keep pull requests small.", Reason: "review was slow"}, &out)

		if out.Ref != "@platform:topic:operating" || out.History != "@platform:topic:corrections" {
			t.Errorf("out = %+v", out)
		}
		if !strings.Contains(diskIn(t, m.platform, "topic:operating"), "Keep pull requests small.") {
			t.Error("the team's operating document was not changed")
		}
		if strings.Contains(diskIn(t, m.personal, "topic:operating"), "Keep pull requests small.") {
			t.Error("the correction leaked into the personal contract")
		}
	})

	t.Run("a team standard", func(t *testing.T) {
		var out CorrectOutput
		m.call("correct", CorrectInput{Target: "@platform:standard:testing", Guidance: "Name tests for behaviour."}, &out)
		if out.Ref != "@platform:standard:testing" {
			t.Errorf("ref = %q", out.Ref)
		}
		if !strings.Contains(diskIn(t, m.platform, "standard:testing"), "Name tests for behaviour.") {
			t.Error("the team's standard was not changed")
		}
	})

	t.Run("no target is the personal contract, and asks no policy", func(t *testing.T) {
		before := len(asked)

		var out CorrectOutput
		m.call("correct", CorrectInput{Guidance: "Prefer boring technology."}, &out)

		if out.Ref != "topic:operating" || !strings.Contains(diskIn(t, m.personal, "topic:operating"), "Prefer boring technology.") {
			t.Errorf("out = %+v", out)
		}
		if len(asked) != before {
			t.Errorf("the policy was consulted for the personal vault: %v", asked[before:])
		}
		if strings.Contains(diskIn(t, m.platform, "topic:operating"), "Prefer boring technology.") {
			t.Error("a personal correction reached the team")
		}
	})

	t.Run("the policy can refuse a team", func(t *testing.T) {
		msg := m.callErr("correct", CorrectInput{Target: "@data:contract", Guidance: "Nope."})
		if !strings.Contains(msg, "only team admins may do that") {
			t.Errorf("message = %s", msg)
		}
		if !slices.Contains(asked, "data") {
			t.Errorf("the policy was not asked about the team: %v", asked)
		}
	})
}

func TestToolsCanBeFilteredByName(t *testing.T) {
	m := newMulti(t, multiOptions{allow: func(name string) bool {
		return name == "boot" || name == "note_read" || name == "search"
	}})

	res, err := m.client.ListTools(m.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"boot", "note_read", "search"}) {
		t.Errorf("tools = %v", got)
	}

	// A tool that is not offered cannot be called.
	if call, err := m.client.CallTool(m.context, callParams("note_delete", map[string]any{"ref": "standard:x", "reason": "r"})); err == nil && !call.IsError {
		t.Error("a filtered tool was callable")
	}

	if all := m.srv.ToolNames(); !slices.Contains(all, "note_delete") || !slices.Contains(all, "git_remote") {
		t.Errorf("ToolNames = %v", all)
	}
}

func TestMCPOffersEveryToolByDefault(t *testing.T) {
	m := newMulti(t, multiOptions{})

	res, err := m.client.ListTools(m.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != len(m.srv.ToolNames()) {
		t.Errorf("%d tools offered, %d registered", len(res.Tools), len(m.srv.ToolNames()))
	}
}

func TestGitRemotePolicyAndMechanism(t *testing.T) {
	t.Run("a policy decides per vault", func(t *testing.T) {
		var asked []string
		m := newMulti(t, multiOptions{configure: func(s *Server) {
			s.GitRemotePolicy = func(_ context.Context, scope string) error {
				asked = append(asked, scope)
				if scope == "platform" {
					return errors.New("only a team admin may change a team's remote")
				}
				return nil
			}
		}})

		msg := m.callErr("git_remote", GitRemoteInput{Vault: "platform"})
		if !strings.Contains(msg, "git_remote is not permitted") || !strings.Contains(msg, "only a team admin") {
			t.Errorf("message = %s", msg)
		}

		var out GitRemoteOutput
		m.call("git_remote", GitRemoteInput{}, &out)
		m.call("git_remote", GitRemoteInput{Vault: "data"}, &out)

		if !slices.Equal(asked, []string{"platform", "", "data"}) {
			t.Errorf("the policy saw scopes %q", asked)
		}
	})

	t.Run("no policy allows the personal vault only", func(t *testing.T) {
		m := newMulti(t, multiOptions{})
		var out GitRemoteOutput
		m.call("git_remote", GitRemoteInput{}, &out)
		if out.Note == "" {
			t.Errorf("out = %+v", out)
		}
		if msg := m.callErr("git_remote", GitRemoteInput{Vault: "platform"}); !strings.Contains(msg, "not permitted") {
			t.Errorf("message = %s", msg)
		}
	})

	t.Run("the mechanism can be replaced", func(t *testing.T) {
		type call struct {
			scope, url string
			core       *Core
		}
		var calls []call

		m := newMulti(t, multiOptions{configure: func(s *Server) {
			s.GitRemotePolicy = permitAll
			s.SetRemote = func(_ context.Context, c *Core, scope, url string) (GitRemoteOutput, error) {
				calls = append(calls, call{scope: scope, url: url, core: c})
				return GitRemoteOutput{Remote: "deploy-key", URL: url}, nil
			}
		}})

		var out GitRemoteOutput
		m.call("git_remote", GitRemoteInput{URL: "ssh://example.invalid/team.git", Vault: "platform"}, &out)

		if out.Remote != "deploy-key" || len(calls) != 1 || calls[0].scope != "platform" ||
			calls[0].url != "ssh://example.invalid/team.git" || calls[0].core != m.platform {
			t.Errorf("out = %+v, calls = %+v", out, calls)
		}
	})
}

func TestCommitsAreAttributedToTheSessionAuthor(t *testing.T) {
	lastAuthor := func(t *testing.T, c *Core) string {
		t.Helper()
		out, err := exec.Command("git", "-C", c.vault.Root(), "log", "-1", "--format=%an <%ae>").Output()
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	t.Run("with an author", func(t *testing.T) {
		m := newMulti(t, multiOptions{author: &git.Author{Name: "u_abc", Email: "u_abc@users.gandalf.invalid"}})

		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Mine"}, nil)
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Theirs", Vault: "platform"}, nil)

		const want = "u_abc <u_abc@users.gandalf.invalid>"
		for name, c := range map[string]*Core{"personal": m.personal, "platform": m.platform} {
			if got := lastAuthor(t, c); got != want {
				t.Errorf("%s: author = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("without one the repository's identity stays", func(t *testing.T) {
		m := newMulti(t, multiOptions{})
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Mine"}, nil)

		if got := lastAuthor(t, m.personal); got != "Gandalf <gandalf@localhost>" {
			t.Errorf("author = %q", got)
		}
	})

	t.Run("a new session keeps the author", func(t *testing.T) {
		m := newMulti(t, multiOptions{author: &git.Author{Name: "u_xyz", Email: "u_xyz@users.gandalf.invalid"}})
		h := m.another()
		h.call("note_new", NoteNewInput{Kind: "standard", Title: "Mine"}, nil)

		if got := lastAuthor(t, m.personal); got != "u_xyz <u_xyz@users.gandalf.invalid>" {
			t.Errorf("author = %q", got)
		}
	})
}

// TestSingleVaultServerHasNoTeams pins the other side of the routing: the
// stdio and single-token HTTP shapes know no teams, say so plainly, and keep
// working for everything that names none.
func TestSingleVaultServerHasNoTeams(t *testing.T) {
	h := newHarness(t)

	calls := map[string]func() string{
		"scoped ref": func() string { return h.callErr("note_read", NoteReadInput{Ref: "@platform:standard:x"}) },
		"vault param": func() string {
			return h.callErr("note_new", NoteNewInput{Kind: "standard", Title: "X", Vault: "platform"})
		},
		"search vault":  func() string { return h.callErr("list", ListInput{Kind: "standards", Vault: "platform"}) },
		"correct":       func() string { return h.callErr("correct", CorrectInput{Target: "@platform:contract", Guidance: "x"}) },
		"remote":        func() string { return h.callErr("git_remote", GitRemoteInput{Vault: "platform"}) },
		"malformed ref": func() string { return h.callErr("note_read", NoteReadInput{Ref: "@platform"}) },
	}
	for name, call := range calls {
		msg := call()
		want := "this server has no team vaults"
		if name == "malformed ref" {
			want = "a team ref is written @<team>:<ref>"
		}
		if !strings.Contains(msg, want) {
			t.Errorf("%s: message = %q, want %q", name, msg, want)
		}
	}

	var out BootOutput
	h.call("boot", BootInput{}, &out)
	if len(out.Teams) != 0 {
		t.Errorf("a single-vault boot lists teams: %+v", out.Teams)
	}
	if !slices.Equal(out.Conventions, conventions) {
		t.Errorf("a single-vault boot changed its conventions: %v", out.Conventions)
	}

	// Naming the personal vault is allowed, and is what the default means.
	var listed ListOutput
	h.call("list", ListInput{Kind: "standards", Vault: "personal"}, &listed)
	if len(listed.Notes) == 0 {
		t.Error("nothing listed from the personal vault")
	}
}

// TestEveryRefFreeToolTakesAVault guards the schema: a tool that can be aimed
// at a vault must say so, in a parameter that is described.
func TestEveryRefFreeToolTakesAVault(t *testing.T) {
	h := newHarness(t)

	res, err := h.client.ListTools(h.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := map[string]bool{
		"note_new": true, "session_start": true, "topic_new": true,
		"category_list": true, "category_create": true, "category_retire": true, "category_delete": true,
		"history": true, "git_remote": true, "search": true, "list": true, "lint": true,
	}
	for _, tool := range res.Tools {
		properties, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		property, has := properties["vault"].(map[string]any)

		if want[tool.Name] {
			delete(want, tool.Name)
			if !has {
				t.Errorf("%s takes no vault parameter", tool.Name)
			} else if description, _ := property["description"].(string); strings.TrimSpace(description) == "" {
				t.Errorf("%s.vault has no description", tool.Name)
			}
		} else if has {
			t.Errorf("%s takes a vault parameter but addresses a note by ref", tool.Name)
		}
	}
	for name := range want {
		t.Errorf("%s is not registered", name)
	}
}
