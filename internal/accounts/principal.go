package accounts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Membership is one user's place in one team.
type Membership struct {
	TeamID string
	Slug   string
	Role   Role
}

// Principal is who is making a request: the result of authenticating a key,
// loaded fresh for each request and never cached across requests, so a
// revocation, demotion or erasure takes effect on the next call.
//
// Memberships lists only teams that are not archived: an archived team's
// vault is no longer reachable through membership.
//
// A Principal is a claim, not a capability. Every Store method that acts on
// someone's behalf re-reads the acting user's standing from the database and
// ignores the flags in the struct it was handed, so a stale or hand-built
// Principal cannot grant more than the database does. The zero Principal is
// refused everywhere.
type Principal struct {
	UserID      string
	Handle      string // Personal.
	GlobalAdmin bool
	KeyID       string
	KeyExpires  time.Time
	Memberships []Membership
}

// RoleIn returns the principal's role in the team with the given id.
func (p Principal) RoleIn(teamID string) (Role, bool) {
	for _, m := range p.Memberships {
		if m.TeamID == teamID {
			return m.Role, true
		}
	}
	return "", false
}

// RoleInSlug is RoleIn addressed by team slug, which is how vaults are named
// to users.
func (p Principal) RoleInSlug(slug string) (Role, bool) {
	for _, m := range p.Memberships {
		if m.Slug == slug {
			return m.Role, true
		}
	}
	return "", false
}

// IsMemberOf reports whether the principal belongs to the team (as member or
// admin). Global administrators are not implicitly members: administering
// accounts does not grant reading a team's notes.
func (p Principal) IsMemberOf(teamID string) bool {
	_, ok := p.RoleIn(teamID)
	return ok
}

// IsTeamAdmin reports whether the principal holds the admin role in the team.
func (p Principal) IsTeamAdmin(teamID string) bool {
	r, ok := p.RoleIn(teamID)
	return ok && r == RoleAdmin
}

// CanManageTeam reports whether the principal may change the team's
// membership: a global administrator, or an administrator of that team.
func (p Principal) CanManageTeam(teamID string) bool {
	return p.GlobalAdmin || p.IsTeamAdmin(teamID)
}

// administersAny reports whether the principal administers at least one team.
func (p Principal) administersAny() bool {
	for _, m := range p.Memberships {
		if m.Role == RoleAdmin {
			return true
		}
	}
	return false
}

// CanManageRemote reports whether the principal may manage the git remote of
// the vault with the given id:
//
//   - a user's personal vault (id == their user id): that user only. Not
//     another user, and not a global administrator: an administrator's
//     authority is over accounts, not over what someone keeps in their own
//     notes' sync.
//   - a team vault: a global administrator, or an administrator of that team.
//   - anything else: nobody.
func (p Principal) CanManageRemote(vaultID string) bool {
	switch {
	case p.UserID == "":
		return false
	case strings.HasPrefix(vaultID, userIDPrefix):
		return vaultID == p.UserID
	case strings.HasPrefix(vaultID, teamIDPrefix):
		return p.CanManageTeam(vaultID)
	}
	return false
}

// String and GoString keep the handle out of any accidental %v or %#v.
func (p Principal) String() string   { return "Principal{" + p.UserID + "}" }
func (p Principal) GoString() string { return p.String() }

// LogValue makes slog print opaque ids only.
func (p Principal) LogValue() slog.Value {
	return slog.GroupValue(slog.String("user", p.UserID), slog.String("key", p.KeyID))
}

const (
	userIDPrefix = "u_"
	teamIDPrefix = "t_"
	keyIDPrefix  = "k_"
)

// liveKey restricts a keys/users join to credentials that currently work. It
// is the single definition of "valid", shared by Authenticate and by the
// per-call re-check, so the two cannot drift.
const liveKey = ` AND k.revoked IS NULL AND k.expires > ? AND u.deleted IS NULL`

const principalSelect = `
	SELECT k.id, k.user_id, k.expires, u.handle, u.global_admin
	FROM keys k JOIN users u ON u.id = k.user_id
	WHERE `

