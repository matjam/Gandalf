// Package accounts is the identity and authorisation store for hosted,
// multi-user Gandalf: who the users are, which teams they belong to, and
// which API keys let them in.
//
// It owns one SQLite file and nothing else. It knows nothing about vaults,
// HTTP or MCP: the server authenticates a bearer key into a [Principal],
// passes that principal to the methods here, and maps the answers onto vault
// access. Keeping the package that small is what makes its privacy claims
// checkable: every place personal data can enter, leave or be logged is in
// this directory.
//
// # Privacy
//
// The data held, and its classification (also repeated beside each column in
// the schema):
//
//   - Email: Personal. Optional. Encrypted at rest (AES-256-GCM) and only ever
//     decrypted by methods that return it to the user it belongs to or to a
//     global administrator. Without a secret key, storing one is refused.
//   - Handle: Personal, because it is a name the person chose.
//   - Key label: Personal (free text typed by a person, which can say anything).
//   - Key hash: Credential. Only the SHA-256 of a key is stored.
//   - Ids, roles, flags, timestamps, team slugs and names: Internal.
//
// Nothing here logs email, handle, label or key material; see [WithLogger].
// Nothing records when or from where a key was used: that would be
// behavioural data about a person with no requirement behind it.
package accounts

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Roles within a team.
type Role string

const (
	// RoleAdmin may manage the team's membership.
	RoleAdmin Role = "admin"
	// RoleMember may use the team's vault.
	RoleMember Role = "member"
)

// valid reports whether r is a role this package knows.
func (r Role) valid() bool { return r == RoleAdmin || r == RoleMember }

const (
	// KeyPrefix starts every API key, so a leaked key is recognisable by
	// secret scanners and a human, and so the server can reject an obviously
	// malformed bearer token without a database round trip.
	KeyPrefix = "gdf_"

	// DefaultKeyTTL is how long a key lives when the caller does not say.
	// Keys always expire: a credential that never lapses is one nobody
	// remembers to rotate.
	DefaultKeyTTL = 365 * 24 * time.Hour

	// keyEntropyBytes is the random content of a key. 256 bits, so the stored
	// hash cannot be attacked by guessing the secret, which is what makes the
	// plain indexed lookup in Authenticate safe.
	keyEntropyBytes = 32

	// secretKeyBytes is the AES-256 key size for email encryption.
	secretKeyBytes = 32

	maxLabelRunes = 64
	maxNameRunes  = 64
	maxEmailBytes = 254 // RFC 5321 path limit.

	// deletedHandlePrefix is what an erased user's handle becomes. Chosen
	// handles may not start with it, so an anonymised handle can never
	// collide with, or be impersonated by, a live one.
	deletedHandlePrefix = "deleted-"

	// Default and maximum page sizes for listings that can grow.
	DefaultPageSize = 100
	MaxPageSize     = 500

	// maxKeysListed bounds ListKeys. Keys are only issued by administrators,
	// so a user's list stays short, but an unbounded read is a bug waiting.
	maxKeysListed = 1000
)

// Sentinel errors. Callers match with errors.Is. Messages never contain
// handles, emails or key material, so wrapping and logging them is safe.
var (
	// ErrInvalidKey is the only thing Authenticate says about a bad key.
	// Unknown, malformed, revoked, expired and belonging-to-an-erased-user are
	// deliberately indistinguishable: telling a caller which one applies helps
	// only someone probing.
	ErrInvalidKey = errors.New("accounts: invalid key")

	// ErrForbidden means the acting principal may not do this. Authorisation
	// is deny-by-default: anything not explicitly allowed returns it.
	ErrForbidden = errors.New("accounts: forbidden")

	// ErrAlreadyBootstrapped is returned by Bootstrap once any user exists.
	ErrAlreadyBootstrapped = errors.New("accounts: already bootstrapped")

	// ErrNotFound means the user, team or key does not exist (or, for users,
	// has been erased).
	ErrNotFound = errors.New("accounts: not found")

	// ErrNoSecretKey means an operation needs the email encryption key and
	// none was configured. Storing an email in plaintext is never a fallback.
	ErrNoSecretKey = errors.New("accounts: no secret key configured; refusing to handle email")

	// ErrSecretKey means a supplied secret key is unusable.
	ErrSecretKey = errors.New("accounts: invalid secret key")

	// ErrDecrypt means a stored encrypted value did not decrypt: the wrong key, or a
	// damaged value.
	ErrDecrypt = errors.New("accounts: cannot decrypt stored value")

	ErrInvalidHandle   = errors.New("accounts: invalid handle")
	ErrInvalidSlug     = errors.New("accounts: invalid team slug")
	ErrInvalidArgument = errors.New("accounts: invalid argument")

	ErrHandleTaken = errors.New("accounts: handle already taken")
	ErrSlugTaken   = errors.New("accounts: team slug already taken")

	// ErrLastGlobalAdmin protects the system from losing every administrator.
	ErrLastGlobalAdmin = errors.New("accounts: would remove the last global administrator")

	// ErrLastTeamAdmin is returned to a team administrator who would leave
	// their team with nobody able to manage it. A global administrator may.
	ErrLastTeamAdmin = errors.New("accounts: would remove the last administrator of the team")

	// ErrCurrentKey is returned when a user tries to revoke the key making
	// the request, which would lock them out mid-session.
	ErrCurrentKey = errors.New("accounts: cannot revoke the key in use")

	ErrAlreadyMember = errors.New("accounts: already a member")
	ErrNotMember     = errors.New("accounts: not a member")
	ErrTeamArchived  = errors.New("accounts: team is archived")
)

