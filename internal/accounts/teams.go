package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Team is a shared group with a shared vault. Its vault is identified by ID.
type Team struct {
	ID       string
	Slug     string
	Name     string
	Created  time.Time
	Archived time.Time // Zero unless archived.
}

// IsArchived reports whether the team has been archived.
func (t Team) IsArchived() bool { return !t.Archived.IsZero() }

// TeamMember is one row of a team's roster. Handle is Personal.
type TeamMember struct {
	UserID string
	Handle string
	Role   Role
	Added  time.Time
}

// String and GoString print the id only, because Handle is Personal.
func (m TeamMember) String() string   { return "TeamMember{" + m.UserID + "}" }
func (m TeamMember) GoString() string { return m.String() }

// LogValue makes slog print the opaque id only.
func (m TeamMember) LogValue() slog.Value { return slog.GroupValue(slog.String("user", m.UserID)) }

const teamColumns = `id, slug, name, created, archived`

func scanTeam(r scanner) (Team, error) {
	var (
		t        Team
		created  int64
		archived *int64
	)
	if err := r.Scan(&t.ID, &t.Slug, &t.Name, &created, &archived); err != nil {
		return Team{}, err
	}
	t.Created, t.Archived = fromUnix(created), optTime(archived)
	return t, nil
}

// teamByID loads a team, archived or not.
func (s *Store) teamByID(ctx context.Context, q querier, id string) (Team, error) {
	t, err := scanTeam(q.QueryRowContext(ctx, `SELECT `+teamColumns+` FROM teams WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("read team: %w", err)
	}
	return t, nil
}

// insertTeam validates and stores a team. Callers have authorised it.
func (s *Store) insertTeam(ctx context.Context, q querier, slug, name string) (Team, error) {
	if err := ValidateSlug(slug); err != nil {
		return Team{}, err
	}
	if err := validText("team name", name, maxNameRunes); err != nil {
		return Team{}, err
	}
	var one int
	switch err := q.QueryRowContext(ctx, `SELECT 1 FROM teams WHERE slug = ?`, slug).Scan(&one); {
	case err == nil:
		return Team{}, ErrSlugTaken
	case !errors.Is(err, sql.ErrNoRows):
		return Team{}, fmt.Errorf("check slug: %w", err)
	}

	id, err := newID(teamIDPrefix)
	if err != nil {
		return Team{}, err
	}
	created := fromUnix(unix(s.now()))
	if _, err := q.ExecContext(ctx,
		`INSERT INTO teams (id, slug, name, created) VALUES (?, ?, ?, ?)`,
		id, slug, name, unix(created)); err != nil {
		return Team{}, fmt.Errorf("store team: %w", err)
	}
	return Team{ID: id, Slug: slug, Name: name, Created: created}, nil
}

// CreateTeam makes an empty team. Global administrators only. Add its first
// administrator with AddMember.
func (s *Store) CreateTeam(ctx context.Context, actor Principal, slug, name string) (Team, error) {
	var team Team
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		if team, err = s.insertTeam(ctx, tx, slug, name); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "accounts: team created", idAttr("actor", a.UserID), idAttr("team", team.ID))
		return nil
	})
	if err != nil {
		return Team{}, err
	}
	return team, nil
}

// ArchiveTeam archives a team. Global administrators only. Memberships are
// kept (so the roster is still known) but stop conferring access: archived
// teams do not appear in a Principal's memberships and take no new members.
// The caller archives the vault directory. Archiving twice is a no-op.
func (s *Store) ArchiveTeam(ctx context.Context, actor Principal, teamID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		t, err := s.teamByID(ctx, tx, teamID)
		if err != nil {
			return err
		}
		if t.IsArchived() {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET archived = ? WHERE id = ?`, unix(s.now()), teamID); err != nil {
			return fmt.Errorf("archive team: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: team archived", idAttr("actor", a.UserID), idAttr("team", teamID))
		return nil
	})
}

// ListTeams lists teams. Global administrators only; everyone else uses
// MembershipsOf to see their own.
func (s *Store) ListTeams(ctx context.Context, actor Principal, includeArchived bool, page Page) ([]Team, error) {
	if _, err := s.requireGlobalAdmin(ctx, s.db, actor); err != nil {
		return nil, err
	}
	page = page.norm()

	query := `SELECT ` + teamColumns + ` FROM teams`
	if !includeArchived {
		query += ` WHERE archived IS NULL`
	}
	query += ` ORDER BY slug LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var out []Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("list teams: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TeamBySlug returns a team. Allowed for global administrators and for the
// team's own members; everyone else gets ErrForbidden whether or not the
// slug exists.
func (s *Store) TeamBySlug(ctx context.Context, actor Principal, slug string) (Team, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return Team{}, err
	}
	if !a.GlobalAdmin {
		if _, ok := a.RoleInSlug(slug); !ok {
			return Team{}, ErrForbidden
		}
	}
	t, err := scanTeam(s.db.QueryRowContext(ctx, `SELECT `+teamColumns+` FROM teams WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("read team: %w", err)
	}
	return t, nil
}

// MembershipsOf lists the teams (not archived) a user belongs to. Allowed
// for the user themselves and for global administrators.
func (s *Store) MembershipsOf(ctx context.Context, actor Principal, userID string) ([]Membership, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	if !a.GlobalAdmin && a.UserID != userID {
		return nil, ErrForbidden
	}
	return s.activeMemberships(ctx, s.db, userID)
}

