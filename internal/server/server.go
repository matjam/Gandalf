// Package server exposes the vault to a model over MCP.
//
// Tools address notes by ref — what a note is — never by path. A model given a
// path parameter will use it, and will then invent paths of its own; keeping
// paths out of the tool surface entirely is what makes the filing conventions
// hold. Every tool that returns a note reference returns a ref, so search,
// lint, and creation results can be fed straight back in.
package server

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/vault"
)

// KindTopic addresses a document Gandalf ships rather than a note the vault
// filed. It is reserved: a category may not take the name.
const KindTopic = "topic"

// Server exposes one vault.
type Server struct {
	vault *vault.Vault

	// name and version identify this build to the client.
	name    string
	version string
}

// New returns a server over the given vault.
func New(v *vault.Vault, version string) *Server {
	return &Server{vault: v, name: "gandalf", version: version}
}

// MCP builds the MCP server with every tool registered.
func (s *Server) MCP() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{
		Name:    s.name,
		Version: s.version,
	}, nil)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_boot",
		Description: "Start here, before any other work. Returns the operating contract, " +
			"the topics available on demand, and any session notes already open today. " +
			"Read what it returns; it is the working agreement for this session.",
	}, s.boot)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_topic",
		Description: "Fetch one topic or standard by id, as listed by gandalf_boot. " +
			"Read the matching topic before proposing or changing work it covers.",
	}, s.topic)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_list",
		Description: "List what the vault holds, without content: sessions, projects, " +
			"standards, topics, meetings, interviews, or all. Use it to find prior work " +
			"before starting, and to discover which projects exist.",
	}, s.list)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_read",
		Description: "Read a note by ref. Refs come from gandalf_boot, gandalf_lint, or " +
			"whichever tool created the note; they are never constructed from a file path.",
	}, s.noteRead)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_session_start",
		Description: "Create this session's note, before proposing or writing code. " +
			"One note per logical unit of work, not one per conversation. Returns the " +
			"ref to append to as the work proceeds.",
	}, s.sessionStart)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_new",
		Description: "Create a note. Gandalf decides where it is filed and writes its " +
			"metadata; supply the content. Returns the ref.",
	}, s.noteNew)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_append",
		Description: "Append content to a note, optionally under a new heading. This is " +
			"the only way to change a note's body, so nothing already recorded is lost.",
	}, s.noteAppend)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_update",
		Description: "Update a note's metadata — tags, related links, status. The updated " +
			"date is maintained automatically; the body is not touched.",
	}, s.noteUpdate)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_category_list",
		Description: "List the kinds of note this vault holds, how each is filed, and how " +
			"its notes are addressed. Check here before creating a note of an unfamiliar kind.",
	}, s.categoryList)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_category_create",
		Description: "Declare a new kind of note, when what the user is keeping does not fit " +
			"the kinds that exist. A design decision, not a routine one: ask first.",
	}, s.categoryCreate)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_category_retire",
		Description: "Stop new notes being filed under a category, leaving the ones already " +
			"there readable and writable.",
	}, s.categoryRetire)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_category_delete",
		Description: "Remove a category entirely. Only works when it holds no notes; retire " +
			"it instead if it does.",
	}, s.categoryDelete)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_lint",
		Description: "Validate note metadata and links, for one note or the whole vault. " +
			"Reports are addressed by ref.",
	}, s.lint)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_correct",
		Description: "Record a correction from the user in the vault, in the one file that " +
			"owns that kind of guidance. Call this in the same reply as applying the " +
			"correction, so it survives the session.",
	}, s.correct)

	return srv
}

// Run serves over stdio until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if err := s.MCP().Run(ctx, &sdk.StdioTransport{}); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// resolve turns a ref string into a ref and the note path it addresses.
//
// Topics are resolved here rather than in the vault package: their paths come
// from the shipped manifest, which the vault deliberately knows nothing about.
func (s *Server) resolve(raw string) (vault.Ref, string, error) {
	// Topics are addressed by a reserved kind rather than a category: they are
	// documents Gandalf ships, and their homes come from its manifest rather
	// than from the vault's filing rules.
	if id, ok := strings.CutPrefix(strings.TrimSpace(raw), KindTopic+":"); ok {
		doc, found := instructions.Lookup(id)
		if !found {
			return vault.Ref{}, "", fmt.Errorf("ref %q: no such topic", raw)
		}
		return s.canonical(doc.Path), doc.Path, nil
	}

	ref, err := s.vault.ParseRef(raw)
	if err != nil {
		// A path is the one wrong answer worth answering properly: the vault
		// can work out which note was meant, so say so rather than leaving the
		// caller to guess the scheme from a rejection.
		if suggestion, ok := s.suggest(raw); ok {
			return vault.Ref{}, "", fmt.Errorf("%w. That note is addressed as %s", err, suggestion)
		}
		return vault.Ref{}, "", err
	}

	path, err := s.vault.Resolve(ref)
	if err != nil {
		return vault.Ref{}, "", err
	}

	// What comes back is always the canonical ref, never the one that was
	// passed in. Aliases matter most here — a caller storing "session:latest"
	// from a result would later read whichever note was newest then — but the
	// same applies to a seeded standard reached by its topic id.
	return s.canonical(path), path, nil
}

// suggest returns the ref addressing a path a caller passed by mistake, when
// that path names a note the vault actually holds.
func (s *Server) suggest(raw string) (vault.Ref, bool) {
	candidate := strings.TrimSpace(raw)
	if !strings.HasSuffix(candidate, ".md") {
		candidate += ".md"
	}

	if !s.vault.Exists(candidate) {
		return vault.Ref{}, false
	}
	return s.canonical(candidate), true
}

// canonical returns the one ref that addresses a note.
//
// Shipped documents are the awkward case: a seeded standard lives where the
// layout can name it, so it is standard:<name>, while the operating topics
// live outside the layout's conventions and are reached as topic:<id>. Picking
// one per note matters — two names for the same note would have boot and lint
// disagreeing about what to call it.
func (s *Server) canonical(notePath string) vault.Ref {
	if ref := s.vault.RefFor(notePath); ref.Kind != vault.KindPath {
		return ref
	}

	for _, doc := range instructions.Docs() {
		if doc.Path == notePath {
			return vault.Ref{Kind: KindTopic, Name: doc.ID}
		}
	}

	return s.vault.RefFor(notePath)
}

// writable resolves a ref and refuses the read-only kinds.
func (s *Server) writable(raw string) (vault.Ref, string, error) {
	ref, path, err := s.resolve(raw)
	if err != nil {
		return vault.Ref{}, "", err
	}
	if !ref.Writable() {
		return vault.Ref{}, "", fmt.Errorf(
			"ref %q addresses a note outside the vault's filing conventions, which Gandalf will not write to", raw)
	}
	return ref, path, nil
}
