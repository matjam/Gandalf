package accounts

import (
	"context"
	"sync"
	"testing"
)

func TestLastGlobalAdminProtected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	t.Run("cannot demote the only admin", func(t *testing.T) {
		wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, e.admin.ID, false), ErrLastGlobalAdmin)
	})
	t.Run("cannot delete the only admin, even as a dry run", func(t *testing.T) {
		before := e.dump()
		_, err := e.s.DeleteUser(ctx, e.adminP, e.admin.ID, true)
		wantErr(t, err, ErrLastGlobalAdmin)
		_, err = e.s.DeleteUser(ctx, e.adminP, e.admin.ID, false)
		wantErr(t, err, ErrLastGlobalAdmin)
		if e.dump() != before {
			t.Errorf("refused deletion changed the database")
		}
	})
	t.Run("with two admins either can step down, then the last is protected", func(t *testing.T) {
		second, _ := e.user("second-admin")
		wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, second.ID, true), nil)
		wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, second.ID, true), nil) // Idempotent.
		secondP := e.auth0(second.ID)

		// root demotes itself; second remains.
		wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, e.admin.ID, false), nil)

		// root is now an ordinary user: its (stale) principal is not an admin.
		wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, second.ID, false), ErrForbidden)

		// second is the last one and cannot step down or be erased.
		wantErr(t, e.s.SetGlobalAdmin(ctx, secondP, second.ID, false), ErrLastGlobalAdmin)
		_, err := e.s.DeleteUser(ctx, secondP, second.ID, false)
		wantErr(t, err, ErrLastGlobalAdmin)
	})
}

// auth0 authenticates a user by inserting a key for them directly,
// bypassing the authorisation under test (for example to act as a user who
// has just been made an administrator).
func (e *env) auth0(userID string) Principal {
	e.t.Helper()
	// Issue a key for the user directly in the database and authenticate it,
	// bypassing the authorisation under test.
	plaintext, hash, err := generateKey()
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := newID(keyIDPrefix)
	if _, err := e.s.db.Exec(
		`INSERT INTO keys (id, user_id, label, hash, created, expires) VALUES (?, ?, 'direct', ?, ?, ?)`,
		id, userID, hash, unix(e.clock.now()), unix(e.clock.now().Add(DefaultKeyTTL))); err != nil {
		e.t.Fatal(err)
	}
	return e.auth(plaintext)
}

func TestLastGlobalAdminRace(t *testing.T) {
	// Two administrators each try to demote the other at once. Exactly one
	// must succeed; zero administrators would lock everyone out.
	e := newEnv(t)
	ctx := context.Background()
	second, _ := e.user("second-admin")
	wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, second.ID, true), nil)
	secondP := e.auth0(second.ID)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for _, c := range []struct {
		actor  Principal
		target string
	}{{e.adminP, second.ID}, {secondP, e.admin.ID}} {
		wg.Go(func() {
			err := e.s.SetGlobalAdmin(ctx, c.actor, c.target, false)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			}
		})
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d demotions succeeded, want exactly 1", wins)
	}
	if n := e.rawCount(`SELECT COUNT(*) FROM users WHERE global_admin = 1 AND deleted IS NULL`); n != 1 {
		t.Errorf("%d global admins remain, want 1", n)
	}
}

func TestLastTeamAdminProtected(t *testing.T) {
	ctx := context.Background()

	// Fresh team per case: alpha has exactly one admin (solo) and one member.
	setup := func(t *testing.T) (*env, Team, member, member) {
		e := newEnv(t)
		team := e.newTeam("alpha")
		solo := e.teamMember("solo-admin", team, RoleAdmin)
		plain := e.teamMember("plain-member", team, RoleMember)
		return e, team, solo, plain
	}

	tests := []struct {
		name string
		do   func(e *env, team Team, solo, plain member) error
		want error
	}{
		{"team admin removes themself when last", func(e *env, team Team, solo, _ member) error {
			return e.s.RemoveMember(ctx, solo.P, team.ID, solo.User.ID)
		}, ErrLastTeamAdmin},
		{"team admin demotes themself when last", func(e *env, team Team, solo, _ member) error {
			return e.s.SetTeamRole(ctx, solo.P, team.ID, solo.User.ID, RoleMember)
		}, ErrLastTeamAdmin},
		{"global admin removes the last admin", func(e *env, team Team, solo, _ member) error {
			return e.s.RemoveMember(ctx, e.adminP, team.ID, solo.User.ID)
		}, nil},
		{"global admin demotes the last admin", func(e *env, team Team, solo, _ member) error {
			return e.s.SetTeamRole(ctx, e.adminP, team.ID, solo.User.ID, RoleMember)
		}, nil},
		{"team admin removes a plain member", func(e *env, team Team, solo, plain member) error {
			return e.s.RemoveMember(ctx, solo.P, team.ID, plain.User.ID)
		}, nil},
		{"team admin removes themself when another admin exists", func(e *env, team Team, solo, plain member) error {
			wantErr(e.t, e.s.SetTeamRole(ctx, solo.P, team.ID, plain.User.ID, RoleAdmin), nil)
			return e.s.RemoveMember(ctx, solo.P, team.ID, solo.User.ID)
		}, nil},
		{"team admin demotes themself when another admin exists", func(e *env, team Team, solo, plain member) error {
			wantErr(e.t, e.s.SetTeamRole(ctx, solo.P, team.ID, plain.User.ID, RoleAdmin), nil)
			return e.s.SetTeamRole(ctx, solo.P, team.ID, solo.User.ID, RoleMember)
		}, nil},
		{"setting the same role is a no-op", func(e *env, team Team, solo, _ member) error {
			return e.s.SetTeamRole(ctx, solo.P, team.ID, solo.User.ID, RoleAdmin)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, team, solo, plain := setup(t)
			before := e.dump()
			err := tt.do(e, team, solo, plain)
			wantErr(t, err, tt.want)
			if tt.want != nil && e.dump() != before {
				t.Errorf("refused change altered the database")
			}
		})
	}
}

