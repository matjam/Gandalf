package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/topic"
	"github.com/matjam/gandalf/internal/vault"
)

// An operating topic is a document the model reads on demand when the work
// touches its subject. Gandalf ships a set of them, and a vault may declare
// more. The two are handled identically past this point: both are addressed as
// topic:<id>, both are listed at boot, and both are the vault's to edit.

// topicEntry is one operating topic, whichever way it came to exist.
type topicEntry struct {
	ID    string
	Path  string
	Title string
	When  string
}

// lookupTopic returns the topic with the given id, shipped or vault-declared.
//
// Shipped documents are consulted first, and by any delivery: the correction
// history is unlisted but still a topic a caller may name. A vault-declared
// topic cannot shadow a shipped one, because topic_new refuses the id.
func (s *Server) lookupTopic(id string) (topicEntry, bool) {
	if doc, ok := instructions.Lookup(id); ok {
		return topicEntry{ID: doc.ID, Path: doc.Path, Title: doc.Title, When: doc.When}, true
	}
	if t, ok := s.vault.Topics().Lookup(id); ok {
		return topicEntry{ID: t.ID, Path: t.Path, Title: t.Title, When: t.When}, true
	}
	return topicEntry{}, false
}

// topicSummaries is the table boot and list hand out: the shipped topics in
// their manifest order, then the vault's own in declaration order.
func (s *Server) topicSummaries() []TopicSummary {
	var out []TopicSummary

	for _, doc := range instructions.Topics() {
		out = append(out, TopicSummary{
			Ref:   s.canonical(doc.Path).String(),
			Title: doc.Title,
			When:  doc.When,
		})
	}

	for _, t := range s.vault.Topics().Topics {
		out = append(out, TopicSummary{
			Ref:   vault.Ref{Kind: KindTopic, Name: t.ID}.String(),
			Title: t.Title,
			When:  t.When,
		})
	}

	return out
}

// TopicNewInput declares a new operating topic.
type TopicNewInput struct {
	Title string `json:"title" jsonschema:"the topic's heading; its id and filename derive from it"`

	When string `json:"when" jsonschema:"the work that should send the model to this topic, in one line; shown in boot's topic table"`

	Content string   `json:"content,omitempty" jsonschema:"body prose, in markdown; the title heading is added when the body does not open with one"`
	Tags    []string `json:"tags,omitempty" jsonschema:"lowercase hyphenated tags"`
	Related []string `json:"related,omitempty" jsonschema:"refs of related notes"`
	Reason  string   `json:"reason" jsonschema:"why this topic is being declared, in a few words; it becomes the commit message"`
}

// TopicNewOutput is the declared topic and the note that holds it.
type TopicNewOutput struct {
	Topic TopicSummary `json:"topic"`
	Note  NoteOutput   `json:"note"`
}

// topicNew declares a topic and writes the note that holds it.
//
// The note is filed beside the shipped topics rather than under a category,
// which is what makes it a topic rather than a standard: a standard is
// guidance for writing code in one language or layer, a topic is a procedure
// that applies to a kind of work. Both are readable and correctable the same
// way once they exist.
func (s *Server) topicNew(ctx context.Context, _ *sdk.CallToolRequest, in TopicNewInput) (*sdk.CallToolResult, TopicNewOutput, error) {
	unlock := s.beginWrite()
	defer unlock()

	if err := checkReason(in.Reason); err != nil {
		return nil, TopicNewOutput{}, err
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, TopicNewOutput{}, fmt.Errorf("a topic needs a title")
	}
	if strings.TrimSpace(in.When) == "" {
		return nil, TopicNewOutput{}, fmt.Errorf(
			"a topic needs a when: one line saying which work should send the model to it, as the boot table shows for shipping and diagnostics")
	}

	id := category.Slugify(title)
	if id == "" {
		return nil, TopicNewOutput{}, fmt.Errorf("title %q produces an empty id", in.Title)
	}
	if _, shipped := instructions.Lookup(id); shipped {
		return nil, TopicNewOutput{}, fmt.Errorf(
			"topic:%s is a document Gandalf ships; edit that one rather than declaring another", id)
	}
	if _, declared := s.vault.Topics().Lookup(id); declared {
		return nil, TopicNewOutput{}, fmt.Errorf(
			"topic:%s already exists; append to it with note_append instead of declaring it again", id)
	}

	entry := topic.Topic{
		ID:    id,
		Path:  topic.Folder + "/" + topicFile(title),
		Title: title,
		When:  strings.TrimSpace(in.When),
	}
	if s.vault.Exists(entry.Path) {
		return nil, TopicNewOutput{}, fmt.Errorf(
			"a note already exists where topic:%s would be filed; it is addressed as %s", id, s.canonical(entry.Path))
	}

	// Every topic points back at the contract, as the shipped ones do, so the
	// contract's backlinks list the whole set. It is only added when the
	// contract is actually there to point at.
	related := append([]string{}, in.Related...)
	if s.vault.Exists(instructions.Contract().Path) {
		related = add(related, []string{KindTopic + ":" + instructions.Contract().ID})
	}

	resolved := s.resolveLinks(related, in.Content)
	if err := resolved.err(); err != nil {
		return nil, TopicNewOutput{}, err
	}

	note, err := s.vault.NewNote(vault.NewNoteRequest{
		Type:    schema.NoteType(topicType),
		Title:   title,
		Path:    entry.Path,
		Tags:    add([]string{"gandalf", id}, in.Tags),
		Related: resolved.Related,
		Author:  schema.AuthorAgent,
		Status:  schema.StatusComplete,
		Body:    resolved.Body,
		On:      schema.Today(),
	})
	if err != nil {
		return nil, TopicNewOutput{}, err
	}

	set := s.vault.Topics()
	if err := set.Add(entry); err != nil {
		return nil, TopicNewOutput{}, err
	}

	if err := s.write(note); err != nil {
		set.Remove(entry.ID)
		return nil, TopicNewOutput{}, err
	}
	if err := s.vault.SetTopics(set); err != nil {
		return nil, TopicNewOutput{}, err
	}

	ref := s.canonical(note.Path)
	s.record("gandalf: topic new "+ref.String(), in.Reason)

	return nil, TopicNewOutput{
		Topic: TopicSummary{Ref: ref.String(), Title: entry.Title, When: entry.When},
		Note:  s.describe(ref, note),
	}, nil
}

// topicType is the category a topic's note is filed under. The shipped topics
// are standards as far as the schema is concerned, and a vault-declared one
// is no different.
const topicType = "standard"

// topicFile derives a topic's filename from its title, matching the shipped
// convention of "External Content.md" rather than a slug. Only characters
// that cannot appear in a filename are dropped.
func topicFile(title string) string {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '\x00':
			return -1
		}
		return r
	}, title)
	clean = strings.Join(strings.Fields(clean), " ")
	return clean + ".md"
}
