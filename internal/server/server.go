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

// Core is what one vault has exactly one of, however many sessions are
// connected to it: the notes themselves, the repository they are committed
// to, the search index over them, and the lock that serialises changes.
//
// Over stdio there is only ever one session, so the split makes no difference.
// Over HTTP a core is shared by every connected agent, and duplicating any of
// these would mean one SQLite handle, one background index pass, and one git
// sync loop per agent.
type Core struct {
	vault *vault.Vault

	// git maintains the vault as a repository when set. Mutations commit
	// through it; a nil value means the vault is not under git for this
	// process.
	git *git.Repo

	// embedder backs search. Nil means search is unavailable and every other
	// tool works as usual.
	embedder embed.Embedder
	searcher searcher

	// indexer runs the first index pass in the background, so a cold vault
	// does not turn the first search into a stall of unknown length.
	indexer *indexer

	// write serialises changes to the vault, held from before a note is read
	// to after the commit that records the change.
	//
	// Two properties depend on it. Committing runs git add -A, so an
	// overlapping change would be swept into another session's commit under
	// the wrong reason; and writing a note rebuilds the backlink blocks of the
	// notes it links to, so two changes touching one target can otherwise lose
	// an update. Both are read-modify-write over the whole vault, which is why
	// the lock is vault-wide rather than per note.
	write sync.Mutex

	// name and version identify this build to the client.
	name    string
	version string
}

// Server is one session: one stdio connection, or one HTTP client holding a
// session id.
//
// It embeds the Core of its personal vault, which is the only vault a
// single-vault process has and is where every tool that names no vault lands.
// Reach says which other vaults the session may use; for stdio and the
// single-token HTTP mode that is none, and the session behaves exactly as it
// did before vaults could be several.
type Server struct {
	*Core

	// reach is the set of vaults this session may use, asked on every call.
	reach Reach

	// author, when set, is who this session's commits are attributed to. Nil
	// keeps the repository's own identity.
	author *git.Author

	// GitRemotePolicy decides whether this session may change a vault's git
	// remote. Nil allows the personal vault, which is what a process serving
	// one vault to its owner wants, and refuses every team's; hosted mode sets
	// it to a role check.
	GitRemotePolicy ScopePolicy

	// CorrectPolicy decides whether this session may record a correction in a
	// team's vault. It is consulted only for team-scoped targets: a correction
	// aimed at the personal vault is the caller's own business. Nil refuses
	// every team, so a process that serves teams must install one.
	CorrectPolicy ScopePolicy

	// SetRemote is how git_remote changes a vault's remote. Nil uses the
	// built-in behaviour, which configures the remote on the vault's own
	// repository and tries one sync. It is a field so that hosted mode can
	// replace the mechanism (per-vault deploy keys, say) without touching the
	// tool: policy, routing and the reply shape stay here.
	SetRemote RemoteSetter

	// read records which notes have been read during this session, so a
	// replacement cannot be aimed at text the caller has not seen. It is
	// per-session rather than per-process because one agent having read a note
	// says nothing about what another has seen, and it is not persisted
	// because a restarted process has forgotten what the note said, and so has
	// whatever is driving it.
	//
	// Keys carry the vault a note is in, so a note in one vault never
	// satisfies the gate for a note of the same name in another.
	mu   sync.Mutex
	read map[string]bool
}

// markRead records that a note's current text has been seen. The key is the
// scope-qualified ref.
func (s *Server) markRead(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.read == nil {
		s.read = map[string]bool{}
	}
	s.read[key] = true
}

// hasRead reports whether a note has been read during this connection.
func (s *Server) hasRead(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.read[key]
}

// versionKey names one version of one note, which is keyed to the commit
// rather than to the note: restoring is gated on having looked at the version
// being restored, and having read what the note says now tells a caller
// nothing about what it is about to say. The commit is the abbreviated hash
// git resolved, so a caller that read HEAD~2 and then restored the full hash of
// the same commit is recognised as having read it.
func versionKey(ref, commit string) string {
	return ref + "@" + commit
}

// New returns a server over the given vault, without search.
func New(v *vault.Vault, version string) *Server {
	return newSession(NewCore(v, version, nil, nil))
}

// WithSearch returns a server that can also search, using the given embedder.
func WithSearch(v *vault.Vault, version string, embedder embed.Embedder) *Server {
	return newSession(NewCore(v, version, embedder, nil))
}

// NewSession returns a session over whatever vaults reach lets it use. A
// non-nil author is who its commits are attributed to.
//
// The session does not own the cores it reaches; whoever opened them closes
// them.
func NewSession(reach Reach, author *git.Author) *Server {
	return &Server{Core: reach.Personal(), reach: reach, author: author}
}