// Authenticate resolves a plaintext API key to the principal it belongs to.
//
// Every failure to validate the key (malformed, unknown, revoked, expired,
// or owned by an erased user) returns ErrInvalidKey and nothing more. Other
// errors are database failures and are not ErrInvalidKey, so the caller can
// answer 5xx rather than 401.
//
// The key is looked up by the SHA-256 of its full text through a unique
// index, not compared in constant time. That is sound because the key is 256
// random bits: timing can leak how the hash of an attacker's guess compares
// with stored hashes, but finding a guess whose hash matches is a preimage
// search the attacker cannot shortcut, and nothing learned about the hash of
// a wrong guess helps with the secret behind a right one. (A password, being
// low-entropy, would need a slow salted hash; a key does not.)
func (s *Store) Authenticate(ctx context.Context, plaintext string) (Principal, error) {
	// Reject the wrong shape before touching the database: unauthenticated
	// callers should not be able to make it work for garbage.
	if len(plaintext) != keyLen || !strings.HasPrefix(plaintext, KeyPrefix) {
		return Principal{}, ErrInvalidKey
	}

	sum := sha256.Sum256([]byte(plaintext))
	p, err := s.principal(ctx, s.db, "k.hash = ?"+liveKey, sum[:], unix(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrInvalidKey
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	return p, nil
}

// principal loads a live key's principal. where must include liveKey and its
// arguments; sql.ErrNoRows is returned when nothing matches.
func (s *Store) principal(ctx context.Context, q querier, where string, args ...any) (Principal, error) {
	var (
		p       Principal
		expires int64
		admin   int
	)
	err := q.QueryRowContext(ctx, principalSelect+where, args...).
		Scan(&p.KeyID, &p.UserID, &expires, &p.Handle, &admin)
	if err != nil {
		return Principal{}, err
	}
	p.KeyExpires = fromUnix(expires)
	p.GlobalAdmin = admin == 1

	p.Memberships, err = s.activeMemberships(ctx, q, p.UserID)
	if err != nil {
		return Principal{}, err
	}
	return p, nil
}

// activeMemberships lists a user's memberships in teams that are not archived.
func (s *Store) activeMemberships(ctx context.Context, q querier, userID string) ([]Membership, error) {
	return s.memberships(ctx, q, userID, true)
}

// memberships lists a user's memberships, optionally only in live teams.
func (s *Store) memberships(ctx context.Context, q querier, userID string, activeOnly bool) ([]Membership, error) {
	query := `
		SELECT t.id, t.slug, m.role
		FROM memberships m JOIN teams t ON t.id = m.team_id
		WHERE m.user_id = ?`
	if activeOnly {
		query += ` AND t.archived IS NULL`
	}
	query += ` ORDER BY t.slug`

	rows, err := q.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("read memberships: %w", err)
	}
	defer rows.Close()

	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.TeamID, &m.Slug, &m.Role); err != nil {
			return nil, fmt.Errorf("read memberships: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// actor re-establishes who is acting from the database. It requires the
// principal's own key to still be valid and its user to still exist, and
// returns their current standing; anything else is ErrForbidden. Every
// authorising method starts here, inside the same transaction as its writes,
// so the decision and the change see one consistent state.
func (s *Store) actor(ctx context.Context, q querier, p Principal) (Principal, error) {
	if p.UserID == "" || p.KeyID == "" {
		return Principal{}, ErrForbidden
	}
	fresh, err := s.principal(ctx, q, "k.id = ? AND k.user_id = ?"+liveKey, p.KeyID, p.UserID, unix(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrForbidden
	}
	if err != nil {
		return Principal{}, fmt.Errorf("check acting user: %w", err)
	}
	return fresh, nil
}

// requireGlobalAdmin is actor plus the global administrator check.
func (s *Store) requireGlobalAdmin(ctx context.Context, q querier, p Principal) (Principal, error) {
	a, err := s.actor(ctx, q, p)
	if err != nil {
		return Principal{}, err
	}
	if !a.GlobalAdmin {
		return Principal{}, ErrForbidden
	}
	return a, nil
}
