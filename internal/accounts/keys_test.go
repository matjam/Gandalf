package accounts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestKeyFormatAndStorage(t *testing.T) {
	e := newEnv(t)
	k := e.adminKey

	if !strings.HasPrefix(k.Plaintext, "gdf_") || len(k.Plaintext) != keyLen {
		t.Fatalf("key %q has the wrong shape (len %d, want %d)", k.Plaintext, len(k.Plaintext), keyLen)
	}
	if strings.ContainsAny(k.Plaintext, "=+/") {
		t.Errorf("key is not unpadded base64url")
	}

	// Only the hash is stored, and it is the hash of the full key.
	var stored []byte
	if err := e.s.db.QueryRow(`SELECT hash FROM keys WHERE id = ?`, k.Key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(k.Plaintext))
	if !bytes.Equal(stored, sum[:]) {
		t.Errorf("stored hash is not SHA-256 of the key")
	}
	raw, err := readFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	// WAL mode: the data may still be in the -wal file, so check both.
	walRaw, _ := readFile(e.path + "-wal")
	for name, content := range map[string][]byte{"db": raw, "wal": walRaw} {
		if bytes.Contains(content, []byte(k.Plaintext)) {
			t.Errorf("plaintext key found on disk (%s file)", name)
		}
	}

	// Two keys never collide.
	k2, err := e.s.IssueKey(context.Background(), e.adminP, e.admin.ID, "second", 0)
	wantErr(t, err, nil)
	if k2.Plaintext == k.Plaintext || k2.Key.ID == k.Key.ID {
		t.Errorf("two issued keys are not distinct")
	}
}

func TestKeyTTL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.user("ttl-user")

	tests := []struct {
		name    string
		ttl     time.Duration
		want    time.Duration
		wantErr error
	}{
		{"zero means default", 0, DefaultKeyTTL, nil},
		{"explicit", 48 * time.Hour, 48 * time.Hour, nil},
		{"negative refused", -time.Hour, 0, ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := e.s.IssueKey(ctx, e.adminP, u.ID, "label", tt.ttl)
			wantErr(t, err, tt.wantErr)
			if tt.wantErr != nil {
				return
			}
			if got := k.Key.Expires.Sub(k.Key.Created); got != tt.want {
				t.Errorf("lifetime = %v, want %v", got, tt.want)
			}
		})
	}
	if DefaultKeyTTL != 365*24*time.Hour {
		t.Errorf("DefaultKeyTTL = %v, want 365 days", DefaultKeyTTL)
	}
}

func TestAuthenticate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	u, k, err := e.s.CreateUser(ctx, e.adminP, "alice", "")
	wantErr(t, err, nil)

	revoked, err := e.s.IssueKey(ctx, e.adminP, u.ID, "to-revoke", 0)
	wantErr(t, err, nil)
	wantErr(t, e.s.RevokeKey(ctx, e.adminP, revoked.Key.ID), nil)

	short, err := e.s.IssueKey(ctx, e.adminP, u.ID, "short", time.Hour)
	wantErr(t, err, nil)

	erased, ek, err := e.s.CreateUser(ctx, e.adminP, "mallory", "")
	wantErr(t, err, nil)
	_, err = e.s.DeleteUser(ctx, e.adminP, erased.ID, false)
	wantErr(t, err, nil)

	// Time passes: only the long-lived key survives past two hours.
	e.clock.advance(2 * time.Hour)

	tests := []struct {
		name string
		key  string
		want error
	}{
		{"valid", k.Plaintext, nil},
		{"revoked", revoked.Plaintext, ErrInvalidKey},
		{"expired", short.Plaintext, ErrInvalidKey},
		{"owner erased", ek.Plaintext, ErrInvalidKey},
		{"empty", "", ErrInvalidKey},
		{"wrong prefix", "xxx_" + k.Plaintext[4:], ErrInvalidKey},
		{"truncated", k.Plaintext[:len(k.Plaintext)-1], ErrInvalidKey},
		{"extended", k.Plaintext + "A", ErrInvalidKey},
		{"unknown but well formed", KeyPrefix + strings.Repeat("A", 43), ErrInvalidKey},
		{"case changed", strings.ToUpper(k.Plaintext), ErrInvalidKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := e.s.Authenticate(ctx, tt.key)
			wantErr(t, err, tt.want)
			if tt.want != nil {
				// A refused key yields no identity at all, and one error
				// text for every reason.
				if p.UserID != "" || p.KeyID != "" {
					t.Errorf("refused key returned a principal: %v", p)
				}
				if err.Error() != ErrInvalidKey.Error() {
					t.Errorf("error text %q reveals more than the sentinel", err)
				}
				return
			}
			if p.UserID != u.ID || p.Handle != "alice" || p.KeyID != k.Key.ID || p.GlobalAdmin {
				t.Errorf("principal = %+v", p)
			}
			if !p.KeyExpires.Equal(k.Key.Expires) {
				t.Errorf("KeyExpires = %v, want %v", p.KeyExpires, k.Key.Expires)
			}
		})
	}
}

