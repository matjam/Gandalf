package accounts

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

// Remote is a vault's git remote as shown to the people who manage it: where
// it pushes to and the public half of the deploy key to install there. The
// private key is never part of it; see RemotePrivateKey.
type Remote struct {
	VaultID   string
	URL       string // Personal. Empty until set.
	PublicKey string // OpenSSH authorized_keys line.
	Created   time.Time
	Updated   time.Time
	SetBy     string // User id of whoever last changed it.
}

const maxRemoteURLBytes = 2048

// scpURL is the scp-like form git accepts, user@host:path. The user and host
// are restricted to plain characters, and the first character of each is
// not '-'.
var scpURL = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[^\s:][^\s]*$`)

// validateRemoteURL admits only SSH remotes: the server holds an SSH deploy
// key for the vault, so nothing else is useful, and git's other transports
// are a hazard when the URL is attacker-influenced (a "ext::" URL runs a
// command; a URL beginning with '-' is read by git or ssh as an option). The
// URL is Personal data and is not echoed in the error.
func validateRemoteURL(raw string) error {
	bad := func(why string) error {
		return fmt.Errorf("%w: remote URL %s", ErrInvalidArgument, why)
	}
	if raw == "" || len(raw) > maxRemoteURLBytes {
		return bad("must be 1 to 2048 bytes")
	}
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return bad("contains whitespace or control characters")
	}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") ||
			u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
			return bad("is not a valid ssh:// URL")
		}
		return nil
	}
	if scpURL.MatchString(raw) {
		return nil
	}
	return bad("must be ssh://host/path or user@host:path")
}

func remoteURLAAD(vaultID string) string { return "remote-url:" + vaultID }
func remoteKeyAAD(vaultID string) string { return "remote-key:" + vaultID }

// remoteExists reports whether a vault has a remote row.
func remoteExists(ctx context.Context, q querier, vaultID string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM vault_remotes WHERE vault_id = ?`, vaultID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read remote: %w", err)
	}
	return true, nil
}

// authoriseRemote checks that the actor may manage the vault's remote and
// that the vault exists. For mutations an archived team is refused.
func (s *Store) authoriseRemote(ctx context.Context, q querier, actor Principal, vaultID string, mutate bool) (Principal, error) {
	a, err := s.actor(ctx, q, actor)
	if err != nil {
		return Principal{}, err
	}
	if !a.CanManageRemote(vaultID) {
		return Principal{}, ErrForbidden
	}
	// A personal vault's owner is the acting user, who exists. A team vault
	// must be checked.
	if strings.HasPrefix(vaultID, teamIDPrefix) {
		t, err := s.teamByID(ctx, q, vaultID)
		if err != nil {
			return Principal{}, err
		}
		if mutate && t.IsArchived() {
			return Principal{}, ErrTeamArchived
		}
	}
	return a, nil
}

// getRemote reads and decrypts a vault's remote (not its private key).
func (s *Store) getRemote(ctx context.Context, q querier, vaultID string) (Remote, error) {
	var (
		r            Remote
		enc          []byte
		created, upd int64
	)
	err := q.QueryRowContext(ctx,
		`SELECT vault_id, url_enc, public_key, created, updated, set_by FROM vault_remotes WHERE vault_id = ?`,
		vaultID).Scan(&r.VaultID, &enc, &r.PublicKey, &created, &upd, &r.SetBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Remote{}, ErrNotFound
	}
	if err != nil {
		return Remote{}, fmt.Errorf("read remote: %w", err)
	}
	r.Created, r.Updated = fromUnix(created), fromUnix(upd)
	if len(enc) > 0 {
		plain, err := s.open(remoteURLAAD(vaultID), enc)
		if err != nil {
			return Remote{}, fmt.Errorf("read remote of %s: %w", vaultID, err)
		}
		r.URL = string(plain)
	}
	return r, nil
}

