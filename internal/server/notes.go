package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// sessionCategory is the category gandalf_session_start writes to. A vault may
// rename or retire it, in which case session notes simply are not a thing that
// vault has.
const sessionCategory = "session"

// NoteReadInput selects a note.
type NoteReadInput struct {
	Ref string `json:"ref" jsonschema:"the note's ref, as returned by another tool"`
}

// NoteOutput is a note's metadata and content.
type NoteOutput struct {
	Ref     string   `json:"ref"`
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Created string   `json:"created"`
	Updated string   `json:"updated"`
	Tags    []string `json:"tags"`
	Related []string `json:"related"`
	Status  string   `json:"status,omitempty"`
	Content string   `json:"content"`

	// Source is set to "shipped" when the text came from the binary because
	// the vault has no copy, and is otherwise omitted.
	Source string `json:"source,omitempty"`
}

// noteRead returns one note, whether it is something the vault holds or one of
// the documents Gandalf ships.
//
// There is one read verb rather than two. A separate tool for shipped
// documents differed only in falling back to the built-in text when the vault
// lacked a copy, which is rare enough not to justify a second way of asking
// the same question — and it was misnamed, since it served standards as
// readily as topics.
func (s *Server) noteRead(ctx context.Context, _ *sdk.CallToolRequest, in NoteReadInput) (*sdk.CallToolResult, NoteOutput, error) {
	ref, path, err := s.resolve(in.Ref)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	if !s.vault.Exists(path) {
		// A shipped document missing from the vault is served from the binary
		// rather than refused: a session with no contract is worse than one
		// reading the default.
		if doc, ok := shippedAt(path); ok {
			body, docErr := doc.Body()
			if docErr != nil {
				return nil, NoteOutput{}, docErr
			}
			s.markRead(ref)
			return nil, NoteOutput{
				Ref:     ref.String(),
				Title:   doc.Title,
				Type:    string(doc.Type),
				Content: s.toRefs(body),
				Source:  "shipped",
			}, nil
		}
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	return nil, s.describe(ref, note), nil
}

// shippedAt returns the shipped document filed at a vault path.
func shippedAt(path string) (instructions.Doc, bool) {
	for _, doc := range instructions.Docs() {
		if doc.Path == path {
			return doc, true
		}
	}
	return instructions.Doc{}, false
}

// SessionStartInput describes the session note to create.
type SessionStartInput struct {
	Title   string   `json:"title" jsonschema:"what this unit of work is about"`
	Tags    []string `json:"tags,omitempty" jsonschema:"lowercase hyphenated tags"`
	Related []string `json:"related,omitempty" jsonschema:"refs of related notes"`
}

// SessionStartOutput is the created session note.
type SessionStartOutput struct {
	Ref     string `json:"ref"`
	Created bool   `json:"created"`
	Note    string `json:"note,omitempty"`
}

// sessionStart creates the session note for this unit of work.
//
// A note already at that path is returned rather than replaced: two units of
// work that slugify the same on one day are far likelier than a deliberate
// request to start over, and overwriting would destroy the record.
func (s *Server) sessionStart(ctx context.Context, _ *sdk.CallToolRequest, in SessionStartInput) (*sdk.CallToolResult, SessionStartOutput, error) {
	resolved := s.resolveLinks(in.Related, "")
	if err := resolved.err(); err != nil {
		return nil, SessionStartOutput{}, err
	}

	note, err := s.vault.NewNote(vault.NewNoteRequest{
		Type:    schema.NoteType(sessionCategory),
		Title:   in.Title,
		Tags:    in.Tags,
		Related: resolved.Related,
		Author:  schema.AuthorBoth,
		Status:  schema.StatusInProgress,
		On:      schema.Today(),
	})
	if err != nil {
		return nil, SessionStartOutput{}, err
	}

	ref := s.canonical(note.Path)
	if s.vault.Exists(note.Path) {
		return nil, SessionStartOutput{
			Ref:  ref.String(),
			Note: "a session note already exists for this title today; continuing it",
		}, nil
	}

	if err := s.write(note); err != nil {
		return nil, SessionStartOutput{}, err
	}

	return nil, SessionStartOutput{Ref: ref.String(), Created: true}, nil
}

// NoteNewInput describes a note to create.
type NoteNewInput struct {
	Kind  string `json:"kind" jsonschema:"the category to file this note under; see gandalf_category_list"`
	Title string `json:"title"`

	Scope string `json:"scope,omitempty" jsonschema:"required for scoped categories, such as the project name"`
	Facet string `json:"facet,omitempty" jsonschema:"required for scoped categories, such as design or decisions"`

	Content string   `json:"content,omitempty" jsonschema:"body prose; the category's template is used when omitted"`
	Tags    []string `json:"tags,omitempty" jsonschema:"lowercase hyphenated tags"`
	Related []string `json:"related,omitempty" jsonschema:"refs of related notes"`
	Status  string   `json:"status,omitempty" jsonschema:"in-progress, complete, or superseded"`
}

// noteNew creates a note, filing it by its category.
//
// Sessions are excluded deliberately: gandalf_session_start creates those,
// setting the authorship and status the memory protocol expects.
func (s *Server) noteNew(ctx context.Context, _ *sdk.CallToolRequest, in NoteNewInput) (*sdk.CallToolResult, NoteOutput, error) {
	kind := strings.TrimSpace(in.Kind)

	cat, ok := s.vault.Categories().Lookup(kind)
	switch {
	case !ok:
		return nil, NoteOutput{}, fmt.Errorf("unknown category %q (want one of %s)",
			in.Kind, strings.Join(s.vault.Categories().CreatableNames(), ", "))
	case !cat.Creatable():
		return nil, NoteOutput{}, fmt.Errorf("category %q cannot be created through the tools", in.Kind)
	case cat.Name == sessionCategory:
		return nil, NoteOutput{}, fmt.Errorf("use gandalf_session_start to open a session note")
	}

	resolved := s.resolveLinks(in.Related, in.Content)
	if err := resolved.err(); err != nil {
		return nil, NoteOutput{}, err
	}

	note, err := s.vault.NewNote(vault.NewNoteRequest{
		Type:    schema.NoteType(cat.Name),
		Title:   in.Title,
		Scope:   in.Scope,
		Name:    in.Facet,
		Tags:    in.Tags,
		Related: resolved.Related,
		Author:  schema.AuthorAgent,
		Status:  schema.Status(in.Status),
		Body:    resolved.Body,
		On:      schema.Today(),
	})
	if err != nil {
		return nil, NoteOutput{}, err
	}

	if s.vault.Exists(note.Path) {
		ref := s.canonical(note.Path)
		return nil, NoteOutput{}, fmt.Errorf(
			"%s already exists; append to it with gandalf_note_append instead of replacing it", ref)
	}

	if err := s.write(note); err != nil {
		return nil, NoteOutput{}, err
	}

	return nil, s.describe(s.canonical(note.Path), note), nil
}

// NoteAppendInput describes content to add to a note.
type NoteAppendInput struct {
	Ref     string `json:"ref"`
	Content string `json:"content"`
	Heading string `json:"heading,omitempty" jsonschema:"optional heading to add the content under"`
}

// noteAppend adds to a note's body.
func (s *Server) noteAppend(ctx context.Context, _ *sdk.CallToolRequest, in NoteAppendInput) (*sdk.CallToolResult, NoteOutput, error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, NoteOutput{}, fmt.Errorf("nothing to append")
	}

	ref, path, err := s.writable(in.Ref)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	resolved := s.resolveLinks(nil, in.Content)
	if err := resolved.err(); err != nil {
		return nil, NoteOutput{}, err
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	note.Append(in.Heading, resolved.Body)
	note.Touch(schema.Today())

	if err := s.write(note); err != nil {
		return nil, NoteOutput{}, err
	}

	return nil, s.describe(ref, note), nil
}

// write saves a note and updates the backlinks of whatever it now links to,
// or has stopped linking to.
//
// Only those notes are touched. Writing a note never changes that note's own
// block — a link it holds is an inbound link somewhere else — so nothing needs
// re-reading afterwards either.
func (s *Server) write(note *vault.Note) error {
	var before []string
	if s.vault.Exists(note.Path) {
		if previous, err := s.vault.Read(note.Path); err == nil {
			before = previous.OutgoingLinks()
		}
	}

	if err := s.vault.Write(note); err != nil {
		return err
	}

	_, err := s.vault.ApplyBacklinks(note.Path, before, note.OutgoingLinks())
	return err
}

// NoteUpdateInput describes metadata changes.
type NoteUpdateInput struct {
	Ref string `json:"ref"`

	// AddTags and RemoveTags are additive and subtractive so that a caller
	// need not restate the whole list, which is how tags get lost.
	AddTags    []string `json:"add_tags,omitempty"`
	RemoveTags []string `json:"remove_tags,omitempty"`
	AddRelated []string `json:"add_related,omitempty" jsonschema:"refs to link to this note"`
	Status     string   `json:"status,omitempty" jsonschema:"in-progress, complete, or superseded"`
}

// noteUpdate changes a note's metadata, never its body.
func (s *Server) noteUpdate(ctx context.Context, _ *sdk.CallToolRequest, in NoteUpdateInput) (*sdk.CallToolResult, NoteOutput, error) {
	ref, path, err := s.writable(in.Ref)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	note, err := s.vault.Read(path)
	if err != nil {
		return nil, NoteOutput{}, err
	}

	if in.Status != "" {
		status := schema.Status(in.Status)
		if !status.Valid() {
			return nil, NoteOutput{}, fmt.Errorf("unknown status %q", in.Status)
		}
		note.FM.Status = status
	}

	resolved := s.resolveLinks(in.AddRelated, "")
	if err := resolved.err(); err != nil {
		return nil, NoteOutput{}, err
	}

	note.FM.Tags = add(remove(note.FM.Tags, in.RemoveTags), in.AddTags)
	note.FM.Related = add(note.FM.Related, resolved.Related)
	note.Touch(schema.Today())

	if issues := note.FM.Validate(); schema.HasErrors(issues) {
		return nil, NoteOutput{}, fmt.Errorf("update would leave %s invalid: %s", ref, issues[0].Message)
	}

	if err := s.write(note); err != nil {
		return nil, NoteOutput{}, err
	}

	return nil, s.describe(ref, note), nil
}

// describe renders a note for a tool result, translating the links stored on
// disk back into refs.
//
// This is the one place a note's full text leaves the server, so it is also
// where the note is recorded as seen. Replacement requires that: a caller
// holding the current text can check what a bounded edit removed, and one that
// has not read the note cannot.
func (s *Server) describe(ref vault.Ref, n *vault.Note) NoteOutput {
	s.markRead(ref)

	return NoteOutput{
		Ref:     ref.String(),
		Title:   n.Title(),
		Type:    string(n.FM.Type),
		Created: n.FM.Created.String(),
		Updated: n.FM.Updated.String(),
		Tags:    n.FM.Tags,
		Related: s.refsFor(n.FM.Related),
		Status:  string(n.FM.Status),
		Content: s.toRefs(n.Body),
	}
}

// add appends values not already present.
func add(existing, values []string) []string {
	seen := make(map[string]bool, len(existing))
	for _, v := range existing {
		seen[v] = true
	}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			existing = append(existing, v)
			seen[v] = true
		}
	}
	return existing
}

// remove drops the given values.
func remove(existing, values []string) []string {
	if len(values) == 0 {
		return existing
	}

	drop := make(map[string]bool, len(values))
	for _, v := range values {
		drop[v] = true
	}

	kept := existing[:0]
	for _, v := range existing {
		if !drop[v] {
			kept = append(kept, v)
		}
	}
	return kept
}
