package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
)

// User is a person's account record.
//
// Email is populated only by methods that have authorised the caller to see
// it (the user themselves, or a global administrator); everywhere else it is
// empty and HasEmail says whether one is stored. This is the one place in the
// package where a decrypted email can sit in memory, so the type refuses to
// print it: see String and LogValue.
type User struct {
	ID          string
	Handle      string // Personal.
	Email       string // Personal. Empty unless the caller was authorised to read it.
	HasEmail    bool
	GlobalAdmin bool
	Created     time.Time
	Deleted     time.Time // Zero unless the user was erased; Handle is then anonymised.
}

// IsDeleted reports whether the account has been erased.
func (u User) IsDeleted() bool { return !u.Deleted.IsZero() }

// String and GoString print the id only, so %v, %+v and %#v cannot leak the
// handle or email into a log line.
func (u User) String() string   { return "User{" + u.ID + "}" }
func (u User) GoString() string { return u.String() }

// LogValue makes slog print the opaque id only.
func (u User) LogValue() slog.Value { return slog.GroupValue(slog.String("id", u.ID)) }

const userColumns = `id, handle, email_enc, global_admin, created, deleted`

type scanner interface{ Scan(dest ...any) error }

// scanUser reads a row of userColumns. It returns the stored ciphertext
// separately; whether to decrypt is the caller's authorisation decision.
func scanUser(r scanner) (User, []byte, error) {
	var (
		u       User
		enc     []byte
		admin   int
		created int64
		deleted *int64
	)
	if err := r.Scan(&u.ID, &u.Handle, &enc, &admin, &created, &deleted); err != nil {
		return User{}, nil, err
	}
	u.GlobalAdmin = admin == 1
	u.Created = fromUnix(created)
	u.Deleted = optTime(deleted)
	u.HasEmail = len(enc) > 0
	return u, enc, nil
}

// reveal decrypts a user's email into u. It is the only path by which a
// stored email becomes plaintext; every caller has already decided the
// reader is entitled to it.
func (s *Store) reveal(u User, enc []byte) (User, error) {
	if len(enc) == 0 {
		return u, nil
	}
	plain, err := s.open(emailAAD(u.ID), enc)
	if err != nil {
		return User{}, fmt.Errorf("read email of %s: %w", u.ID, err)
	}
	u.Email = string(plain)
	return u, nil
}

// liveUser loads a user who exists and has not been erased.
func (s *Store) liveUser(ctx context.Context, q querier, id string) (User, error) {
	u, _, err := s.userByID(ctx, q, id)
	if err != nil {
		return User{}, err
	}
	if u.IsDeleted() {
		return User{}, ErrNotFound
	}
	return u, nil
}

// userByID loads any user, erased or not, with the stored ciphertext.
func (s *Store) userByID(ctx context.Context, q querier, id string) (User, []byte, error) {
	u, enc, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, nil, ErrNotFound
	}
	if err != nil {
		return User{}, nil, fmt.Errorf("read user: %w", err)
	}
	return u, enc, nil
}

// validateEmail checks the shape of an address without echoing it.
func validateEmail(e string) error {
	if len(e) > maxEmailBytes {
		return fmt.Errorf("%w: email too long", ErrInvalidArgument)
	}
	// Bare addresses only: no display name, no comments, nothing that parses
	// as more than one thing. Errors from net/mail are dropped because they
	// quote the input.
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || strings.ContainsAny(e, " \t\r\n") {
		return fmt.Errorf("%w: not a valid email address", ErrInvalidArgument)
	}
	return nil
}

// sealEmail validates and encrypts an email for a user. An empty email yields
// nil (no email). A non-empty one without a configured key is refused
// before anything is written.
func (s *Store) sealEmail(userID, email string) ([]byte, error) {
	if email == "" {
		return nil, nil
	}
	if s.aead == nil {
		return nil, ErrNoSecretKey
	}
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	return s.seal(emailAAD(userID), []byte(email))
}

// handleFree returns ErrHandleTaken if a handle is in use. The check and the
// insert happen in one write transaction, so there is no race; the UNIQUE
// constraint remains as a backstop.
func handleFree(ctx context.Context, q querier, handle string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM users WHERE handle = ?`, handle).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("check handle: %w", err)
	}
	return ErrHandleTaken
}

// insertUser validates and stores a new user. Used by CreateUser and
// Bootstrap, which have done their own authorisation.
func (s *Store) insertUser(ctx context.Context, q querier, handle, email string, globalAdmin bool) (User, error) {
	if err := ValidateHandle(handle); err != nil {
		return User{}, err
	}
	id, err := newID(userIDPrefix)
	if err != nil {
		return User{}, err
	}
	enc, err := s.sealEmail(id, email)
	if err != nil {
		return User{}, err
	}
	if err := handleFree(ctx, q, handle); err != nil {
		return User{}, err
	}

	created := fromUnix(unix(s.now()))
	admin := 0
	if globalAdmin {
		admin = 1
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO users (id, handle, email_enc, global_admin, created) VALUES (?, ?, ?, ?, ?)`,
		id, handle, enc, admin, unix(created)); err != nil {
		return User{}, fmt.Errorf("store user: %w", err)
	}
	return User{
		ID: id, Handle: handle, Email: email, HasEmail: email != "",
		GlobalAdmin: globalAdmin, Created: created,
	}, nil
}

