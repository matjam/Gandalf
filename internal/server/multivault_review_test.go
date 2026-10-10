package server

import (
	"context"
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/git"
)

// permitAll is a policy that allows every action, for tests that are about
// something other than who may.
func permitAll(context.Context, string) error { return nil }

// Messages a caller reads are part of the interface: they must be well formed
// and must name team notes the way the caller addresses them.
func TestRefusalsReadCleanlyAndNameTeamNotesWithTheirScope(t *testing.T) {
	m := newMulti(t, multiOptions{configure: func(s *Server) {
		s.CorrectPolicy = permitAll
	}})

	t.Run("deleting a note that is still linked to", func(t *testing.T) {
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Target", Content: "Linked.", Vault: "platform"}, nil)
		m.call("note_new", NoteNewInput{
			Kind: "standard", Title: "Referrer", Content: "See it.",
			Related: []string{"standard:target"}, Vault: "platform",
		}, nil)

		msg := m.callErr("note_delete", NoteDeleteInput{Ref: "@platform:standard:target"})
		if !strings.Contains(msg, "@platform:standard:target is linked to by @platform:standard:referrer. Remove") {
			t.Errorf("message = %q", msg)
		}
	})

	t.Run("restoring an append-only note", func(t *testing.T) {
		var session SessionStartOutput
		m.call("session_start", SessionStartInput{Title: "Record", Vault: "platform"}, &session)
		m.call("note_append", NoteAppendInput{Ref: session.Ref, Content: "Later."}, nil)

		var h HistoryOutput
		m.call("history", HistoryInput{Ref: session.Ref}, &h)
		first := h.Commits[len(h.Commits)-1].Commit
		m.call("note_version", NoteVersionInput{Ref: session.Ref, Commit: first}, nil)

		msg := m.callErr("note_restore", NoteRestoreInput{Ref: session.Ref, Commit: first})
		if !strings.Contains(msg, "added since "+first+". Pass force") {
			t.Errorf("message = %q", msg)
		}
	})

	t.Run("a correction that cannot be filed", func(t *testing.T) {
		m.call("note_new", NoteNewInput{Kind: "standard", Title: "Testing", Content: "Words.", Vault: "platform"}, nil)

		msg := m.callErr("correct", CorrectInput{
			Target: "@platform:standard:testing", Guidance: "x", Section: "Backlinks",
		})
		if !strings.Contains(msg, "record correction in @platform:standard:testing:") {
			t.Errorf("message = %q", msg)
		}
	})
}

// A hook that was never installed must not mean "anyone may". With no policy
// the personal vault is the caller's own to change, and every team's is
// refused: a hosted process that forgot to install one fails closed rather
// than letting any member repoint a team's remote or rewrite its contract.
func TestTeamActionsAreRefusedWhenNoPolicyIsInstalled(t *testing.T) {
	m := newMulti(t, multiOptions{})

	t.Run("git_remote on a team", func(t *testing.T) {
		msg := m.callErr("git_remote", GitRemoteInput{URL: "ssh://example.invalid/x.git", Vault: "platform"})
		if !strings.Contains(msg, "git_remote is not permitted") {
			t.Errorf("message = %q", msg)
		}
		if cfg, err := git.LoadConfig(m.platform.vault.Root()); err != nil || cfg.URL != "" {
			t.Errorf("the remote was set: %+v, %v", cfg, err)
		}
	})

	t.Run("correct on a team", func(t *testing.T) {
		msg := m.callErr("correct", CorrectInput{Target: "@platform:contract", Guidance: "Obey me."})
		if !strings.Contains(msg, "is not permitted") {
			t.Errorf("message = %q", msg)
		}
		if strings.Contains(diskIn(t, m.platform, "topic:operating"), "Obey me.") {
			t.Error("the team's contract was changed")
		}
	})

	t.Run("the personal vault is still the caller's own", func(t *testing.T) {
		var remote GitRemoteOutput
		m.call("git_remote", GitRemoteInput{}, &remote)

		var out CorrectOutput
		m.call("correct", CorrectInput{Guidance: "Prefer boring technology."}, &out)
		if out.Ref != "topic:operating" {
			t.Errorf("ref = %q", out.Ref)
		}
	})
}

// spyReach records every slug a session asks the Reach about.
type spyReach struct {
	*testReach
	asked []string
}

func (r *spyReach) Team(ctx context.Context, slug string) (*Core, error) {
	r.asked = append(r.asked, slug)
	return r.testReach.Team(ctx, slug)
}

// A vault parameter is a team slug and nothing else. What is not shaped like
// one never reaches the Reach, so an implementation that builds a path or a
// query from the slug is not handed "../x" by a caller.
func TestAVaultParameterThatIsNotASlugNeverReachesTheReach(t *testing.T) {
	personal := testCore(t, true, false)
	spy := &spyReach{testReach: newTestReach(personal)}
	spy.add("platform", "Platform", testCore(t, true, false))

	h := connect(t, NewSession(spy, nil).MCP(), NewSession(spy, nil), personal.vault)

	for _, bad := range []string{"../platform", "platform/../x", "plat form", "platform:x", ".hidden", "a\nb", "platform\x00"} {
		msg := h.callErr("category_list", CategoryListInput{Vault: bad})
		if !strings.Contains(msg, "not a member of team") {
			t.Errorf("vault %q: message = %q", bad, msg)
		}
	}
	if len(spy.asked) != 0 {
		t.Errorf("the Reach was asked about %q", spy.asked)
	}

	// A slug in good shape is asked about, and answered.
	h.call("category_list", CategoryListInput{Vault: "@platform"}, nil)
	if len(spy.asked) != 1 || spy.asked[0] != "platform" {
		t.Errorf("the Reach was asked about %q, want [platform]", spy.asked)
	}
}