func TestErasingLastTeamAdminIsNotBlocked(t *testing.T) {
	// Erasure outranks the "never zero admins" rule for teams: the right to
	// be forgotten must not depend on team arrangements. A global
	// administrator can appoint a replacement afterwards.
	e := newEnv(t)
	ctx := context.Background()
	team := e.newTeam("orphaned")
	solo := e.teamMember("solo-admin", team, RoleAdmin)

	_, err := e.s.DeleteUser(ctx, e.adminP, solo.User.ID, false)
	wantErr(t, err, nil)

	heir := e.teamMember("heir", team, RoleMember)
	wantErr(t, e.s.SetTeamRole(ctx, e.adminP, team.ID, heir.User.ID, RoleAdmin), nil)
}

func TestMembershipRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	team := e.newTeam("alpha")
	u, _ := e.user("someone")

	t.Run("archived teams take no members", func(t *testing.T) {
		arch := e.newTeam("old-team")
		wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, arch.ID), nil)
		wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, arch.ID), nil) // Idempotent.
		wantErr(t, e.s.AddMember(ctx, e.adminP, arch.ID, u.ID, RoleMember), ErrTeamArchived)

		teams, err := e.s.ListTeams(ctx, e.adminP, false, Page{})
		wantErr(t, err, nil)
		for _, tm := range teams {
			if tm.ID == arch.ID {
				t.Errorf("archived team listed without includeArchived")
			}
		}
		teams, err = e.s.ListTeams(ctx, e.adminP, true, Page{})
		wantErr(t, err, nil)
		var found bool
		for _, tm := range teams {
			found = found || (tm.ID == arch.ID && tm.IsArchived())
		}
		if !found {
			t.Errorf("archived team missing from includeArchived listing")
		}
	})
	t.Run("deleted users cannot be added", func(t *testing.T) {
		gone, _ := e.user("goner")
		_, err := e.s.DeleteUser(ctx, e.adminP, gone.ID, false)
		wantErr(t, err, nil)
		wantErr(t, e.s.AddMember(ctx, e.adminP, team.ID, gone.ID, RoleMember), ErrNotFound)
	})
	t.Run("unknown team", func(t *testing.T) {
		wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, "t_missing"), ErrNotFound)
		wantErr(t, e.s.RemoveMember(ctx, e.adminP, "t_missing", u.ID), ErrNotFound)
	})
	t.Run("remove non-member", func(t *testing.T) {
		wantErr(t, e.s.RemoveMember(ctx, e.adminP, team.ID, u.ID), ErrNotMember)
	})
	t.Run("MembersOf lists the roster", func(t *testing.T) {
		wantErr(t, e.s.AddMember(ctx, e.adminP, team.ID, u.ID, RoleMember), nil)
		members, err := e.s.MembersOf(ctx, e.adminP, team.ID)
		wantErr(t, err, nil)
		if len(members) != 1 || members[0].Handle != "someone" || members[0].Role != RoleMember {
			t.Errorf("roster = %+v", members)
		}
	})
	t.Run("membership of archived team leaves MembershipsOf", func(t *testing.T) {
		ms, err := e.s.MembershipsOf(ctx, e.adminP, u.ID)
		wantErr(t, err, nil)
		if len(ms) != 1 {
			t.Fatalf("memberships = %+v", ms)
		}
		wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, team.ID), nil)
		ms, _ = e.s.MembershipsOf(ctx, e.adminP, u.ID)
		if len(ms) != 0 {
			t.Errorf("archived membership still listed: %+v", ms)
		}
	})
}

func TestPaging(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, h := range []string{"aa", "bb", "cc", "dd"} {
		e.user(h)
	}
	// Five live users (root + 4). Pages must tile the set with no overlap.
	seen := map[string]bool{}
	for off := 0; off < 5; off += 2 {
		page, err := e.s.ListUsers(ctx, e.adminP, Page{Limit: 2, Offset: off})
		wantErr(t, err, nil)
		for _, u := range page {
			if seen[u.ID] {
				t.Errorf("user %s repeated across pages", u.ID)
			}
			seen[u.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("pages covered %d users, want 5", len(seen))
	}
}
