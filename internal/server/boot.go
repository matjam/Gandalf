package server

import (
	"context"
	"errors"
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

// StandardSummary is one standard a team vault holds.
type StandardSummary struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
}

// TeamBoot is what a session needs to know on arrival about one team vault it
// can reach: how the team asks to work, what it has written down, and whether
// its notes can be searched yet.
//
// Everything is addressed with the team in the ref, so the caller can read any
// of it without working out which vault it came from.
type TeamBoot struct {
	Slug string `json:"slug"`
	Name string `json:"name"`

	// Operating is the team's own operating document, when its vault has one.
	// It is the team's counterpart of the personal contract and governs work
	// on the team's projects; there is no shipped fallback, because a team
	// that wrote nothing has no operating document to be reminded of.
	Operating *Document `json:"operating,omitempty"`

	Topics    []TopicSummary    `json:"topics,omitempty"`
	Standards []StandardSummary `json:"standards,omitempty"`

	// Search is the state of this team vault's search index.
	Search IndexStatus `json:"search"`

	// GitNotice is present when this team vault's own repository is broken or
	// not syncing. Each vault has its own repository, so the personal vault's
	// health says nothing about a team's and the reverse.
	GitNotice
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
	// GitNotice comes first so that it is the first thing in the result. A
	// session that starts without hearing the vault is not being backed up
	// goes on to write notes it believes are safe.
	GitNotice

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
	// if it assumes a capability that is still warming up or absent. It
	// describes the personal vault; each team carries its own.
	Search IndexStatus `json:"search"`

	// Teams are the team vaults this session can reach, listed only when there
	// are any.
	Teams []TeamBoot `json:"teams,omitempty"`
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
	sc := s.personal()
	sc.startIndexing(ctx)

	out := BootOutput{
		Vault:   sc.vault.Root(),
		Version: instructions.Version,
		Search:  sc.indexStatus(),
		Addressing: "Notes are addressed by ref — what a note is, not where it lives — never by " +
			"path. The ref forms this vault uses are below, generated from the categories it " +
			"declares. Refs come from these tools; do not construct one from a filename.",
		Categories:  sc.addressing(),
		Conventions: conventions,
	}

	// Git's health is reported before anything else, and in the notices too,
	// where a session looks for things it should act on. It says what failed,
	// since when, and that changes are not being committed or synced: the
	// vault keeps accepting writes while broken, which is the reason it has to
	// be said.
	if warning := sc.gitWarning(); warning != "" {
		out.GitWarning = warning
		out.Notices = append(out.Notices, "GIT IS NOT WORKING: "+warning)
	}

	for _, doc := range instructions.Core() {
		content, source, err := sc.document(doc)
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

	out.Topics = sc.topicSummaries()

	open, err := sc.openSessions(schema.Today())
	if err != nil {
		return nil, BootOutput{}, err
	}
	out.OpenToday = open

	// Teams come after the personal vault's own state, and their open sessions
	// join the same list with the team in the ref, so "continue the session
	// that is already open" works the same wherever that session lives.
	teams, err := s.reach.Teams(ctx)
	if err != nil {
		return nil, BootOutput{}, fmt.Errorf("list team vaults: %w", err)
	}
	for _, team := range teams {
		tc, err := s.vaultFor(ctx, team.Slug)
		if err != nil {
			if errors.Is(err, ErrNoAccess) {
				// Membership ended between the listing and the lookup.
				continue
			}
			return nil, BootOutput{}, err
		}

		// A team's repository is examined whether or not the rest of its boot
		// could be read: what a writer needs to hear is that its changes are
		// not being kept, and that is true either way. The personal vault's
		// report came first.
		gitWarning := tc.gitWarning()
		if gitWarning != "" {
			out.Notices = append(out.Notices,
				fmt.Sprintf("GIT IS NOT WORKING for team %s: %s", team.Slug, gitWarning))
		}

		boot, teamOpen, err := tc.bootTeam(ctx, team)
		if err != nil {
			out.Notices = append(out.Notices,
				fmt.Sprintf("team %s could not be read and is left out: %v", team.Slug, err))
			continue
		}
		boot.GitWarning = gitWarning
		out.Teams = append(out.Teams, boot)
		open = append(open, teamOpen...)
	}
	out.OpenToday = open

	if len(out.Teams) > 0 {
		out.Conventions = append(out.Conventions[:len(out.Conventions):len(out.Conventions)], teamConventions...)
	}

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

// teamConventions are added to boot's conventions when the session can reach
// any team vault. They are conditional because a rule about a feature the
// session does not have is noise in every boot of a single-vault install.
var teamConventions = []string{
	"A note in a team's vault is addressed @<team>:<ref>, such as @platform:standard:testing; refs without the @team: prefix are your personal vault. Tools that take no ref take a vault parameter naming the team.",
	"Links between vaults are refused, because Obsidian cannot resolve a link from one vault into another. Inside a team note, link by ref as usual (the @team: prefix is optional there); to point at a note in another vault, name it in prose.",
	"A team's standards and operating document (listed under teams) govern work on that team's projects, alongside your own contract and standards.",
}

// addressing reports how each of the vault's categories is named and whether
// its notes may be rewritten.
func (c *Core) addressing() map[string]Addressing {
	out := map[string]Addressing{}

	for _, c := range c.vault.Categories().Categories {
		view := view(c, 0)
		out[c.Name] = Addressing{
			RefForm:         view.RefForm,
			Holds:           view.Description,
			Mutability:      view.Mutability,
			FacetMutability: view.FacetMutability,
			Retired:         view.Retired,
		}
	}

	// Topics are declared individually rather than by a category's filing
	// rule, so nothing above accounts for them, and a model that cannot
	// address them cannot read the operating topics boot just listed.
	out[KindTopic] = Addressing{
		RefForm: KindTopic + ":<name>",
		Holds: "operating topics: the ones Gandalf ships and the ones this vault adds " +
			"with topic_new; the vault's copy is what is served",
		Mutability: string(category.Replaceable),
	}

	return out
}

// document returns a shipped document's text, preferring the vault's copy.
func (c *Core) document(doc instructions.Doc) (content, source string, err error) {
	if c.vault.Exists(doc.Path) {
		note, err := c.vault.Read(doc.Path)
		if err != nil {
			return "", "", fmt.Errorf("read %q: %w", doc.Path, err)
		}
		// The backlinks block helps someone navigating the vault and tells a
		// session starting work nothing, so boot leaves it out.
		return note.Content(), "vault", nil
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
func (c *scoped) openSessions(on schema.Date) ([]OpenSession, error) {
	paths, err := c.vault.List()
	if err != nil {
		return nil, err
	}

	prefix := on.String()

	var open []OpenSession
	for _, p := range paths {
		ref := c.vault.RefFor(p)
		if ref.Kind != sessionCategory || !strings.HasPrefix(ref.Name, prefix) {
			continue
		}

		note, err := c.vault.Read(p)
		if err != nil {
			// A malformed note is lint's problem, not boot's.
			continue
		}
		if note.FM.Status != schema.StatusInProgress {
			continue
		}

		open = append(open, OpenSession{Ref: c.qualify(ref), Title: note.Title()})
	}

	return open, nil
}

// bootTeam reports one team vault: its operating document, its topics and
// standards, its index state, and the sessions still open in it today.
//
// Indexing is started here, as it is for the personal vault, so the work
// happens while the model reads the contract rather than on its first search.
func (c *scoped) bootTeam(ctx context.Context, team TeamInfo) (TeamBoot, []OpenSession, error) {
	c.startIndexing(ctx)

	out := TeamBoot{Slug: c.slug, Name: team.Name}

	operating := instructions.Contract()
	if c.vault.Exists(operating.Path) {
		note, err := c.vault.Read(operating.Path)
		if err != nil {
			return TeamBoot{}, nil, fmt.Errorf("read %q: %w", operating.Path, err)
		}
		out.Operating = &Document{
			ID:      operating.ID,
			Title:   operating.Title,
			Content: c.toRefs(note.Content()),
			Source:  "vault",
		}
	}

	out.Topics = c.topicSummaries()

	standards, err := c.standards()
	if err != nil {
		return TeamBoot{}, nil, err
	}
	out.Standards = standards
	out.Search = c.indexStatus()

	open, err := c.openSessions(schema.Today())
	if err != nil {
		return TeamBoot{}, nil, err
	}
	return out, open, nil
}

// standardCategory is the category engineering standards are filed under.
const standardCategory = "standard"

// standards lists the standards the vault holds, by ref and title.
func (c *scoped) standards() ([]StandardSummary, error) {
	paths, err := c.vault.List()
	if err != nil {
		return nil, err
	}

	var out []StandardSummary
	for _, p := range paths {
		ref := c.canonical(p)
		if ref.Kind != standardCategory {
			continue
		}

		note, err := c.vault.Read(p)
		if err != nil {
			// A malformed note is lint's problem, not boot's.
			continue
		}
		out = append(out, StandardSummary{Ref: c.qualify(ref), Title: note.Title()})
	}
	return out, nil
}