// GenerateRemoteKey creates a fresh ed25519 deploy key for a vault and stores
// it, private half encrypted. If the vault already has one it is replaced:
// that is rotation, and the previous key stops being usable the moment this
// returns, so the caller must install the new public key on the remote. The
// URL, if set, is kept. Needs the secret key (ErrNoSecretKey).
//
// Allowed per Principal.CanManageRemote. The result carries the public key to
// install; the private key can only be fetched with RemotePrivateKey.
func (s *Store) GenerateRemoteKey(ctx context.Context, actor Principal, vaultID string) (Remote, error) {
	var out Remote
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.authoriseRemote(ctx, tx, actor, vaultID, true)
		if err != nil {
			return err
		}
		if s.aead == nil {
			return ErrNoSecretKey
		}

		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate deploy key: %w", err)
		}
		comment := "gandalf-" + vaultID // Opaque id; shows which vault a key is for.
		block, err := ssh.MarshalPrivateKey(priv, comment)
		if err != nil {
			return fmt.Errorf("encode deploy key: %w", err)
		}
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			return fmt.Errorf("encode deploy key: %w", err)
		}
		publicLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment

		encKey, err := s.seal(remoteKeyAAD(vaultID), pem.EncodeToMemory(block))
		if err != nil {
			return err
		}
		now := unix(s.now())
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vault_remotes (vault_id, public_key, private_key_enc, created, updated, set_by)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT(vault_id) DO UPDATE SET
			     public_key = excluded.public_key,
			     private_key_enc = excluded.private_key_enc,
			     updated = excluded.updated,
			     set_by = excluded.set_by`,
			vaultID, publicLine, encKey, now, now, a.UserID); err != nil {
			return fmt.Errorf("store deploy key: %w", err)
		}
		s.log.InfoContext(ctx, "accounts: deploy key generated", idAttr("actor", a.UserID), idAttr("vault", vaultID))
		out, err = s.getRemote(ctx, tx, vaultID)
		return err
	})
	if err != nil {
		return Remote{}, err
	}
	return out, nil
}

// setRemoteURL stores or clears the URL of a vault that already has a key.
func (s *Store) setRemoteURL(ctx context.Context, actor Principal, vaultID, rawURL string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		a, err := s.authoriseRemote(ctx, tx, actor, vaultID, true)
		if err != nil {
			return err
		}
		var enc []byte
		if rawURL != "" {
			if err := validateRemoteURL(rawURL); err != nil {
				return err
			}
			if enc, err = s.seal(remoteURLAAD(vaultID), []byte(rawURL)); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE vault_remotes SET url_enc = ?, updated = ?, set_by = ? WHERE vault_id = ?`,
			enc, unix(s.now()), a.UserID, vaultID)
		if err != nil {
			return fmt.Errorf("store remote URL: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store remote URL: %w", err)
		} else if n == 0 {
			// No key yet: generate one first, so the vault never has a URL
			// the server cannot authenticate to.
			return ErrNotFound
		}
		s.log.InfoContext(ctx, "accounts: remote changed", idAttr("actor", a.UserID), idAttr("vault", vaultID))
		return nil
	})
}

// SetRemoteURL sets the vault's remote URL, which must be an SSH URL
// (ssh://host/path or user@host:path). The vault must already have a deploy
// key (ErrNotFound otherwise). The URL is Personal data and is stored
// encrypted, so this needs the secret key.
func (s *Store) SetRemoteURL(ctx context.Context, actor Principal, vaultID, rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%w: remote URL is empty; use ClearRemoteURL", ErrInvalidArgument)
	}
	return s.setRemoteURL(ctx, actor, vaultID, rawURL)
}

// ClearRemoteURL removes the vault's remote URL, keeping its deploy key.
func (s *Store) ClearRemoteURL(ctx context.Context, actor Principal, vaultID string) error {
	return s.setRemoteURL(ctx, actor, vaultID, "")
}

// GetRemote returns a vault's remote: URL and public key, never the private
// key. ErrNotFound if the vault has no deploy key.
func (s *Store) GetRemote(ctx context.Context, actor Principal, vaultID string) (Remote, error) {
	if _, err := s.authoriseRemote(ctx, s.db, actor, vaultID, false); err != nil {
		return Remote{}, err
	}
	return s.getRemote(ctx, s.db, vaultID)
}

// RemotePrivateKey returns the vault's decrypted deploy key as OpenSSH PEM
// bytes.
//
// This is for the server's git runner and nothing else, and so it takes no
// acting user: the runner pushes on the vault's behalf, not a person's. It
// must never be reachable from a tool call or any response. The caller should
// hand the bytes straight to the SSH client and let them go; they are
// credential material and must not be logged or written to disk in
// plaintext beyond what the SSH client strictly needs.
func (s *Store) RemotePrivateKey(ctx context.Context, vaultID string) ([]byte, error) {
	var enc []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT private_key_enc FROM vault_remotes WHERE vault_id = ?`, vaultID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read deploy key: %w", err)
	}
	key, err := s.open(remoteKeyAAD(vaultID), enc)
	if err != nil {
		return nil, fmt.Errorf("read deploy key of %s: %w", vaultID, err)
	}
	return key, nil
}