// CreateUser makes a user and their first key. Global administrators only.
// email may be empty; a non-empty one needs the secret key (ErrNoSecretKey).
// The returned User carries the email the administrator just supplied.
func (s *Store) CreateUser(ctx context.Context, actor Principal, handle, email string) (User, IssuedKey, error) {
	var (
		user   User
		issued IssuedKey
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		if user, err = s.insertUser(ctx, tx, handle, email, false); err != nil {
			return err
		}
		if issued, err = s.issueKey(ctx, tx, user.ID, "initial", 0); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "accounts: user created", idAttr("actor", a.UserID), idAttr("user", user.ID))
		return nil
	})
	if err != nil {
		return User{}, IssuedKey{}, err
	}
	return user, issued, nil
}

// GetUser returns a user's record, with their email decrypted. Allowed for
// the user themselves and for global administrators; anyone else gets
// ErrForbidden whether or not the id exists. An erased user is returned
// (anonymised) to administrators.
func (s *Store) GetUser(ctx context.Context, actor Principal, id string) (User, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return User{}, err
	}
	if !a.GlobalAdmin && a.UserID != id {
		return User{}, ErrForbidden
	}
	u, enc, err := s.userByID(ctx, s.db, id)
	if err != nil {
		return User{}, err
	}
	return s.reveal(u, enc)
}

// UserByHandle resolves a handle to a user. Allowed for global
// administrators, for team administrators (they need it to add people to
// their team, and the alternative is making them ask an administrator for
// every id), and for a user looking up themselves. Others get ErrForbidden
// before any lookup, so the call is not an oracle for which handles exist;
// the team-administrator audience is the accepted exception. The handle is
// matched case-insensitively. The email is decrypted only for administrators
// and for the user themselves.
func (s *Store) UserByHandle(ctx context.Context, actor Principal, handle string) (User, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return User{}, err
	}
	handle = strings.ToLower(strings.TrimSpace(handle))
	if !a.GlobalAdmin && !a.administersAny() && a.Handle != handle {
		return User{}, ErrForbidden
	}

	u, enc, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE handle = ? AND deleted IS NULL`, handle))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if a.GlobalAdmin || a.UserID == u.ID {
		return s.reveal(u, enc)
	}
	return u, nil
}

// ListUsers lists live users with their emails decrypted. Global
// administrators only. Erased users are omitted.
func (s *Store) ListUsers(ctx context.Context, actor Principal, page Page) ([]User, error) {
	if _, err := s.requireGlobalAdmin(ctx, s.db, actor); err != nil {
		return nil, err
	}
	page = page.norm()

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE deleted IS NULL
		 ORDER BY created, id LIMIT ? OFFSET ?`, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, enc, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		if u, err = s.reveal(u, enc); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetEmail sets or (with "") clears a user's email. Allowed for the user
// themselves and for global administrators. Setting needs the secret key.
func (s *Store) SetEmail(ctx context.Context, actor Principal, userID, email string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, actor)
		if err != nil {
			return err
		}
		if !a.GlobalAdmin && a.UserID != userID {
			return ErrForbidden
		}
		if _, err := s.liveUser(ctx, tx, userID); err != nil {
			return err
		}
		enc, err := s.sealEmail(userID, email)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET email_enc = ? WHERE id = ?`, enc, userID); err != nil {
			return fmt.Errorf("store email: %w", err)
		}
		return nil
	})
}

// SetGlobalAdmin grants or revokes the global administrator flag. Global
// administrators only. Revoking the last one is refused (ErrLastGlobalAdmin).
func (s *Store) SetGlobalAdmin(ctx context.Context, actor Principal, userID string, admin bool) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		target, err := s.liveUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		if target.GlobalAdmin == admin {
			return nil
		}
		if !admin {
			if err := s.checkNotLastAdmin(ctx, tx); err != nil {
				return err
			}
		}
		v := 0
		if admin {
			v = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET global_admin = ? WHERE id = ?`, v, userID); err != nil {
			return fmt.Errorf("set global admin: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: global admin changed",
			idAttr("actor", a.UserID), idAttr("user", userID), slog.Bool("admin", admin))
		return nil
	})
}

// checkNotLastAdmin returns ErrLastGlobalAdmin if removing one global
// administrator would leave none. Call it only when about to remove one.
func (s *Store) checkNotLastAdmin(ctx context.Context, q querier) error {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE global_admin = 1 AND deleted IS NULL`).Scan(&n); err != nil {
		return fmt.Errorf("count global admins: %w", err)
	}
	if n <= 1 {
		return ErrLastGlobalAdmin
	}
	return nil
}
