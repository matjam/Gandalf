package server

import (
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every tool that changes the vault holds the write lock from before it reads
// the note through to after the change is committed. Over stdio there is one
// session and it never matters; over HTTP a missed lock means one session's
// commit sweeping up another's half-written note, or a backlink rebuild losing
// an update.
//
// The lock is taken as the first statement of each handler, before any
// validation, so a call blocks whether or not its arguments would be accepted.
// That is what lets this test enumerate the tools without satisfying the
// preconditions of each: what is being checked is that the handler waits.

// blockedFor is how long a call is given to prove it is waiting. A handler
// that takes the lock waits indefinitely, so this bounds the test rather than
// the behaviour.
const blockedFor = 100 * time.Millisecond

// released is how long a call gets to finish once the lock is free.
const released = 10 * time.Second

func TestEveryChangeToTheVaultTakesTheWriteLock(t *testing.T) {
	h := newGitHarness(t)

	var note NoteOutput
	h.call("note_new", map[string]any{
		"kind": "project", "scope": "locking", "facet": "design",
		"title": "Locking — Design", "content": designBody, "reason": "set up the test",
	}, &note)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"session_start", map[string]any{"title": "Some work", "reason": "test"}},
		{"note_new", map[string]any{
			"kind": "project", "scope": "other", "facet": "design",
			"title": "Other — Design", "content": "Body.", "reason": "test",
		}},
		{"note_append", map[string]any{"ref": note.Ref, "content": "More.", "reason": "test"}},
		{"note_replace", map[string]any{
			"ref": note.Ref, "section": "Verification", "content": "New.", "reason": "test",
		}},
		{"note_update", map[string]any{"ref": note.Ref, "add_tags": []string{"locking"}, "reason": "test"}},
		{"note_delete", map[string]any{"ref": note.Ref, "reason": "test"}},
		{"note_restore", map[string]any{"ref": note.Ref, "commit": "HEAD", "reason": "test"}},
		{"category_create", map[string]any{
			"name": "incident", "plural": "incidents", "rule": "dated",
			"folder": "Incidents", "description": "Something broke and why.", "reason": "test",
		}},
		{"category_retire", map[string]any{"name": "meeting", "reason": "test"}},
		{"category_delete", map[string]any{"name": "meeting", "reason": "test"}},
		{"correct", map[string]any{
			"target": "contract", "guidance": "Say less.", "reason": "test",
		}},
		{"git_remote", map[string]any{"url": ""}},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			unlock := h.server.beginWrite()

			done := make(chan struct{})
			go func() {
				defer close(done)
				// The result is not inspected: whether the call succeeds or is
				// refused, it must not get that far while the lock is held.
				_, _ = h.client.CallTool(h.context, &sdk.CallToolParams{
					Name: c.tool, Arguments: c.args,
				})
			}()

			select {
			case <-done:
				unlock()
				t.Fatalf("%s finished while the write lock was held", c.tool)
			case <-time.After(blockedFor):
			}

			unlock()

			select {
			case <-done:
			case <-time.After(released):
				t.Fatalf("%s did not finish after the write lock was released", c.tool)
			}
		})
	}
}

// TestReadingToolsDoNotWaitOnTheWriteLock keeps the lock from being widened
// into a lock on the whole server. Reading is the common case, and one slow
// commit must not stop every other agent looking anything up.
func TestReadingToolsDoNotWaitOnTheWriteLock(t *testing.T) {
	h := newGitHarness(t)

	var note NoteOutput
	h.call("note_new", map[string]any{
		"kind": "project", "scope": "reading", "facet": "design",
		"title": "Reading — Design", "content": designBody, "reason": "set up the test",
	}, &note)

	unlock := h.server.beginWrite()
	defer unlock()

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"boot", map[string]any{}},
		{"note_read", map[string]any{"ref": note.Ref}},
		{"list", map[string]any{"kind": "projects"}},
		{"lint", map[string]any{}},
		{"category_list", map[string]any{}},
		{"history", map[string]any{}},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = h.client.CallTool(h.context, &sdk.CallToolParams{
					Name: c.tool, Arguments: c.args,
				})
			}()

			select {
			case <-done:
			case <-time.After(released):
				t.Fatalf("%s waited on the write lock", c.tool)
			}
		})
	}
}