// newSession is the session over a single vault.
func newSession(c *Core) *Server {
	return &Server{Core: c, reach: SingleVault(c)}
}

// NewCore returns the shared state of one vault, without a session to use it.
//
// A hosted process opens cores as they are first needed and gives each session
// a Reach over the ones that session may use. The embedder may be shared by
// any number of cores, since embedding keeps no per-vault state; the index and
// the background indexer are per core. A nil embedder disables search for the
// vault, and a nil repository leaves it outside git.
func NewCore(v *vault.Vault, version string, embedder embed.Embedder, repo *git.Repo) *Core {
	return &Core{
		vault:    v,
		git:      repo,
		embedder: embedder,
		name:     "gandalf",
		version:  version,
		indexer:  newIndexer(),
	}
}

// WithGit returns a server that commits every vault mutation and can sync a
// configured remote through StartSync.
func (s *Server) WithGit(repo *git.Repo) *Server {
	s.git = repo
	return s
}

// StartSync pulls from and pushes to the vault's remote on its configured
// interval, until ctx is cancelled. Without git it does nothing.
//
// The phases of a sync that touch the working tree run under the write lock,
// so a sync can neither sweep a half-written note into its checkpoint commit
// nor rewrite a note a tool is in the middle of changing. The phases that
// talk to the network run outside it, so a stalled remote never holds up a
// write.
func (c *Core) StartSync(ctx context.Context) {
	if c.git == nil {
		return
	}
	c.git.StartSync(ctx, &c.write)
}

// Session returns a server for one new connection, over the same vaults.
//
// Everything the vault has one of is shared; what has been read is not, so one
// agent's reading cannot satisfy another agent's read-before-write gate. The
// returned server holds nothing that needs releasing: Close belongs to whoever
// built the core, and closing it here would take the shared index out from
// under every other session.
func (s *Server) Session() *Server {
	return &Server{
		Core:            s.Core,
		reach:           s.reach,
		author:          s.author,
		GitRemotePolicy: s.GitRemotePolicy,
		CorrectPolicy:   s.CorrectPolicy,
		SetRemote:       s.SetRemote,
	}
}

// beginWrite takes the vault-wide write lock and returns the function that
// releases it. Every tool that changes the vault holds it from before it reads
// the note through to after the change is committed.
func (c *Core) beginWrite() func() {
	c.write.Lock()
	return c.write.Unlock
}

// BeginWrite is beginWrite for code outside this package that replaces part of
// a tool, such as a SetRemote.
func (c *Core) BeginWrite() func() { return c.beginWrite() }

// Repo is the vault's repository, or nil when it is not under git here.
func (c *Core) Repo() *git.Repo { return c.git }

// Vault is the vault this core serves.
func (c *Core) Vault() *vault.Vault { return c.vault }

// checkReason refuses a change that does not say why it is being made.
//
// It is required rather than optional because an optional field is the one a
// model leaves out, and a history of "note replace project:gandalf:design"
// twelve times over answers none of the questions history is asked.
func checkReason(reason string) error {
	if git.OneLine(reason) == "" {
		return fmt.Errorf(
			"a reason is required: say why this change is being made, in a few words. It becomes the " +
				"commit message, so it is what explains this edit to whoever reads the history later. " +
				"What changed and which note is recorded already")
	}
	return nil
}

// Close releases anything the vault's core opened, stopping background
// indexing before the index it writes to is closed.
//
// It belongs to whoever constructed the core, once, at shutdown. Sessions
// returned by Session share the core and must not close it.
func (c *Core) Close() error {
	c.stopIndexing()
	return c.closeIndex()
}

// registration is one tool, ready to be added to an MCP server. Keeping the
// registrations as a table rather than a run of statements is what lets a
// caller expose a subset: the hosted mode offers tools by role, and filtering
// needs the names before anything is registered.
type registration struct {
	name string
	add  func(srv *sdk.Server)
}

// tool builds the registration for one handler. The input and output types
// come from the handler, so the schema the client sees is derived from the
// same structs the handler decodes.
func tool[In, Out any](name, description string, handler sdk.ToolHandlerFor[In, Out]) registration {
	return registration{name: name, add: func(srv *sdk.Server) {
		sdk.AddTool(srv, &sdk.Tool{Name: name, Description: description}, handler)
	}}
}

