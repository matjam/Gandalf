package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/matjam/gandalf/internal/vault"
)

// A session can reach more than one vault. Its own personal vault is always
// there; in the hosted mode each team it belongs to adds another. Everything
// below is the routing that lets one tool call land in the right one:
// refs name the vault they are in, and the vault is looked up, and the
// caller's right to it checked, on every call.

// ErrNoAccess is what a Reach returns for a team the caller is not a member of
// now. It is also what it returns for a team that does not exist: telling the
// two apart would let a non-member probe which teams there are.
var ErrNoAccess = errors.New("no access to this team vault")

// ErrNoTeams is what a Reach returns when it has no team vaults at all, as in
// stdio and single-token HTTP mode.
var ErrNoTeams = errors.New("this server has no team vaults")

// notMemberError is the refusal a caller reads for a team it cannot use. The
// wording is the same whether the team is unknown or merely not theirs.
type notMemberError struct{ slug string }

func (e *notMemberError) Error() string { return "you are not a member of team " + e.slug }

func (e *notMemberError) Unwrap() error { return ErrNoAccess }

// TeamInfo names a team vault a session can reach.
type TeamInfo struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Reach is the set of vaults one session may use, checked live on every call.
//
// It is a port rather than a list because membership changes while sessions
// are open. A session holding a snapshot of its teams would keep working in a
// team it had been removed from until it reconnected; asking on every call
// makes removal take effect on the next call.
type Reach interface {
	// Personal is the caller's own vault. It is always reachable.
	Personal() *Core

	// Team returns a team's vault, or ErrNoAccess when the caller is not a
	// member of it now (or there is no such team).
	Team(ctx context.Context, slug string) (*Core, error)

	// Teams lists the teams reachable now.
	Teams(ctx context.Context) ([]TeamInfo, error)
}

// SingleVault is the Reach for a process serving exactly one vault: stdio, and
// the single-token HTTP mode. It has no teams, and says so rather than
// pretending a team is unreachable.
func SingleVault(c *Core) Reach { return personalOnly{core: c} }

type personalOnly struct{ core *Core }

func (p personalOnly) Personal() *Core { return p.core }

func (p personalOnly) Team(context.Context, string) (*Core, error) { return nil, ErrNoTeams }

func (p personalOnly) Teams(context.Context) ([]TeamInfo, error) { return nil, nil }

// ScopePolicy decides whether a session may do something to a vault.
//
// scope is the team slug the action is aimed at, or "" for the session's
// personal vault. A nil policy allows the personal vault, which is the caller's
// own, and refuses every team: a team's vault is shared, so acting on it needs
// someone to have said who may, and a process that serves teams without having
// installed a policy fails closed rather than open. Single-vault mode has no
// teams, so the difference never shows there. Hosted mode installs a policy
// that turns the action into a role check. A non-nil error refuses the action,
// and its text is what the caller reads.
type ScopePolicy func(ctx context.Context, scope string) error

// errNoPolicy is the refusal for a team action when no policy was installed.
var errNoPolicy = errors.New("no policy allows this on a team vault")

// permit asks policy whether the session may act on the vault scope names.
func (s *Server) permit(ctx context.Context, policy ScopePolicy, scope string) error {
	if policy != nil {
		return policy(ctx, scope)
	}
	if scope != "" {
		return errNoPolicy
	}
	return nil
}

// scoped is one vault as one session sees it: the shared Core, the session
// whose reading and authorship apply, and the name the vault goes by in refs.
//
// Every handler works through one of these after routing, so the code that
// used to say s.vault says the same thing about whichever vault the call was
// aimed at.
type scoped struct {
	*Core
	srv *Server

	// slug is the team the vault belongs to, or "" for the personal vault.
	slug string
}

// prefix is what a ref in this vault is written with in front: "@slug:" for a
// team, nothing for the personal vault, whose refs stay exactly as they were.
func (c *scoped) prefix() string {
	if c.slug == "" {
		return ""
	}
	return "@" + c.slug + ":"
}

// qualify renders a ref the way a caller addresses it.
func (c *scoped) qualify(ref vault.Ref) string { return c.prefix() + ref.String() }

// qualifyString qualifies a ref already rendered as text, leaving the empty
// string alone.
func (c *scoped) qualifyString(ref string) string {
	if ref == "" {
		return ""
	}
	return c.prefix() + ref
}

// slugPattern bounds what may follow the @ in a scoped ref. Whether a slug
// names a team is the Reach's business; this only keeps the syntax from
// swallowing things that were never meant to be one.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// splitScope separates an optional "@slug:" prefix from a ref.
//
// A personal ref can never begin with @, which is what makes the prefix
// unambiguous: nothing that parsed before the prefix existed parses
// differently now.
func splitScope(raw string) (slug, rest string, err error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "@") {
		return "", trimmed, nil
	}

	name, rest, ok := strings.Cut(trimmed[1:], ":")
	name, rest = strings.TrimSpace(name), strings.TrimSpace(rest)
	if !ok || !slugPattern.MatchString(name) || rest == "" {
		return "", "", fmt.Errorf("ref %q: a team ref is written @<team>:<ref>, such as @platform:standard:testing", raw)
	}
	return name, rest, nil
}

// personal returns the session's own vault.
func (s *Server) personal() *scoped {
	return &scoped{Core: s.Core, srv: s, slug: ""}
}

