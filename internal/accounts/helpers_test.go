package accounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testKey is a fixed secret key so tests are deterministic about what is
// encrypted with what.
func testKey(b byte) []byte {
	k := make([]byte, secretKeyBytes)
	for i := range k {
		k[i] = b + byte(i)
	}
	return k
}

// clock is a settable time source injected into the store.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// env is a store with a controllable clock and a bootstrapped administrator.
type env struct {
	t     *testing.T
	s     *Store
	clock *clock
	path  string

	admin    User
	adminKey IssuedKey
	adminP   Principal
	team     Team // The team created by bootstrap; admin administers it.
}

// newEnv opens a store with the secret key configured and bootstraps it.
func newEnv(t *testing.T) *env { return newEnvOpts(t, WithSecretKey(testKey(1))) }

func newEnvOpts(t *testing.T, opts ...Option) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "gandalf.db")
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}

	s, err := Open(context.Background(), path, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.now = c.now
	t.Cleanup(func() { s.Close() })

	e := &env{t: t, s: s, clock: c, path: path}
	e.admin, e.team, e.adminKey, err = s.Bootstrap(context.Background(), "root-admin", "root@example.com", "ops", "Operations")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	e.adminP = e.auth(e.adminKey.Plaintext)
	return e
}

// newBareEnv opens a store with no bootstrap.
func newBareEnv(t *testing.T, opts ...Option) (*Store, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "gandalf.db"), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.now = c.now
	t.Cleanup(func() { s.Close() })
	return s, c
}

func (e *env) auth(plaintext string) Principal {
	e.t.Helper()
	p, err := e.s.Authenticate(context.Background(), plaintext)
	if err != nil {
		e.t.Fatalf("Authenticate: %v", err)
	}
	return p
}

// user creates a plain user as the administrator and returns it with a
// freshly authenticated principal.
func (e *env) user(handle string) (User, Principal) {
	e.t.Helper()
	u, k, err := e.s.CreateUser(context.Background(), e.adminP, handle, "")
	if err != nil {
		e.t.Fatalf("CreateUser(%s): %v", handle, err)
	}
	return u, e.auth(k.Plaintext)
}

// userWithEmail is user with an email address.
func (e *env) userWithEmail(handle, email string) (User, Principal) {
	e.t.Helper()
	u, k, err := e.s.CreateUser(context.Background(), e.adminP, handle, email)
	if err != nil {
		e.t.Fatalf("CreateUser(%s): %v", handle, err)
	}
	return u, e.auth(k.Plaintext)
}

// newTeam creates a team as the administrator.
func (e *env) newTeam(slug string) Team {
	e.t.Helper()
	tm, err := e.s.CreateTeam(context.Background(), e.adminP, slug, strings.ToUpper(slug))
	if err != nil {
		e.t.Fatalf("CreateTeam(%s): %v", slug, err)
	}
	return tm
}

// add adds a user to a team as the administrator.
func (e *env) add(team Team, u User, role Role) {
	e.t.Helper()
	if err := e.s.AddMember(context.Background(), e.adminP, team.ID, u.ID, role); err != nil {
		e.t.Fatalf("AddMember: %v", err)
	}
}

// member is a user with their key plaintext and current principal.
type member struct {
	User  User
	Key   string
	P     Principal
	Team  Team
	Reset func() Principal
}

// teamMember creates a user, adds them to team with role, and returns them
// with a principal that reflects the membership.
func (e *env) teamMember(handle string, team Team, role Role) member {
	e.t.Helper()
	u, k, err := e.s.CreateUser(context.Background(), e.adminP, handle, "")
	if err != nil {
		e.t.Fatalf("CreateUser(%s): %v", handle, err)
	}
	e.add(team, u, role)
	m := member{User: u, Key: k.Plaintext, Team: team}
	m.P = e.auth(k.Plaintext)
	m.Reset = func() Principal { return e.auth(k.Plaintext) }
	return m
}

// rawCount runs a COUNT query.
func (e *env) rawCount(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.db.QueryRow(query, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// dump renders every table's full contents, ordered, as one string, so a test
// can assert that an operation changed nothing.
func (e *env) dump() string {
	e.t.Helper()
	var b strings.Builder
	for _, table := range []string{"users", "teams", "memberships", "keys", "vault_remotes"} {
		rows, err := e.s.db.Query(`SELECT * FROM ` + table + ` ORDER BY 1, 2`)
		if err != nil {
			e.t.Fatalf("dump %s: %v", table, err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&b, "[%s]\n", table)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				e.t.Fatalf("dump %s: %v", table, err)
			}
			fmt.Fprintf(&b, "%v\n", vals)
		}
		rows.Close()
	}
	return b.String()
}

// wantErr fails unless err matches target (nil target means no error).
func wantErr(t *testing.T, err, target error) {
	t.Helper()
	switch {
	case target == nil && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case target != nil && !errors.Is(err, target):
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func isInvalidKey(err error) bool { return errors.Is(err, ErrInvalidKey) }
func isForbidden(err error) bool  { return errors.Is(err, ErrForbidden) }
func isNotFound(err error) bool   { return errors.Is(err, ErrNotFound) }
