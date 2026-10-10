package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NoteDeleteInput names the note to remove.
type NoteDeleteInput struct {
	Ref    string `json:"ref" jsonschema:"the note's ref, as returned by another tool"`
	Reason string `json:"reason" jsonschema:"why this note is being removed, in a few words; it becomes the commit message"`
}

// NoteDeleteOutput reports what was removed, or why it was not.
type NoteDeleteOutput struct {
	Ref     string `json:"ref"`
	Deleted bool   `json:"deleted"`

	// Rebuilt lists notes whose backlinks changed as a result.
	Rebuilt []string `json:"backlinks_updated,omitempty"`

	GitNotice
}

// noteDelete removes a note, provided nothing points at it.
//
// A note that is still referenced cannot go: deleting it would turn every link
// to it into a dead one, scattered across notes nobody is looking at. The
// referrers come back in the error so the caller can fix them rather than
// hunting for them.
func (s *Server) noteDelete(ctx context.Context, _ *sdk.CallToolRequest, in NoteDeleteInput) (*sdk.CallToolResult, NoteDeleteOutput, error) {
	sc, rest, err := s.route(ctx, in.Ref)
	if err != nil {
		return nil, NoteDeleteOutput{}, err
	}

	unlock := sc.beginWrite()
	defer unlock()

	if err := checkReason(in.Reason); err != nil {
		return nil, NoteDeleteOutput{}, err
	}

	ref, path, err := sc.writable(rest)
	if err != nil {
		return nil, NoteDeleteOutput{}, err
	}
	if !sc.vault.Exists(path) {
		return nil, NoteDeleteOutput{}, fmt.Errorf("%s does not exist", sc.qualify(ref))
	}

	referrers, err := sc.vault.Referrers(path)
	if err != nil {
		return nil, NoteDeleteOutput{}, err
	}
	if len(referrers) > 0 {
		refs := make([]string, 0, len(referrers))
		for _, r := range referrers {
			refs = append(refs, sc.qualify(sc.canonical(r)))
		}
		return nil, NoteDeleteOutput{}, fmt.Errorf(
			"%s is linked to by %s. Remove those links first, or keep the note — deleting it would leave dead links behind",
			sc.qualify(ref), strings.Join(refs, ", "))
	}

	// Read the note before removing it: whatever it pointed at needs its
	// backlinks trimmed, and afterwards there is nothing left to ask.
	var outgoing []string
	if note, err := sc.vault.Read(path); err == nil {
		outgoing = note.OutgoingLinks()
	}

	if err := sc.vault.Delete(path); err != nil {
		return nil, NoteDeleteOutput{}, err
	}

	rebuilt, err := sc.vault.ApplyBacklinks(path, outgoing, nil)
	if err != nil {
		return nil, NoteDeleteOutput{}, err
	}

	// A vault-declared topic is named in the registry as well as on disk.
	// Leaving the entry behind would have boot advertise a topic that reads
	// as missing.
	if t, ok := sc.vault.Topics().ByPath(path); ok {
		set := sc.vault.Topics()
		set.Remove(t.ID)
		if err := sc.vault.SetTopics(set); err != nil {
			return nil, NoteDeleteOutput{}, err
		}
	}

	warning := sc.record("gandalf: note delete "+ref.String(), in.Reason)
	return nil, NoteDeleteOutput{
		Ref:       sc.qualify(ref),
		Deleted:   true,
		Rebuilt:   sc.refsOf(rebuilt),
		GitNotice: GitNotice{GitWarning: warning},
	}, nil
}

// refsOf renders note paths as refs.
func (c *scoped) refsOf(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, c.qualify(c.canonical(p)))
	}
	return out
}
