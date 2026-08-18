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

// BootInput is empty: boot takes no arguments and is safe to call again.
type BootInput struct{}

// Document is one instruction document returned in full.
type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`

	// Source is "vault" when the text came from the vault, "shipped" when the
	// vault had no copy and the built-in default was used instead.
	Source string `json:"source"`
}

// TopicSummary is one entry in the dispatch table.
type TopicSummary struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
	When  string `json:"when"`
}

// OpenSession is a session note still marked in progress.
type OpenSession struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// BootOutput is the session's starting context.
type BootOutput struct {
	Vault      string         `json:"vault"`
	Version    int            `json:"gandalfos_version"`
	Contract   []Document     `json:"contract"`
	Topics     []TopicSummary `json:"topics"`
	OpenToday  []OpenSession  `json:"open_sessions_today"`
	Notices    []string       `json:"notices,omitempty"`
	Addressing string         `json:"addressing"`
}

// boot returns the operating contract, the topics available on demand, and
// enough vault state to resume work already in progress.
//
// The contract is read from the vault rather than from the binary, because the
// vault is where corrections live. A missing document falls back to the
// shipped text with a notice, which is more useful than a session with no
// contract at all.
func (s *Server) boot(ctx context.Context, _ *sdk.CallToolRequest, _ BootInput) (*sdk.CallToolResult, BootOutput, error) {
	out := BootOutput{
		Vault:   s.vault.Root(),
		Version: instructions.Version,
		Addressing: "Notes are addressed by ref, never by path: session:<date-slug>, " +
			"project:<name>:<design|decisions|todo>, standard:<name>, topic:<name>, glossary. " +
			"Refs come from these tools; do not construct file paths.",
	}

	for _, doc := range instructions.Core() {
		content, source, err := s.document(doc)
		if err != nil {
			return nil, BootOutput{}, err
		}
		if source == "shipped" {
			out.Notices = append(out.Notices,
				fmt.Sprintf("%s is missing from the vault; using the shipped default. Run `gandalf init` to write it.", doc.Path))
		}
		out.Contract = append(out.Contract, Document{
			ID:      doc.ID,
			Title:   doc.Title,
			Content: content,
			Source:  source,
		})
	}

	for _, doc := range instructions.Topics() {
		out.Topics = append(out.Topics, TopicSummary{
			Ref:   s.canonical(doc.Path).String(),
			Title: doc.Title,
			When:  doc.When,
		})
	}

	open, err := s.openSessions(schema.Today())
	if err != nil {
		return nil, BootOutput{}, err
	}
	out.OpenToday = open

	if len(open) > 0 {
		out.Notices = append(out.Notices,
			"A session note is already open today. Continue it rather than starting another, unless this is a different unit of work.")
	}

	return nil, out, nil
}

// TopicInput selects one topic.
type TopicInput struct {
	Ref string `json:"ref" jsonschema:"the topic ref from gandalf_boot, such as topic:shipping"`
}

// TopicOutput is a topic's full text.
type TopicOutput struct {
	Document
}

// topic returns one instruction document on demand.
func (s *Server) topic(ctx context.Context, _ *sdk.CallToolRequest, in TopicInput) (*sdk.CallToolResult, TopicOutput, error) {
	// Boot hands out whichever ref is canonical for each document, so both
	// kinds are accepted here. A bare id is too: the model has just read a
	// table of them, and refusing "shipping" for want of a prefix would be
	// pedantry rather than a safeguard.
	id := strings.TrimSpace(in.Ref)
	for _, prefix := range []vault.Kind{vault.KindTopic, vault.KindStandard} {
		id = strings.TrimPrefix(id, string(prefix)+":")
	}

	doc, ok := instructions.Lookup(id)
	if !ok {
		return nil, TopicOutput{}, fmt.Errorf("no topic %q; call gandalf_boot for the list", in.Ref)
	}

	content, source, err := s.document(doc)
	if err != nil {
		return nil, TopicOutput{}, err
	}

	return nil, TopicOutput{Document{
		ID:      doc.ID,
		Title:   doc.Title,
		Content: content,
		Source:  source,
	}}, nil
}

// document returns a shipped document's text, preferring the vault's copy.
func (s *Server) document(doc instructions.Doc) (content, source string, err error) {
	if s.vault.Exists(doc.Path) {
		note, err := s.vault.Read(doc.Path)
		if err != nil {
			return "", "", fmt.Errorf("read %q: %w", doc.Path, err)
		}
		return note.Body, "vault", nil
	}

	body, err := doc.Body()
	if err != nil {
		return "", "", err
	}
	return body, "shipped", nil
}

// openSessions returns session notes dated today that are still in progress.
//
// This is what lets a model that lost its ref — to compaction, or a restarted
// server — pick the session note back up instead of starting a second one.
func (s *Server) openSessions(on schema.Date) ([]OpenSession, error) {
	paths, err := s.vault.List()
	if err != nil {
		return nil, err
	}

	layout := s.vault.Layout()
	prefix := on.String()

	var open []OpenSession
	for _, p := range paths {
		ref := layout.RefFor(p)
		if ref.Kind != vault.KindSession || !strings.HasPrefix(ref.Name, prefix) {
			continue
		}

		note, err := s.vault.Read(p)
		if err != nil {
			// A malformed note is lint's problem, not boot's.
			continue
		}
		if note.FM.Status != schema.StatusInProgress {
			continue
		}

		open = append(open, OpenSession{Ref: ref.String(), Title: note.Title()})
	}

	return open, nil
}
