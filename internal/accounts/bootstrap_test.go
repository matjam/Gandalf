package accounts

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestBootstrap(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if !e.admin.GlobalAdmin {
		t.Error("bootstrap user is not a global admin")
	}
	if e.adminKey.Plaintext == "" || e.adminKey.Key.UserID != e.admin.ID {
		t.Errorf("bootstrap key = %v", e.adminKey)
	}
	if !e.adminP.GlobalAdmin || e.adminP.UserID != e.admin.ID {
		t.Errorf("bootstrap principal = %v", e.adminP)
	}
	if role, ok := e.adminP.RoleIn(e.team.ID); !ok || role != RoleAdmin {
		t.Errorf("bootstrap user role in first team = %q, %v; want admin", role, ok)
	}
	if got := e.adminP.Memberships; len(got) != 1 || got[0].Slug != "ops" {
		t.Errorf("memberships = %+v", got)
	}

	// The email supplied was stored (encrypted) and is readable by the owner.
	u, err := e.s.GetUser(ctx, e.adminP, e.admin.ID)
	if err != nil || u.Email != "root@example.com" {
		t.Errorf("GetUser = %v, %v", u, err)
	}

	// Only once.
	_, _, _, err = e.s.Bootstrap(ctx, "other", "", "second", "Second")
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Errorf("second Bootstrap = %v, want ErrAlreadyBootstrapped", err)
	}
	if n := e.rawCount(`SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("users after refused bootstrap = %d, want 1", n)
	}
}

func TestBootstrapStaysClosedAfterErasure(t *testing.T) {
	// Erasing every user must not reopen the unauthenticated bootstrap path.
	e := newEnv(t)
	ctx := context.Background()
	other, _ := e.user("second-admin")
	wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, other.ID, true), nil)

	otherKey, err := e.s.IssueKey(ctx, e.adminP, other.ID, "k", 0)
	wantErr(t, err, nil)
	otherP := e.auth(otherKey.Plaintext)

	if _, err := e.s.DeleteUser(ctx, otherP, e.admin.ID, false); err != nil {
		t.Fatal(err)
	}
	// The remaining admin is the last one; they cannot be erased, so create
	// the all-erased state directly.
	if _, err := e.s.db.Exec(`UPDATE users SET deleted = 1`); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = e.s.Bootstrap(ctx, "intruder", "", "evil", "Evil")
	if !errors.Is(err, ErrAlreadyBootstrapped) {
		t.Errorf("Bootstrap after erasing everyone = %v, want ErrAlreadyBootstrapped", err)
	}
}

func TestBootstrapIsAtomic(t *testing.T) {
	tests := []struct {
		name                       string
		handle, email, slug, tname string
		secret                     bool
		want                       error
	}{
		{"bad handle", "Bad Handle", "", "ops", "Ops", true, ErrInvalidHandle},
		{"bad slug", "boss", "", "Bad Slug", "Ops", true, ErrInvalidSlug},
		{"reserved slug", "boss", "", "personal", "Ops", true, ErrInvalidSlug},
		{"bad team name", "boss", "", "ops", "", true, ErrInvalidArgument},
		{"bad email", "boss", "not-an-email", "ops", "Ops", true, ErrInvalidArgument},
		{"email without secret key", "boss", "boss@example.com", "ops", "Ops", false, ErrNoSecretKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.secret {
				opts = append(opts, WithSecretKey(testKey(1)))
			}
			s, _ := newBareEnv(t, opts...)
			_, _, _, err := s.Bootstrap(context.Background(), tt.handle, tt.email, tt.slug, tt.tname)
			wantErr(t, err, tt.want)

			// A failure part-way must leave nothing: no user, team or key,
			// or the next attempt would be refused as already bootstrapped.
			for _, table := range []string{"users", "teams", "memberships", "keys"} {
				var n int
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
					t.Errorf("%s has %d rows after failed bootstrap (%v)", table, n, err)
				}
			}
			if _, _, _, err := s.Bootstrap(context.Background(), "boss", "", "ops", "Ops"); err != nil {
				t.Errorf("Bootstrap after a failed attempt: %v", err)
			}
		})
	}
}

func TestBootstrapRace(t *testing.T) {
	s, _ := newBareEnv(t)
	ctx := context.Background()

	const n = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		errs []error
	)
	for i := range n {
		wg.Go(func() {
			handle := "boss-" + string(rune('a'+i))
			_, _, _, err := s.Bootstrap(ctx, handle, "", "team-"+string(rune('a'+i)), "Team")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				errs = append(errs, err)
			}
		})
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d bootstraps succeeded, want exactly 1 (errors: %v)", wins, errs)
	}
	for _, err := range errs {
		if !errors.Is(err, ErrAlreadyBootstrapped) {
			t.Errorf("losing bootstrap failed with %v, want ErrAlreadyBootstrapped", err)
		}
	}
}
