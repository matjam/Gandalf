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
	"os"
	"strings"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/embed"
	"github.com/matjam/gandalf/internal/git"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/vault"
)

// KindTopic addresses a document Gandalf ships rather than a note the vault
// filed. It is reserved: a category may not take the name.
const KindTopic = "topic"

// Server exposes one vault.
type Server struct {
	vault *vault.Vault

	// git maintains the vault as a repository when set. Mutations commit
	// through it; a nil value means the vault is not under git for this
	// process.
	git *git.Repo

	// embedder backs search. Nil means search is unavailable and every other
	// tool works as usual.
	embedder embed.Embedder
	searcher searcher

	// read records which notes have been read during this connection, so a
	// replacement cannot be aimed at text the caller has not seen. It is
	// deliberately not persisted: a restarted process has forgotten what the
	// note said, and so has whatever is driving it.
	mu   sync.Mutex
	read map[string]bool

	// name and version identify this build to the client.
	name    string
	version string
}

// markRead records that a note's current text has been seen.
func (s *Server) markRead(ref vault.Ref) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.read == nil {
		s.read = map[string]bool{}
	}
	s.read[ref.String()] = true
}

// hasRead reports whether a note has been read during this connection.
func (s *Server) hasRead(ref vault.Ref) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.read[ref.String()]
}

// New returns a server over the given vault, without search.
func New(v *vault.Vault, version string) *Server {
	return &Server{vault: v, name: "gandalf", version: version}
}

// WithSearch returns a server that can also search, using the given embedder.
func WithSearch(v *vault.Vault, version string, embedder embed.Embedder) *Server {
	s := New(v, version)
	s.embedder = embedder
	return s
}

// WithGit returns a server that commits every vault mutation and can sync a
// configured remote. The repo is also what StartSync on the repo itself uses.
func (s *Server) WithGit(repo *git.Repo) *Server {
	s.git = repo
	return s
}

// record commits the vault after a successful mutation. A commit failure is
// logged and never returned: the note already landed, and a missing commit is
// recoverable on the next change.
func (s *Server) record(message string) {
	if s.git == nil {
		return
	}
	if err := s.git.Commit(message); err != nil {
		fmt.Fprintf(os.Stderr, "gandalf: git commit: %v\n", err)
	}
}

// Close releases anything the server opened.
func (s *Server) Close() error { return s.closeIndex() }

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
		Name: "gandalf_search",
		Description: "Find notes by meaning, not just wording. Use this before starting work " +
			"to see what the vault already says about a topic. Returns refs, so a hit can be " +
			"read straight away.",
	}, s.search)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_list",
		Description: "List what the vault holds, without content: sessions, projects, " +
			"standards, topics, meetings, interviews, or all. Use it to find prior work " +
			"before starting, and to discover which projects exist.",
	}, s.list)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_read",
		Description: "Read a note or an operating topic by ref. Refs come from gandalf_boot, " +
			"gandalf_search, gandalf_list, or whichever tool created the note; they are never " +
			"constructed from a file path. Read the matching topic before proposing or " +
			"changing work it covers.",
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
		Description: "Append content to a note, optionally under a new heading. Adding to " +
			"a note can never lose what it already says, so prefer it whenever the new " +
			"content sits alongside the old rather than replacing it.",
	}, s.noteAppend)

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_note_replace",
		Description: "Rewrite part of a note that describes current state — a design note, " +
			"a backlog, a standard. Name a section to rewrite just that section, or give " +
			"from and to anchors to rewrite the span between them; with neither, the whole " +
			"body is replaced. Returns the text it removed, so check that against what you " +
			"meant to remove. Refused by default on notes that are a chronological record, " +
			"such as sessions and decision logs: append to those instead, or pass force to " +
			"repair a defect in one. Read the note first.",
	}, s.noteReplace)

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
		Name: "gandalf_note_delete",
		Description: "Delete a note. Refused when other notes link to it, listing them so " +
			"the links can be removed first.",
	}, s.noteDelete)

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

	sdk.AddTool(srv, &sdk.Tool{
		Name: "gandalf_git_remote",
		Description: "Configure the vault's git remote URL so Gandalf can push and pull. " +
			"Gandalf commits every change automatically; use this when the user wants the " +
			"vault mirrored to a remote, or pass an empty url to stop syncing. Conflicts " +
			"resolve as remote-wins on pull.",
	}, s.gitRemote)

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
		// A bare shipped-document id is accepted: the model has just read a
		// table of them, and refusing "shipping" for want of a prefix would be
		// pedantry rather than a safeguard.
		if doc, found := instructions.Lookup(strings.TrimSpace(raw)); found {
			return s.canonical(doc.Path), doc.Path, nil
		}

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
	return CanonicalRef(s.vault, notePath)
}

// CanonicalRef returns the one ref addressing a note.
//
// It is exported because anything that records refs — the search index built
// from the command line, as much as the server — has to agree with the tools
// about a note's name. Two namers produced two answers for the shipped
// documents, and the index kept whichever ran last.
func CanonicalRef(v *vault.Vault, notePath string) vault.Ref {
	if ref := v.RefFor(notePath); ref.Kind != vault.KindPath {
		return ref
	}

	for _, doc := range instructions.Docs() {
		if doc.Path == notePath {
			return vault.Ref{Kind: KindTopic, Name: doc.ID}
		}
	}

	return v.RefFor(notePath)
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