// vaultFor returns the vault a team slug names, or the personal vault for "".
//
// Access is decided here, by the Reach, on this call. Any refusal that means
// "not yours" is worded identically whether the team exists or not.
func (s *Server) vaultFor(ctx context.Context, slug string) (*scoped, error) {
	slug = cleanVaultParam(slug)
	if slug == "" || slug == personalName {
		return s.personal(), nil
	}

	// Only something shaped like a slug is passed on. Whether it names a team
	// is the Reach's business, but it is handed a lookup key and not whatever a
	// caller typed, so an implementation that builds a path or a query from the
	// slug cannot be given "../x". The refusal is the one any unknown team gets.
	if !slugPattern.MatchString(slug) {
		return nil, &notMemberError{slug: slug}
	}

	core, err := s.reach.Team(ctx, slug)
	switch {
	case err == nil && core != nil:
		return &scoped{Core: core, srv: s, slug: slug}, nil
	case errors.Is(err, ErrNoTeams):
		return nil, err
	case err == nil, errors.Is(err, ErrNoAccess):
		return nil, &notMemberError{slug: slug}
	default:
		return nil, fmt.Errorf("open the vault of team %s: %w", slug, err)
	}
}

// cleanVaultParam tidies a vault parameter: surrounding space and a leading @
// are dropped, since a caller who has been reading @platform:... refs will
// reasonably write @platform here too.
//
// The word "personal" names the caller's own vault wherever a vault can be
// named, which is what lets a tool that defaults to every vault be pointed at
// just that one. A team therefore cannot be called "personal".
func cleanVaultParam(p string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p), "@"))
}

// route finds the vault a ref is in and returns the ref with its prefix
// removed.
func (s *Server) route(ctx context.Context, raw string) (*scoped, string, error) {
	return s.routeIn(ctx, raw, "")
}

// routeIn is route for a tool that also takes a vault parameter. The ref's own
// prefix wins when there is one, and it is an error for the two to disagree;
// an unprefixed ref is taken to be in the named vault, which saves a caller
// from writing the same slug twice.
func (s *Server) routeIn(ctx context.Context, raw, vaultParam string) (*scoped, string, error) {
	slug, rest, err := splitScope(raw)
	if err != nil {
		return nil, "", err
	}

	vaultParam = cleanVaultParam(vaultParam)
	switch {
	case slug == "":
		slug = vaultParam
	case vaultParam != "" && vaultParam != slug:
		return nil, "", fmt.Errorf("ref %q is in team %s but vault is %s; pass one or the other", raw, slug, vaultParam)
	}

	sc, err := s.vaultFor(ctx, slug)
	if err != nil {
		return nil, "", err
	}
	return sc, rest, nil
}

// resolveRef routes a ref and resolves it within its vault.
func (s *Server) resolveRef(ctx context.Context, raw, vaultParam string) (*scoped, vault.Ref, string, error) {
	sc, rest, err := s.routeIn(ctx, raw, vaultParam)
	if err != nil {
		return nil, vault.Ref{}, "", err
	}
	ref, path, err := sc.resolve(rest)
	if err != nil {
		return nil, vault.Ref{}, "", err
	}
	return sc, ref, path, nil
}

// writableRef is resolveRef for a note about to be changed.
func (s *Server) writableRef(ctx context.Context, raw, vaultParam string) (*scoped, vault.Ref, string, error) {
	sc, rest, err := s.routeIn(ctx, raw, vaultParam)
	if err != nil {
		return nil, vault.Ref{}, "", err
	}
	ref, path, err := sc.writable(rest)
	if err != nil {
		return nil, vault.Ref{}, "", err
	}
	return sc, ref, path, nil
}

// reachable returns every vault the session can use now: the personal vault
// first, then each team in the order the Reach lists them. A team that stopped
// being reachable between the listing and the lookup is left out rather than
// failing the whole call.
func (s *Server) reachable(ctx context.Context) ([]*scoped, error) {
	out := []*scoped{s.personal()}

	teams, err := s.reach.Teams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list team vaults: %w", err)
	}
	for _, team := range teams {
		sc, err := s.vaultFor(ctx, team.Slug)
		if err != nil {
			if errors.Is(err, ErrNoTeams) || errors.Is(err, ErrNoAccess) {
				continue
			}
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// vaultsFor chooses the vaults a read-only tool covers: the one named, or
// every vault the session can reach when none is.
func (s *Server) vaultsFor(ctx context.Context, vaultParam string) ([]*scoped, error) {
	vaultParam = cleanVaultParam(vaultParam)
	if vaultParam == "" {
		return s.reachable(ctx)
	}
	sc, err := s.vaultFor(ctx, vaultParam)
	if err != nil {
		return nil, err
	}
	return []*scoped{sc}, nil
}

// markRead records that a note's current text has been seen by this session.
//
// The key carries the vault, so having read standard:testing in the personal
// vault says nothing about @platform:standard:testing: they are different
// notes that happen to share a name.
func (c *scoped) markRead(ref vault.Ref) { c.srv.markRead(c.qualify(ref)) }

// hasRead reports whether this session has read the note.
func (c *scoped) hasRead(ref vault.Ref) bool { return c.srv.hasRead(c.qualify(ref)) }

// markVersionRead records that a past version of a note has been seen.
func (c *scoped) markVersionRead(ref vault.Ref, commit string) {
	c.srv.markRead(versionKey(c.qualify(ref), commit))
}

// hasReadVersion reports whether a version of a note has been read.
func (c *scoped) hasReadVersion(ref vault.Ref, commit string) bool {
	return c.srv.hasRead(versionKey(c.qualify(ref), commit))
}

// record commits the vault after a successful mutation, attributed to the
// session's author when it has one, and returns the warning for the tool's
// result when git could not record it. See Core.commit for the failure policy.
func (c *scoped) record(message, reason string) string {
	return c.commit(message, reason, c.srv.author)
}

// resolve resolves a ref in the personal vault. It is the single-vault form of
// the routed resolution the tools use, kept for callers that have no context
// and no business with teams.
func (s *Server) resolve(raw string) (vault.Ref, string, error) {
	return s.personal().resolve(raw)
}
