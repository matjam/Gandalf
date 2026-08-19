package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
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

// Addressing is how one kind of note is named and whether it may be rewritten.
//
// It is generated from the vault's own categories rather than described in a
// seeded document, because a document describing the vault's shape goes stale
// the moment the shape changes and a seeded one cannot be corrected by a
// release. The same reasoning keeps the tool list out of the instructions
// entirely: the client already shows the model what each tool does.
type Addressing struct {
	RefForm string `json:"ref_form"`
	Holds   string `json:"holds,omitempty"`

	// Mutability says whether these notes may be rewritten in place or only
	// added to. Knowing before trying is the point: the alternative is
	// learning it from a refusal.
	Mutability string `json:"mutability"`

	// FacetMutability names the facets that answer differently from their
	// category, such as a project's decisions log among its other notes.
	FacetMutability map[string]string `json:"facet_mutability,omitempty"`

	Retired bool `json:"retired,omitempty"`
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

	// Categories is how this vault's notes are addressed, generated from what
	// the vault declares.
	Categories map[string]Addressing `json:"categories"`

	// Conventions are the rules the tool descriptions cannot carry: what a ref
	// is, what every writing tool requires, what is read-only. Everything a
	// single tool can explain about itself is left to that tool.
	Conventions []string `json:"conventions"`

	// Search says whether the search index is usable yet. Boot is where a
	// session decides how it will find prior work, and that decision is wrong
	// if it assumes a capability that is still warming up or absent.
	Search IndexStatus `json:"search"`
}

// boot returns the operating contract, the topics available on demand, and
// enough vault state to resume work already in progress.
//
// The contract is read from the vault rather than from the binary, because the
// vault is where corrections live. A missing document falls back to the
// shipped text with a notice, which is more useful than a session with no
// contract at all.
func (s *Server) boot(ctx context.Context, _ *sdk.CallToolRequest, _ BootInput) (*sdk.CallToolResult, BootOutput, error) {
	// Boot is the first call of a session, which makes it the right moment to
	// start indexing: the work happens while the model is reading the contract
	// rather than when it first tries to search.
	s.startIndexing(ctx)

	out := BootOutput{
		Vault:   s.vault.Root(),
		Version: instructions.Version,
		Search:  s.indexStatus(),
		Addressing: "Notes are addressed by ref — what a note is, not where it lives — never by " +
			"path. The ref forms this vault uses are below, generated from the categories it " +
			"declares. Refs come from these tools; do not construct one from a filename.",
		Categories:  s.addressing(),
		Conventions: conventions,
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

// conventions are the standing rules about the tool surface as a whole.
//
// They live here rather than in a seeded document for the reason that document
// kept getting wrong: a release can change these and cannot change the vault's
// copy of anything. Anything one tool can say about itself is left to that
// tool's own description, which the client already shows.
var conventions = []string{
	"A note outside the vault's filing conventions is addressed as path:<...>, and can be read but not written.",
	"session:latest resolves to the most recent session note. Ask for it when you mean it; nothing defaults to it.",
	"Every tool that changes the vault requires a reason. It becomes the commit message, so say why rather than what: what changed is recorded already.",
	"Write links as refs, such as [[standard:language-go]]. A link to a note that does not exist is refused rather than creating one.",
	"Each note ends with a maintained Backlinks block. Read it to see what depends on a note; never write into it.",
	"Every change is committed, and past versions of a note can be read, compared, and restored. Nothing here rewrites history.",
}

// addressing reports how each of the vault's categories is named and whether
// its notes may be rewritten.
func (s *Server) addressing() map[string]Addressing {
	out := map[string]Addressing{}

	for _, c := range s.vault.Categories().Categories {
		view := view(c, 0)
		out[c.Name] = Addressing{
			RefForm:         view.RefForm,
			Holds:           view.Description,
			Mutability:      view.Mutability,
			FacetMutability: view.FacetMutability,
			Retired:         view.Retired,
		}
	}

	// Topics are Gandalf's own documents rather than a category the vault
	// declares, so nothing above accounts for them, and a model that cannot
	// address them cannot read the operating topics boot just listed.
	out[KindTopic] = Addressing{
		RefForm:    KindTopic + ":<name>",
		Holds:      "operating topics Gandalf ships; the vault's copy is what is served",
		Mutability: string(category.Replaceable),
	}

	return out
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

	prefix := on.String()

	var open []OpenSession
	for _, p := range paths {
		ref := s.vault.RefFor(p)
		if ref.Kind != sessionCategory || !strings.HasPrefix(ref.Name, prefix) {
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