// registrations is every tool this server offers, in the order they are listed.
func (s *Server) registrations() []registration {
	return []registration{
		tool("boot",
			"Start here, before any other work. Returns the operating contract, "+
				"the topics available on demand, and any session notes already open today. "+
				"Read what it returns; it is the working agreement for this session.",
			s.boot),

		tool("search",
			"Find notes by meaning, not just wording. Use this before starting work "+
				"to see what the vault already says about a topic. Returns refs, so a hit can be "+
				"read straight away.",
			s.search),

		tool("list",
			"List what the vault holds, without content: sessions, projects, "+
				"standards, topics, meetings, interviews, or all. Use it to find prior work "+
				"before starting, and to discover which projects exist.",
			s.list),

		tool("note_read",
			"Read a note or an operating topic by ref. Refs come from boot, "+
				"search, list, or whichever tool created the note; they are never "+
				"constructed from a file path. Read the matching topic before proposing or "+
				"changing work it covers.",
			s.noteRead),

		tool("session_start",
			"Create this session's note, before proposing or writing code. "+
				"One note per logical unit of work, not one per conversation. Returns the "+
				"ref to append to as the work proceeds.",
			s.sessionStart),

		tool("note_new",
			"Create a note. Gandalf decides where it is filed and writes its "+
				"metadata; supply the content. Returns the ref.",
			s.noteNew),

		tool("note_append",
			"Append content to a note, optionally under a new heading. Adding to "+
				"a note can never lose what it already says, so prefer it whenever the new "+
				"content sits alongside the old rather than replacing it.",
			s.noteAppend),

		tool("note_replace",
			"Rewrite part of a note that describes current state — a design note, "+
				"a backlog, a standard. Name a section to rewrite just that section, or give "+
				"from and to anchors to rewrite the span between them; with neither, the whole "+
				"body is replaced. Returns the text it removed, so check that against what you "+
				"meant to remove. Refused by default on notes that are a chronological record, "+
				"such as sessions and decision logs: append to those instead, or pass force to "+
				"repair a defect in one. Read the note first.",
			s.noteReplace),

		tool("note_update",
			"Update a note's metadata — tags, related links, status. The updated "+
				"date is maintained automatically; the body is not touched.",
			s.noteUpdate),

		tool("topic_new",
			"Declare a new operating topic: a document read on demand, listed in "+
				"boot's topic table beside shipping and diagnostics, and addressed as "+
				"topic:<id>. For standing guidance that applies to a kind of work rather "+
				"than to a project — a style guide, a review procedure. Ask first: it "+
				"changes what every session is offered.",
			s.topicNew),

		tool("category_list",
			"List the kinds of note this vault holds, how each is filed, and how "+
				"its notes are addressed. Check here before creating a note of an unfamiliar kind.",
			s.categoryList),

		tool("category_create",
			"Declare a new kind of note, when what the user is keeping does not fit "+
				"the kinds that exist. A design decision, not a routine one: ask first.",
			s.categoryCreate),

		tool("category_retire",
			"Stop new notes being filed under a category, leaving the ones already "+
				"there readable and writable.",
			s.categoryRetire),

		tool("category_delete",
			"Remove a category entirely. Only works when it holds no notes; retire "+
				"it instead if it does.",
			s.categoryDelete),

		tool("note_delete",
			"Delete a note. Refused when other notes link to it, listing them so "+
				"the links can be removed first.",
			s.noteDelete),

		tool("lint",
			"Validate note metadata and links, for one note or the whole vault. "+
				"Reports are addressed by ref.",
			s.lint),

		tool("correct",
			"Record a correction from the user in the vault, in the one file that "+
				"owns that kind of guidance. Call this in the same reply as applying the "+
				"correction, so it survives the session.",
			s.correct),

		tool("history",
			"List what has changed, newest first: the commits behind one note when given "+
				"a ref, or every change to the vault when not. Returns the commit each change was "+
				"made in, which note_version, note_diff, and note_restore all take.",
			s.history),

		tool("note_version",
			"Read a note as it stood at a commit, without changing anything. Use it to "+
				"see what a note said before an edit, and to check a version before putting it back — "+
				"note_restore refuses a version that has not been read here first.",
			s.noteVersion),

		tool("note_diff",
			"Show what changed in a note between two commits, or between a commit and "+
				"what the note says now. Prefer this to reading two whole versions when the question "+
				"is what moved.",
			s.noteDiff),

		tool("note_restore",
			"Put a past version of a note back, as a new commit that leaves the history "+
				"intact. Read the version with note_version first. Restoring a note that is a "+
				"chronological record, such as a session or a decisions log, discards what was added "+
				"since and needs force.",
			s.noteRestore),

		tool("git_remote",
			"Configure the vault's git remote URL so Gandalf can push and pull. "+
				"Gandalf commits every change automatically; use this when the user wants the "+
				"vault mirrored to a remote, or pass an empty url to stop syncing. Conflicts "+
				"resolve as remote-wins on pull.",
			s.gitRemote),
	}
}

