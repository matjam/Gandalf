package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErasurePlan describes what DeleteUser did, or with dryRun would do. It
// holds ids and counts only, never anything personal.
type ErasurePlan struct {
	UserID string

	// DryRun is true when nothing was changed.
	DryRun bool

	// AlreadyDeleted is true when the user had been erased before; the rest
	// of the plan is then empty. DeleteUser is idempotent so that a retry
	// after a crash between the database change and archiving the vault
	// directory still hands back PersonalVaultID.
	AlreadyDeleted bool

	// KeysRevoked lists the ids of keys revoked (keys already revoked are
	// not repeated).
	KeysRevoked []string

	// MembershipsRemoved lists every team membership removed, including those
	// in archived teams.
	MembershipsRemoved []Membership

	// EmailCleared is true when a stored email was discarded.
	EmailCleared bool

	// RemoteCleared is true when the personal vault's git remote (URL and
	// deploy key) was deleted.
	RemoteCleared bool

	// AnonymisedHandle is what the handle becomes. It is derived from the
	// opaque id, so it identifies no one.
	AnonymisedHandle string

	// PersonalVaultID names the user's personal vault, for the caller to
	// archive. It is the user id.
	PersonalVaultID string
}

// DeleteUser erases a user: revokes all their keys, discards their email and
// git remote, replaces the handle with "deleted-<id suffix>", removes their
// memberships, clears any global-admin flag and stamps the account deleted.
// The row itself stays (anonymised) so ids referenced elsewhere, such as a
// team vault's commit history by user id, stay meaningful without naming
// anyone.
//
// Global administrators only. Refused with ErrLastGlobalAdmin if it would
// remove the last global administrator, in a dry run as well, so a dry run
// predicts the real outcome. A team left without an administrator by this is
// not an error: erasure must not be blockable by team arrangements, and a
// global administrator can name a new team admin.
//
// With dryRun true nothing is written. The caller archives the personal
// vault directory named in the plan; this package does not touch vaults.
func (s *Store) DeleteUser(ctx context.Context, actor Principal, id string, dryRun bool) (ErasurePlan, error) {
	var plan ErasurePlan
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		u, enc, err := s.userByID(ctx, tx, id)
		if err != nil {
			return err
		}
		plan = ErasurePlan{UserID: id, DryRun: dryRun, PersonalVaultID: id, AnonymisedHandle: u.Handle}
		if u.IsDeleted() {
			plan.AlreadyDeleted = true
			return nil
		}

		if u.GlobalAdmin {
			if err := s.checkNotLastAdmin(ctx, tx); err != nil {
				return err
			}
		}

		if plan.KeysRevoked, err = liveKeyIDs(ctx, tx, id); err != nil {
			return err
		}
		if plan.MembershipsRemoved, err = s.memberships(ctx, tx, id, false); err != nil {
			return err
		}
		plan.EmailCleared = len(enc) > 0
		if plan.RemoteCleared, err = remoteExists(ctx, tx, id); err != nil {
			return err
		}
		if plan.AnonymisedHandle, err = anonymisedHandle(ctx, tx, id); err != nil {
			return err
		}
		if dryRun {
			return nil
		}

		now := unix(s.now())
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE keys SET revoked = ? WHERE user_id = ? AND revoked IS NULL`, []any{now, id}},
			{`DELETE FROM memberships WHERE user_id = ?`, []any{id}},
			{`DELETE FROM vault_remotes WHERE vault_id = ?`, []any{id}},
			{`UPDATE users SET email_enc = NULL, handle = ?, global_admin = 0, deleted = ? WHERE id = ?`,
				[]any{plan.AnonymisedHandle, now, id}},
		} {
			if _, err := tx.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
				return fmt.Errorf("erase user: %w", err)
			}
		}
		s.log.InfoContext(ctx, "accounts: user erased", idAttr("actor", a.UserID), idAttr("user", id))
		return nil
	})
	if err != nil {
		return ErasurePlan{}, err
	}
	return plan, nil
}

// liveKeyIDs lists a user's keys that are not yet revoked (expired ones
// included: revoking them is harmless and leaves a uniform record).
func liveKeyIDs(ctx context.Context, q querier, userID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id FROM keys WHERE user_id = ? AND revoked IS NULL ORDER BY created, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list keys to revoke: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list keys to revoke: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// anonymisedHandle derives "deleted-" plus the first six characters of the
// id's random suffix, lengthening only if another erased user already holds
// that handle (a 1-in-a-billion event, but a unique-constraint failure in an
// erasure path is not acceptable).
func anonymisedHandle(ctx context.Context, q querier, id string) (string, error) {
	suffix := strings.TrimPrefix(id, userIDPrefix)
	for n := min(6, len(suffix)); n <= len(suffix); n++ {
		h := deletedHandlePrefix + suffix[:n]
		err := handleFree(ctx, q, h)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, ErrHandleTaken) {
			return "", err
		}
	}
	return "", fmt.Errorf("anonymise handle: no free handle derived from id")
}