// MembersOf lists a team's roster. Allowed for global administrators and for
// administrators of that team. Plain members cannot enumerate each other.
func (s *Store) MembersOf(ctx context.Context, actor Principal, teamID string) ([]TeamMember, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	if !a.CanManageTeam(teamID) {
		return nil, ErrForbidden
	}
	if _, err := s.teamByID(ctx, s.db, teamID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.handle, m.role, m.added
		 FROM memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.team_id = ? AND u.deleted IS NULL ORDER BY m.added, u.id LIMIT ?`,
		teamID, MaxPageSize*2)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var out []TeamMember
	for rows.Next() {
		var (
			m     TeamMember
			added int64
		)
		if err := rows.Scan(&m.UserID, &m.Handle, &m.Role, &added); err != nil {
			return nil, fmt.Errorf("list members: %w", err)
		}
		m.Added = fromUnix(added)
		out = append(out, m)
	}
	return out, rows.Err()
}

// memberRole returns a user's role in a team, or ErrNotMember.
func memberRole(ctx context.Context, q querier, teamID, userID string) (Role, error) {
	var role Role
	err := q.QueryRowContext(ctx,
		`SELECT role FROM memberships WHERE team_id = ? AND user_id = ?`, teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", fmt.Errorf("read membership: %w", err)
	}
	return role, nil
}

// checkNotLastTeamAdmin enforces that a team administrator cannot leave their
// team with nobody to manage it. Global administrators are exempt: they can
// always repair a team, so for them the rule would only get in the way (for
// example when winding a team down).
func checkNotLastTeamAdmin(ctx context.Context, q querier, a Principal, teamID string) error {
	if a.GlobalAdmin {
		return nil
	}
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.team_id = ? AND m.role = 'admin' AND u.deleted IS NULL`, teamID).Scan(&n); err != nil {
		return fmt.Errorf("count team admins: %w", err)
	}
	if n <= 1 {
		return ErrLastTeamAdmin
	}
	return nil
}

// AddMember adds an existing, live user to a team with the given role.
// Allowed for global administrators and for administrators of that team.
// Archived teams take no new members.
func (s *Store) AddMember(ctx context.Context, actor Principal, teamID, userID string, role Role) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !a.CanManageTeam(teamID) {
			return ErrForbidden
		}
		if !role.valid() {
			return fmt.Errorf("%w: unknown role", ErrInvalidArgument)
		}
		t, err := s.teamByID(ctx, tx, teamID)
		if err != nil {
			return err
		}
		if t.IsArchived() {
			return ErrTeamArchived
		}
		if _, err := s.liveUser(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := memberRole(ctx, tx, teamID, userID); err == nil {
			return ErrAlreadyMember
		} else if !errors.Is(err, ErrNotMember) {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memberships (team_id, user_id, role, added) VALUES (?, ?, ?, ?)`,
			teamID, userID, string(role), unix(s.now())); err != nil {
			return fmt.Errorf("add member: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: member added", idAttr("actor", a.UserID),
			idAttr("team", teamID), idAttr("user", userID), slog.String("role", string(role)))
		return nil
	})
}

// RemoveMember removes a user from a team. Allowed for global administrators
// and for administrators of that team; a team administrator may not remove
// the last administrator (ErrLastTeamAdmin).
func (s *Store) RemoveMember(ctx context.Context, actor Principal, teamID, userID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !a.CanManageTeam(teamID) {
			return ErrForbidden
		}
		if _, err := s.teamByID(ctx, tx, teamID); err != nil {
			return err
		}
		role, err := memberRole(ctx, tx, teamID, userID)
		if err != nil {
			return err
		}
		if role == RoleAdmin {
			if err := checkNotLastTeamAdmin(ctx, tx, a, teamID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM memberships WHERE team_id = ? AND user_id = ?`, teamID, userID); err != nil {
			return fmt.Errorf("remove member: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: member removed", idAttr("actor", a.UserID),
			idAttr("team", teamID), idAttr("user", userID))
		return nil
	})
}

// SetTeamRole changes an existing member's role (promote to or demote from
// team administrator). Allowed for global administrators and for
// administrators of that team; a team administrator may not demote the last
// administrator (ErrLastTeamAdmin).
func (s *Store) SetTeamRole(ctx context.Context, actor Principal, teamID, userID string, role Role) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !a.CanManageTeam(teamID) {
			return ErrForbidden
		}
		if !role.valid() {
			return fmt.Errorf("%w: unknown role", ErrInvalidArgument)
		}
		if _, err := s.teamByID(ctx, tx, teamID); err != nil {
			return err
		}
		current, err := memberRole(ctx, tx, teamID, userID)
		if err != nil {
			return err
		}
		if current == role {
			return nil
		}
		if current == RoleAdmin {
			if err := checkNotLastTeamAdmin(ctx, tx, a, teamID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE memberships SET role = ? WHERE team_id = ? AND user_id = ?`,
			string(role), teamID, userID); err != nil {
			return fmt.Errorf("set team role: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: team role changed", idAttr("actor", a.UserID),
			idAttr("team", teamID), idAttr("user", userID), slog.String("role", string(role)))
		return nil
	})
}