// ToolNames lists every tool this server can offer, so a caller deciding what
// to expose can see what there is to choose from.
func (s *Server) ToolNames() []string {
	var names []string
	for _, r := range s.registrations() {
		names = append(names, r.name)
	}
	return names
}

// MCP builds the MCP server with every tool registered.
func (s *Server) MCP() *sdk.Server { return s.MCPFor(nil) }

// MCPFor builds the MCP server with only the tools allow accepts; a nil allow
// accepts them all.
//
// Filtering happens at registration, so a tool that is not allowed is absent
// from the tool list and cannot be called at all, rather than being listed and
// refused. Whether a role may use a tool is decided by the caller; this only
// honours the answer.
func (s *Server) MCPFor(allow func(toolName string) bool) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{
		Name:    s.name,
		Version: s.version,
	}, nil)

	for _, r := range s.registrations() {
		if allow != nil && !allow(r.name) {
			continue
		}
		r.add(srv)
	}
	return srv
}

// Run serves over stdio until the client disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Start indexing as the server comes up rather than on the first search.
	// The cost is the same either way; paying it in the background means the
	// session is not blocked on it, and boot can say how far it got.
	s.startIndexing(ctx)

	if err := s.MCP().Run(ctx, &sdk.StdioTransport{}); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// resolve turns a ref string into a ref and the note path it addresses.
//
// Topics are resolved here rather than in the vault package: a shipped topic's
// path comes from the manifest compiled into the binary, and a vault-declared
// one from the registry the vault keeps, neither of which is a filing rule.
func (c *scoped) resolve(raw string) (vault.Ref, string, error) {
	// Topics are addressed by a reserved kind rather than a category, because
	// their homes are declared individually rather than derived from a rule.
	if id, ok := strings.CutPrefix(strings.TrimSpace(raw), KindTopic+":"); ok {
		entry, found := c.lookupTopic(strings.TrimSpace(id))
		if !found {
			return vault.Ref{}, "", fmt.Errorf("ref %q: no such topic", raw)
		}
		return c.canonical(entry.Path), entry.Path, nil
	}

	ref, err := c.vault.ParseRef(raw)
	if err != nil {
		// A bare topic id is accepted: the model has just read a table of
		// them, and refusing "shipping" for want of a prefix would be pedantry
		// rather than a safeguard.
		if entry, found := c.lookupTopic(strings.TrimSpace(raw)); found {
			return c.canonical(entry.Path), entry.Path, nil
		}

		// A path is the one wrong answer worth answering properly: the vault
		// can work out which note was meant, so say so rather than leaving the
		// caller to guess the scheme from a rejection.
		if suggestion, ok := c.suggest(raw); ok {
			return vault.Ref{}, "", fmt.Errorf("%w. That note is addressed as %s", err, c.qualify(suggestion))
		}
		return vault.Ref{}, "", err
	}

	path, err := c.vault.Resolve(ref)
	if err != nil {
		return vault.Ref{}, "", err
	}

	// What comes back is always the canonical ref, never the one that was
	// passed in. Aliases matter most here — a caller storing "session:latest"
	// from a result would later read whichever note was newest then — but the
	// same applies to a seeded standard reached by its topic id.
	return c.canonical(path), path, nil
}

// suggest returns the ref addressing a path a caller passed by mistake, when
// that path names a note the vault actually holds.
func (c *scoped) suggest(raw string) (vault.Ref, bool) {
	candidate := strings.TrimSpace(raw)
	if !strings.HasSuffix(candidate, ".md") {
		candidate += ".md"
	}

	if !c.vault.Exists(candidate) {
		return vault.Ref{}, false
	}
	return c.canonical(candidate), true
}

// canonical returns the one ref that addresses a note.
//
// Shipped documents are the awkward case: a seeded standard lives where the
// layout can name it, so it is standard:<name>, while the operating topics
// live outside the layout's conventions and are reached as topic:<id>. Picking
// one per note matters — two names for the same note would have boot and lint
// disagreeing about what to call it.
func (c *Core) canonical(notePath string) vault.Ref {
	return CanonicalRef(c.vault, notePath)
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

	if t, ok := v.Topics().ByPath(notePath); ok {
		return vault.Ref{Kind: KindTopic, Name: t.ID}
	}

	return v.RefFor(notePath)
}

// writable resolves a ref and refuses the read-only kinds.
func (c *scoped) writable(raw string) (vault.Ref, string, error) {
	ref, path, err := c.resolve(raw)
	if err != nil {
		return vault.Ref{}, "", err
	}
	if !ref.Writable() {
		return vault.Ref{}, "", fmt.Errorf(
			"ref %q addresses a note outside the vault's filing conventions, which Gandalf will not write to", raw)
	}
	return ref, path, nil
}