func TestAuthenticateRefusesErasedOwnerEvenWithLiveKey(t *testing.T) {
	// Erasure revokes keys, but Authenticate must not rely on that: a user
	// marked deleted is refused on its own account, for both Authenticate and
	// the per-call acting-user check.
	e := newEnv(t)
	ctx := context.Background()
	u, k, err := e.s.CreateUser(ctx, e.adminP, "ghost", "")
	wantErr(t, err, nil)
	p := e.auth(k.Plaintext)
	if _, err := e.s.db.Exec(`UPDATE users SET deleted = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.rawCount(`SELECT COUNT(*) FROM keys WHERE user_id = ? AND revoked IS NULL`, u.ID); n != 1 {
		t.Fatalf("test setup: want one live key, have %d", n)
	}
	if _, err := e.s.Authenticate(ctx, k.Plaintext); !isInvalidKey(err) {
		t.Errorf("Authenticate = %v, want ErrInvalidKey", err)
	}
	if _, err := e.s.ListKeys(ctx, p, u.ID); !isForbidden(err) {
		t.Errorf("acting as erased user = %v, want ErrForbidden", err)
	}
}

func TestAuthenticateExpiryBoundary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.user("boundary")
	k, err := e.s.IssueKey(ctx, e.adminP, u.ID, "b", time.Hour)
	wantErr(t, err, nil)

	e.clock.advance(time.Hour - time.Second)
	if _, err := e.s.Authenticate(ctx, k.Plaintext); err != nil {
		t.Errorf("one second before expiry: %v", err)
	}
	e.clock.advance(time.Second)
	if _, err := e.s.Authenticate(ctx, k.Plaintext); !isInvalidKey(err) {
		t.Errorf("at expiry: %v, want ErrInvalidKey", err)
	}
}

func TestAuthenticateLoadsFreshState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	team := e.newTeam("fresh")
	m := e.teamMember("fresh-user", team, RoleMember)

	if role, ok := m.P.RoleIn(team.ID); !ok || role != RoleMember {
		t.Fatalf("role = %q, %v", role, ok)
	}

	wantErr(t, e.s.SetTeamRole(ctx, e.adminP, team.ID, m.User.ID, RoleAdmin), nil)
	p := m.Reset()
	if !p.IsTeamAdmin(team.ID) || !p.CanManageTeam(team.ID) {
		t.Errorf("promotion not visible on next authentication: %+v", p)
	}

	wantErr(t, e.s.RemoveMember(ctx, e.adminP, team.ID, m.User.ID), nil)
	if p := m.Reset(); p.IsMemberOf(team.ID) || len(p.Memberships) != 0 {
		t.Errorf("removal not visible on next authentication: %+v", p)
	}
}

func TestArchivedTeamsDropFromPrincipal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	team := e.newTeam("shelved")
	m := e.teamMember("shelf-user", team, RoleAdmin)
	if !m.P.IsMemberOf(team.ID) {
		t.Fatal("not a member before archive")
	}
	wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, team.ID), nil)

	p := m.Reset()
	if p.IsMemberOf(team.ID) {
		t.Errorf("archived team still in memberships: %+v", p.Memberships)
	}
	// And the stale pre-archive principal gains nothing from its old claim.
	err := e.s.AddMember(ctx, m.P, team.ID, e.admin.ID, RoleMember)
	wantErr(t, err, ErrForbidden)
}

func TestListKeys(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("lister")
	second, err := e.s.IssueKey(ctx, e.adminP, u.ID, "laptop", 0)
	wantErr(t, err, nil)
	wantErr(t, e.s.RevokeKey(ctx, e.adminP, second.Key.ID), nil)
	_, otherP := e.user("someone-else")

	keys, err := e.s.ListKeys(ctx, p, u.ID)
	wantErr(t, err, nil)
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	byLabel := map[string]Key{}
	for _, k := range keys {
		byLabel[k.Label] = k
	}
	if k := byLabel["initial"]; !k.Active(e.clock.now()) {
		t.Errorf("initial key = %+v", k)
	}
	if k := byLabel["laptop"]; k.Revoked.IsZero() || k.Active(e.clock.now()) {
		t.Errorf("revoked key = %+v", k)
	}

	// The listing type has no field that could carry a hash or plaintext.
	for _, k := range keys {
		if strings.Contains(strings.Join([]string{k.ID, k.UserID, k.Label}, " "), KeyPrefix) {
			t.Errorf("listing contains key material: %+v", k)
		}
	}

	if _, err := e.s.ListKeys(ctx, e.adminP, u.ID); err != nil {
		t.Errorf("global admin listing another user's keys: %v", err)
	}
	if _, err := e.s.ListKeys(ctx, otherP, u.ID); !isForbidden(err) {
		t.Errorf("user listing another user's keys = %v, want ErrForbidden", err)
	}
}

func TestRevokeKeys(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, aliceKey, err := e.s.CreateUser(ctx, e.adminP, "alice", "")
	wantErr(t, err, nil)
	aliceP := e.auth(aliceKey.Plaintext)
	bob, bobP := e.user("bob")
	spare, err := e.s.IssueKey(ctx, e.adminP, alice.ID, "spare", 0)
	wantErr(t, err, nil)
	bobKeys, err := e.s.ListKeys(ctx, bobP, bob.ID)
	wantErr(t, err, nil)

	t.Run("own current key refused", func(t *testing.T) {
		err := e.s.RevokeOwnKey(ctx, aliceP, aliceKey.Key.ID)
		wantErr(t, err, ErrCurrentKey)
		if _, err := e.s.Authenticate(ctx, aliceKey.Plaintext); err != nil {
			t.Errorf("current key stopped working: %v", err)
		}
	})
	t.Run("someone else's key looks like a missing key", func(t *testing.T) {
		err := e.s.RevokeOwnKey(ctx, aliceP, bobKeys[0].ID)
		wantErr(t, err, ErrNotFound)
		missing := e.s.RevokeOwnKey(ctx, aliceP, "k_doesnotexist")
		wantErr(t, missing, ErrNotFound)
		if err.Error() != missing.Error() {
			t.Errorf("owned-by-other (%q) distinguishable from missing (%q)", err, missing)
		}
	})
	t.Run("own other key", func(t *testing.T) {
		wantErr(t, e.s.RevokeOwnKey(ctx, aliceP, spare.Key.ID), nil)
		if _, err := e.s.Authenticate(ctx, spare.Plaintext); !isInvalidKey(err) {
			t.Errorf("revoked key still authenticates: %v", err)
		}
		// Revoking again is harmless and keeps the first revocation time.
		keys, _ := e.s.ListKeys(ctx, aliceP, alice.ID)
		var first time.Time
		for _, k := range keys {
			if k.ID == spare.Key.ID {
				first = k.Revoked
			}
		}
		e.clock.advance(time.Hour)
		wantErr(t, e.s.RevokeOwnKey(ctx, aliceP, spare.Key.ID), nil)
		keys, _ = e.s.ListKeys(ctx, aliceP, alice.ID)
		for _, k := range keys {
			if k.ID == spare.Key.ID && !k.Revoked.Equal(first) {
				t.Errorf("revocation time changed: %v -> %v", first, k.Revoked)
			}
		}
	})
	t.Run("admin revokes any key including a current one", func(t *testing.T) {
		wantErr(t, e.s.RevokeKey(ctx, e.adminP, aliceKey.Key.ID), nil)
		if _, err := e.s.Authenticate(ctx, aliceKey.Plaintext); !isInvalidKey(err) {
			t.Errorf("revoked key still authenticates: %v", err)
		}
		wantErr(t, e.s.RevokeKey(ctx, e.adminP, "k_nosuchkey"), ErrNotFound)
	})
	t.Run("non-admin cannot use RevokeKey", func(t *testing.T) {
		wantErr(t, e.s.RevokeKey(ctx, bobP, bobKeys[0].ID), ErrForbidden)
	})
}

func TestIssueKeyValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.user("labelled")

	tests := []struct {
		name  string
		label string
		want  error
	}{
		{"ordinary", "work laptop", nil},
		{"empty", "", ErrInvalidArgument},
		{"blank", "   ", ErrInvalidArgument},
		{"too long", strings.Repeat("x", maxLabelRunes+1), ErrInvalidArgument},
		{"control character", "bad\nlabel", ErrInvalidArgument},
		{"max length", strings.Repeat("x", maxLabelRunes), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.s.IssueKey(ctx, e.adminP, u.ID, tt.label, 0)
			wantErr(t, err, tt.want)
		})
	}

	if _, err := e.s.IssueKey(ctx, e.adminP, "u_nosuchuser", "x", 0); !isNotFound(err) {
		t.Errorf("IssueKey for missing user = %v, want ErrNotFound", err)
	}
}