// handlePattern is the shape of both user handles and team slugs: lowercase,
// starts with a letter, 2 to 32 characters. Lowercase only, so uniqueness
// needs no case folding and nothing looks alike apart from digits.
var handlePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// ValidateHandle reports whether s is acceptable as a user handle. The
// server may call it at the edge to fail early; the store enforces it anyway.
func ValidateHandle(s string) error {
	if !handlePattern.MatchString(s) || strings.HasPrefix(s, deletedHandlePrefix) {
		// The value is not echoed: a handle is Personal data and error
		// strings travel into logs.
		return fmt.Errorf("%w: must match %s and not start with %q",
			ErrInvalidHandle, handlePattern, deletedHandlePrefix)
	}
	return nil
}

// ValidateSlug reports whether s is acceptable as a team slug.
func ValidateSlug(s string) error {
	if !handlePattern.MatchString(s) {
		return fmt.Errorf("%w: must match %s", ErrInvalidSlug, handlePattern)
	}
	if slices.Contains(reservedSlugs, s) {
		return fmt.Errorf("%w: %q is reserved", ErrInvalidSlug, s)
	}
	return nil
}

// reservedSlugs are names the server gives meaning to when choosing a vault,
// so a team may not take them. "personal" addresses a user's own vault
// (vault: "personal"); a team with that slug would be ambiguous with it.
var reservedSlugs = []string{"personal"}

// validText checks free text typed by a person: key labels and team names.
func validText(what, s string, maxRunes int) error {
	n := utf8.RuneCountInString(s)
	switch {
	case !utf8.ValidString(s), strings.TrimSpace(s) == "", n > maxRunes:
		return fmt.Errorf("%w: %s must be 1 to %d characters", ErrInvalidArgument, what, maxRunes)
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: %s contains control characters", ErrInvalidArgument, what)
	}
	return nil
}

// newID returns prefix plus 128 random bits in lowercase base32 (26 chars).
// Ids are opaque on purpose: nothing about a user or team can be learned from
// one, and they are safe to log.
func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return prefix + strings.ToLower(enc.EncodeToString(b[:])), nil
}

// Time is stored as unix seconds: integer comparison in SQL, no timezone to
// get wrong, and second resolution is all expiry needs.
func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(v int64) time.Time { return time.Unix(v, 0).UTC() }

// optTime converts a nullable column to a time, zero meaning NULL.
func optTime(v *int64) time.Time {
	if v == nil {
		return time.Time{}
	}
	return fromUnix(*v)
}

// Page selects a window of a listing. The zero value is the first page of
// the default size.
type Page struct {
	Limit  int
	Offset int
}

// norm clamps a page to sane bounds.
func (p Page) norm() Page {
	if p.Limit <= 0 {
		p.Limit = DefaultPageSize
	}
	p.Limit = min(p.Limit, MaxPageSize)
	p.Offset = max(p.Offset, 0)
	return p
}

// idAttr is the one way this package puts a person-related record into a log:
// by opaque id.
func idAttr(key, id string) slog.Attr { return slog.String(key, id) }
