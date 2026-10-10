package accounts

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Bootstrap creates the very first user, as a global administrator, together
// with a first team they administer and a first key. It exists because every
// other method needs an authenticated administrator, and none exists yet.
//
// It succeeds only while the users table is empty (ErrAlreadyBootstrapped
// otherwise; erased users still count, so erasing everyone cannot reopen it).
// It runs in one write transaction, so two racing calls cannot both win and a
// failure leaves nothing behind. Nothing authenticates the caller: the
// server must expose this only to the operator (for example as a command-line
// action on the host), never over the network.
//
// email may be empty; a non-empty one needs the secret key. The returned
// User carries the email that was supplied.
func (s *Store) Bootstrap(ctx context.Context, handle, email, teamSlug, teamName string) (User, Team, IssuedKey, error) {
	var (
		user   User
		team   Team
		issued IssuedKey
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if n > 0 {
			return ErrAlreadyBootstrapped
		}

		var err error
		if user, err = s.insertUser(ctx, tx, handle, email, true); err != nil {
			return err
		}
		if team, err = s.insertTeam(ctx, tx, teamSlug, teamName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memberships (team_id, user_id, role, added) VALUES (?, ?, ?, ?)`,
			team.ID, user.ID, string(RoleAdmin), unix(s.now())); err != nil {
			return fmt.Errorf("add first member: %w", err)
		}
		if issued, err = s.issueKey(ctx, tx, user.ID, "bootstrap", time.Duration(0)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return User{}, Team{}, IssuedKey{}, err
	}
	s.log.InfoContext(ctx, "accounts: bootstrapped", idAttr("user", user.ID), idAttr("team", team.ID))
	return user, team, issued, nil
}
