package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// keyLen is the exact length of a well-formed key: prefix plus 32 bytes in
// unpadded base64url (43 characters).
var keyLen = len(KeyPrefix) + base64.RawURLEncoding.EncodedLen(keyEntropyBytes)

// Key is a key's record. The plaintext and the hash are deliberately absent:
// the plaintext exists only in an IssuedKey, once, and the hash never leaves
// the package.
type Key struct {
	ID      string
	UserID  string
	Label   string // Personal.
	Created time.Time
	Expires time.Time
	Revoked time.Time // Zero if not revoked.
}

// Active reports whether the key would authenticate at time now.
func (k Key) Active(now time.Time) bool {
	return k.Revoked.IsZero() && now.Before(k.Expires)
}

// IssuedKey is a newly minted key. Plaintext is shown to the person once and
// cannot be recovered: only its hash is stored.
type IssuedKey struct {
	Plaintext string // Credential.
	Key       Key
}

// String, GoString and LogValue redact the plaintext so that logging or
// printing an IssuedKey cannot leak it. Reading the Plaintext field is the
// only way to get it out, and has to be written down on purpose.
func (k IssuedKey) String() string   { return "IssuedKey{" + k.Key.ID + ", redacted}" }
func (k IssuedKey) GoString() string { return k.String() }
func (k IssuedKey) LogValue() slog.Value {
	return slog.GroupValue(slog.String("key", k.Key.ID))
}

// generateKey returns a new plaintext key and its stored hash.
func generateKey() (plaintext string, hash []byte, err error) {
	var raw [keyEntropyBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, fmt.Errorf("generate key: %w", err)
	}
	plaintext = KeyPrefix + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:], nil
}

// issueKey mints and stores a key for userID. The caller has authorised it
// and checked the user exists.
func (s *Store) issueKey(ctx context.Context, q querier, userID, label string, ttl time.Duration) (IssuedKey, error) {
	if err := validText("key label", label, maxLabelRunes); err != nil {
		return IssuedKey{}, err
	}
	if ttl < 0 {
		return IssuedKey{}, fmt.Errorf("%w: key lifetime is negative", ErrInvalidArgument)
	}
	if ttl == 0 {
		ttl = DefaultKeyTTL
	}

	id, err := newID(keyIDPrefix)
	if err != nil {
		return IssuedKey{}, err
	}
	plaintext, hash, err := generateKey()
	if err != nil {
		return IssuedKey{}, err
	}

	now := s.now()
	key := Key{ID: id, UserID: userID, Label: label, Created: fromUnix(unix(now)), Expires: fromUnix(unix(now.Add(ttl)))}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO keys (id, user_id, label, hash, created, expires) VALUES (?, ?, ?, ?, ?, ?)`,
		key.ID, key.UserID, key.Label, hash, unix(key.Created), unix(key.Expires)); err != nil {
		return IssuedKey{}, fmt.Errorf("store key: %w", err)
	}
	return IssuedKey{Plaintext: plaintext, Key: key}, nil
}

// IssueKey mints a key for a user. Global administrators only: users do not
// mint their own, so an expired key is replaced by a person who can vouch
// for the request. ttl 0 means DefaultKeyTTL; keys always expire.
func (s *Store) IssueKey(ctx context.Context, actor Principal, userID, label string, ttl time.Duration) (IssuedKey, error) {
	var issued IssuedKey
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		if _, err := s.liveUser(ctx, tx, userID); err != nil {
			return err
		}
		issued, err = s.issueKey(ctx, tx, userID, label, ttl)
		if err != nil {
			return err
		}
		s.log.InfoContext(ctx, "accounts: key issued",
			idAttr("actor", a.UserID), idAttr("user", userID), idAttr("key", issued.Key.ID))
		return nil
	})
	if err != nil {
		return IssuedKey{}, err
	}
	return issued, nil
}

// ListKeys returns a user's keys, revoked and expired ones included so the
// person can see what they have. Allowed for the user themselves and for
// global administrators. No hashes are returned.
func (s *Store) ListKeys(ctx context.Context, actor Principal, userID string) ([]Key, error) {
	a, err := s.actor(ctx, s.db, actor)
	if err != nil {
		return nil, err
	}
	if !a.GlobalAdmin && a.UserID != userID {
		return nil, ErrForbidden
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, label, created, expires, revoked
		 FROM keys WHERE user_id = ? ORDER BY created, id LIMIT ?`, userID, maxKeysListed)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer rows.Close()

	var out []Key
	for rows.Next() {
		var (
			k                Key
			created, expires int64
			revoked          *int64
		)
		if err := rows.Scan(&k.ID, &k.UserID, &k.Label, &created, &expires, &revoked); err != nil {
			return nil, fmt.Errorf("list keys: %w", err)
		}
		k.Created, k.Expires, k.Revoked = fromUnix(created), fromUnix(expires), optTime(revoked)
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeKey revokes any key. Global administrators only. Revoking an
// already-revoked key succeeds and keeps the original revocation time.
func (s *Store) RevokeKey(ctx context.Context, actor Principal, keyID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.requireGlobalAdmin(ctx, tx, actor)
		if err != nil {
			return err
		}
		if err := s.revoke(ctx, tx, `id = ?`, keyID); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "accounts: key revoked", idAttr("actor", a.UserID), idAttr("key", keyID))
		return nil
	})
}

// RevokeOwnKey lets a user revoke one of their own keys, except the one
// making this request (ErrCurrentKey): revoking it would end the session
// that asked, and a mistyped id should not lock someone out. The user and
// the current key come from the principal, not from arguments, so they
// cannot be forged by the caller.
//
// A key that does not exist and a key that belongs to someone else both
// return ErrNotFound, so the call cannot be used to probe for key ids.
func (s *Store) RevokeOwnKey(ctx context.Context, actor Principal, keyID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.actor(ctx, tx, actor)
		if err != nil {
			return err
		}
		if keyID == a.KeyID {
			return ErrCurrentKey
		}
		if err := s.revoke(ctx, tx, `id = ? AND user_id = ?`, keyID, a.UserID); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "accounts: key revoked", idAttr("actor", a.UserID), idAttr("key", keyID))
		return nil
	})
}

// revoke marks matching keys revoked. where selects the key (always by id).
// ErrNotFound if nothing matches the selector at all; already-revoked keys
// match and are left alone.
func (s *Store) revoke(ctx context.Context, q querier, where string, args ...any) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM keys WHERE `+where, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find key: %w", err)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE keys SET revoked = ? WHERE `+where+` AND revoked IS NULL`,
		append([]any{unix(s.now())}, args...)...); err != nil {
		return fmt.Errorf("revoke key: %w", err)
	}
	return nil
}
