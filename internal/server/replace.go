package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/schema"
)

// NoteReplaceInput describes a rewrite of part of a note's body.
//
// Section and anchor addressing are alternatives, and giving neither replaces
// the whole body. Frontmatter is not reachable from here at all: it is
// Gandalf's, and gandalf_note_update is how metadata changes.
type NoteReplaceInput struct {
	Ref     string `json:"ref"`
	Content string `json:"content" jsonschema:"the replacement text; empty removes the span"`

	Section        string `json:"section,omitempty" jsonschema:"the heading whose section to rewrite, such as Verification"`
	IncludeHeading bool   `json:"include_heading,omitempty" jsonschema:"also replace the heading line, which is how a section is removed entirely"`

	From           string `json:"from,omitempty" jsonschema:"literal text marking the start of the span; must appear exactly once"`
	To             string `json:"to,omitempty" jsonschema:"literal text marking the end of the span; must appear exactly once, after from"`
	IncludeAnchors bool   `json:"include_anchors,omitempty" jsonschema:"also replace the anchors themselves; by default they are kept"`
}

// NoteReplaceOutput is the note as it now stands, plus the text that went.
type NoteReplaceOutput struct {
	NoteOutput

	// Removed is what the replacement displaced. A bounded replacement does
	// not say what it is deleting, so this is the only way for a caller to
	// check that it deleted what it meant to.
	Removed string `json:"removed"`
}

// target is which part of a note a replacement addresses.
type target int

const (
	targetWhole target = iota
	targetSection
	targetAnchored
)

// noteReplace rewrites part of a note's body.
func (s *Server) noteReplace(ctx context.Context, _ *sdk.CallToolRequest, in NoteReplaceInput) (*sdk.CallToolResult, NoteReplaceOutput, error) {
	ref, path, err := s.writable(in.Ref)
	if err != nil {
		return nil, NoteReplaceOutput{}, err
	}

	where, err := replaceTarget(in)
	if err != nil {
		return nil, NoteReplaceOutput{}, err
	}

	if m := s.mutability(path); m != category.Replaceable {
		return nil, NoteReplaceOutput{}, fmt.Errorf(
			"%s is %s: it records what was known at the time rather than describing the current "+
				"state, so nothing already written there may be rewritten. Add to it with "+
				"gandalf_note_append instead", ref, m)
	}

	// Read before write, for the same reason the harness's own editing tools
	// require it: a caller that has not seen the current text cannot judge
	// whether a bounded edit removed the right thing.
	if !s.hasRead(ref) {
		return nil, NoteReplaceOutput{}, fmt.Errorf(
			"read %s with gandalf_note_read before replacing part of it", ref)
	}

	resolved := s.resolveLinks(nil, in.Content)
	if err := resolved.err(); err != nil {
		return nil, NoteReplaceOutput{}, err
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return nil, NoteReplaceOutput{}, err
	}

	var removed string
	switch where {
	case targetSection:
		removed, err = note.ReplaceSection(in.Section, resolved.Body, in.IncludeHeading)
	case targetAnchored:
		// Anchors are matched against the text on disk, where links are
		// paths, while a caller copying an anchor out of a tool result has
		// them as refs.
		removed, err = note.ReplaceBetween(s.toPaths(in.From), s.toPaths(in.To), resolved.Body, in.IncludeAnchors)
	default:
		removed, err = note.ReplaceContent(resolved.Body)
	}
	if err != nil {
		return nil, NoteReplaceOutput{}, fmt.Errorf("%s: %w", ref, err)
	}

	note.Touch(schema.Today())

	if err := s.write(note); err != nil {
		return nil, NoteReplaceOutput{}, err
	}

	s.record("gandalf: note replace " + ref.String())
	return nil, NoteReplaceOutput{
		NoteOutput: s.describe(ref, note),
		Removed:    s.toRefs(removed),
	}, nil
}

// replaceTarget works out which part of the note a request addresses, and
// refuses the requests that are ambiguous or that would empty a note.
func replaceTarget(in NoteReplaceInput) (target, error) {
	section := strings.TrimSpace(in.Section) != ""
	anchored := in.From != "" || in.To != ""

	switch {
	case section && anchored:
		return 0, fmt.Errorf("pass either a section or a from/to pair, not both")

	case section:
		return targetSection, nil

	case anchored:
		if in.From == "" || in.To == "" {
			return 0, fmt.Errorf(
				"anchored replacement needs both from and to; without a closing anchor the " +
					"replacement would run to the end of the note")
		}
		return targetAnchored, nil

	case strings.TrimSpace(in.Content) == "":
		return 0, fmt.Errorf(
			"replacing the whole body with nothing would leave an empty note; name a section, " +
				"give anchors, or remove the note with gandalf_note_delete")

	default:
		return targetWhole, nil
	}
}

// mutability reports whether the note at a vault path may be rewritten in
// place.
//
// The vault's own declaration is asked first, so a user who has changed what a
// category means gets their answer rather than Gandalf's. A document Gandalf
// ships is next: those are operating instructions describing current state,
// and the correction protocol depends on them being editable. Anything else —
// a note no category accounts for — is append-only, because losing a record
// is worse than having to append to one.
func (s *Server) mutability(notePath string) category.Mutability {
	if cat, _, name, ok := s.vault.Categories().Owner(notePath); ok {
		facet := ""
		if cat.Rule == category.RuleScoped {
			facet = name
		}
		return cat.MutabilityOf(facet)
	}

	if _, ok := shippedAt(notePath); ok {
		return category.Replaceable
	}

	return category.AppendOnly
}
