package accounts

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
)

const sampleEmail = "alice.private@example.org"

// rawEmail reads the stored column exactly as it is on disk.
func rawEmail(t *testing.T, e *env, userID string) []byte {
	t.Helper()
	var enc []byte
	if err := e.s.db.QueryRow(`SELECT email_enc FROM users WHERE id = ?`, userID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestEmailEncryptedAtRest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.userWithEmail("alice", sampleEmail)

	enc := rawEmail(t, e, u.ID)
	if len(enc) == 0 {
		t.Fatal("no email stored")
	}
	if bytes.Contains(enc, []byte(sampleEmail)) || bytes.Contains(enc, []byte("example.org")) {
		t.Errorf("email column holds the plaintext")
	}

	// Nowhere in the database's files, WAL included, may the address appear.
	if _, err := e.s.db.Exec(`PRAGMA wal_checkpoint(FULL)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{e.path, e.path + "-wal"} {
		content, err := readFile(f)
		if err != nil {
			continue
		}
		if bytes.Contains(content, []byte(sampleEmail)) {
			t.Errorf("plaintext email found in %s", f)
		}
	}

	// Every column of the row, not just the email column.
	rows, err := e.s.db.Query(`SELECT id, handle, email_enc, global_admin, created, deleted FROM users WHERE id = ?`, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, handle string
			enc        []byte
			admin      int
			created    int64
			deleted    sql.NullInt64
		)
		if err := rows.Scan(&id, &handle, &enc, &admin, &created, &deleted); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(id+handle, "example.org") {
			t.Errorf("email leaked into another column")
		}
	}

	got, err := e.s.GetUser(ctx, p, u.ID)
	wantErr(t, err, nil)
	if got.Email != sampleEmail || !got.HasEmail {
		t.Errorf("owner reads %q (HasEmail %v), want the address", got.Email, got.HasEmail)
	}
}

func TestEmailNonceIsRandomPerValue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.userWithEmail("alice", sampleEmail)
	before := rawEmail(t, e, u.ID)

	wantErr(t, e.s.SetEmail(ctx, p, u.ID, sampleEmail), nil)
	after := rawEmail(t, e, u.ID)
	if bytes.Equal(before, after) {
		t.Errorf("same email encrypted twice gave identical ciphertext")
	}
	if got, _ := e.s.GetUser(ctx, p, u.ID); got.Email != sampleEmail {
		t.Errorf("email = %q after re-encrypt", got.Email)
	}
}

func TestEmailRefusedWithoutSecretKey(t *testing.T) {
	// A store with no key can bootstrap without an email, but never holds one.
	s, _ := newBareEnv(t)
	ctx := context.Background()
	_, _, k, err := s.Bootstrap(ctx, "root-admin", "", "ops", "Ops")
	wantErr(t, err, nil)
	e := &env{t: t, s: s}
	admin, err := s.Authenticate(ctx, k.Plaintext)
	wantErr(t, err, nil)

	tests := []struct {
		name string
		do   func() error
	}{
		{"CreateUser with email", func() error {
			_, _, err := s.CreateUser(ctx, admin, "alice", sampleEmail)
			return err
		}},
		{"SetEmail", func() error { return s.SetEmail(ctx, admin, admin.UserID, sampleEmail) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr(t, tt.do(), ErrNoSecretKey)
		})
	}

	if n := e.rawCount(`SELECT COUNT(*) FROM users WHERE handle = 'alice'`); n != 0 {
		t.Errorf("a user was created despite the refusal")
	}
	if enc := rawEmail(t, e, admin.UserID); len(enc) != 0 {
		t.Errorf("something was stored: %x", enc)
	}

	// Without a key, users without emails work, and clearing is allowed.
	if _, _, err := s.CreateUser(ctx, admin, "bob", ""); err != nil {
		t.Errorf("CreateUser without email: %v", err)
	}
	wantErr(t, s.SetEmail(ctx, admin, admin.UserID, ""), nil)
}

func TestEmailValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("alice")

	tests := []struct {
		name  string
		email string
		ok    bool
	}{
		{"plain", "a@example.com", true},
		{"plus tag", "a+tag@example.com", true},
		{"empty clears", "", true},
		{"missing at", "example.com", false},
		{"display name", "Alice <a@example.com>", false},
		{"with space", "a b@example.com", false},
		{"two addresses", "a@example.com, b@example.com", false},
		{"too long", strings.Repeat("a", 250) + "@example.com", false},
		{"newline", "a@example.com\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.s.SetEmail(ctx, p, u.ID, tt.email)
			if tt.ok {
				wantErr(t, err, nil)
				return
			}
			wantErr(t, err, ErrInvalidArgument)
			if tt.email != "" && strings.Contains(err.Error(), tt.email) {
				t.Errorf("error %q echoes the email", err)
			}
		})
	}
}

func TestEmailVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	team := e.newTeam("people")
	owner := e.teamMember("owner", team, RoleMember)
	wantErr(t, e.s.SetEmail(ctx, owner.P, owner.User.ID, sampleEmail), nil)
	teamAdmin := e.teamMember("team-admin", team, RoleAdmin)
	peer := e.teamMember("peer", team, RoleMember)
	outsider, outsiderP := e.user("outsider")
	_ = outsider

	tests := []struct {
		name      string
		actor     Principal
		wantEmail bool
		wantErr   error
	}{
		{"owner", owner.P, true, nil},
		{"global admin", e.adminP, true, nil},
		{"team admin of a shared team", teamAdmin.P, false, ErrForbidden},
		{"team peer", peer.P, false, ErrForbidden},
		{"outsider", outsiderP, false, ErrForbidden},
		{"zero principal", Principal{}, false, ErrForbidden},
	}
	for _, tt := range tests {
		t.Run("GetUser/"+tt.name, func(t *testing.T) {
			u, err := e.s.GetUser(ctx, tt.actor, owner.User.ID)
			wantErr(t, err, tt.wantErr)
			if (u.Email != "") != tt.wantEmail {
				t.Errorf("email visible = %v, want %v", u.Email != "", tt.wantEmail)
			}
			if tt.wantErr != nil && u != (User{}) {
				t.Errorf("a refused call returned data: %v", u)
			}
		})
	}

	// A team administrator can resolve a handle (needed to add people) but
	// never sees the email.
	u, err := e.s.UserByHandle(ctx, teamAdmin.P, "owner")
	wantErr(t, err, nil)
	if u.Email != "" || !u.HasEmail {
		t.Errorf("team admin lookup: Email %q HasEmail %v; want hidden but known to exist", u.Email, u.HasEmail)
	}
	u, err = e.s.UserByHandle(ctx, owner.P, "owner")
	wantErr(t, err, nil)
	if u.Email != sampleEmail {
		t.Errorf("owner lookup of self lacks email")
	}
	u, err = e.s.UserByHandle(ctx, e.adminP, "OWNER")
	wantErr(t, err, nil)
	if u.Email != sampleEmail {
		t.Errorf("admin lookup (case-insensitive) lacks email")
	}

	// ListUsers decrypts for the global admin only.
	list, err := e.s.ListUsers(ctx, e.adminP, Page{})
	wantErr(t, err, nil)
	var found bool
	for _, lu := range list {
		if lu.ID == owner.User.ID {
			found = lu.Email == sampleEmail
		}
	}
	if !found {
		t.Errorf("ListUsers did not return the decrypted email")
	}
	if _, err := e.s.ListUsers(ctx, teamAdmin.P, Page{}); !isForbidden(err) {
		t.Errorf("team admin ListUsers = %v", err)
	}
}

func TestEmailClearAndReplace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.userWithEmail("alice", sampleEmail)

	wantErr(t, e.s.SetEmail(ctx, p, u.ID, ""), nil)
	if enc := rawEmail(t, e, u.ID); len(enc) != 0 {
		t.Errorf("email column not cleared: %x", enc)
	}
	got, _ := e.s.GetUser(ctx, p, u.ID)
	if got.HasEmail || got.Email != "" {
		t.Errorf("after clear: %+v", got)
	}

	wantErr(t, e.s.SetEmail(ctx, e.adminP, u.ID, "new@example.net"), nil)
	got, _ = e.s.GetUser(ctx, p, u.ID)
	if got.Email != "new@example.net" {
		t.Errorf("admin set email, owner reads %q", got.Email)
	}

	// Someone else's email is theirs to set, not a peer's.
	_, otherP := e.user("other")
	wantErr(t, e.s.SetEmail(ctx, otherP, u.ID, "x@example.net"), ErrForbidden)
}

func TestEmailTamperingDetected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, aP := e.userWithEmail("alice", sampleEmail)
	b, bP := e.userWithEmail("bobby", "bob@example.net")

	// Copying one user's ciphertext onto another must not yield a readable
	// address: the user id is authenticated data.
	if _, err := e.s.db.Exec(`UPDATE users SET email_enc = (SELECT email_enc FROM users WHERE id = ?) WHERE id = ?`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.GetUser(ctx, bP, b.ID); !isErr(err, ErrDecrypt) || got.Email != "" {
		t.Errorf("swapped ciphertext: %v, %q; want ErrDecrypt and no data", err, got.Email)
	}
	if _, err := e.s.GetUser(ctx, aP, a.ID); err != nil {
		t.Errorf("untouched row broke: %v", err)
	}

	// A flipped bit is caught too.
	enc := rawEmail(t, e, a.ID)
	enc[len(enc)-1] ^= 1
	if _, err := e.s.db.Exec(`UPDATE users SET email_enc = ? WHERE id = ?`, enc, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetUser(ctx, aP, a.ID); !isErr(err, ErrDecrypt) {
		t.Errorf("flipped bit: %v, want ErrDecrypt", err)
	}
}

func TestEmailWrongOrMissingKeyAtRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.userWithEmail("alice", sampleEmail)
	e.s.Close()

	tests := []struct {
		name string
		opts []Option
		want error
	}{
		{"wrong key", []Option{WithSecretKey(testKey(99))}, ErrDecrypt},
		{"no key", nil, ErrNoSecretKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(ctx, e.path, tt.opts...)
			wantErr(t, err, nil)
			defer s.Close()
			admin, err := s.Authenticate(ctx, e.adminKey.Plaintext)
			wantErr(t, err, nil)

			// A misconfigured key is an error, not an empty email: silently
			// showing no address would hide the problem.
			got, err := s.GetUser(ctx, admin, u.ID)
			wantErr(t, err, tt.want)
			if got.Email != "" {
				t.Errorf("returned an email: %q", got.Email)
			}
			if strings.Contains(err.Error(), sampleEmail) {
				t.Errorf("error echoes the email")
			}
		})
	}
}
