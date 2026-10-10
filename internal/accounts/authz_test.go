package accounts

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// world is a standard cast for authorisation tests:
//
//	root       global admin (also administers the bootstrap team "ops")
//	tAdmin     admin of team alpha
//	tMember    member of team alpha
//	uAdmin     admin of team beta
//	outsider   no teams
//	newbie     no teams; the usual target of an operation
//
// plus three principals that must not be honoured: one whose key was
// revoked, one who was an admin of alpha when captured but has since been
// demoted, and a member whose Principal has been edited by hand to claim
// global admin.
type world struct {
	*env
	alpha, beta Team
	tAdmin      member
	tMember     member
	uAdmin      member
	outsider    User
	outsiderP   Principal
	newbie      User
	newbieKey   Key

	revoked Principal
	demoted Principal
	forged  Principal
}

func newWorld(t *testing.T) *world {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	w := &world{env: e}
	w.alpha, w.beta = e.newTeam("alpha"), e.newTeam("beta")
	w.tAdmin = e.teamMember("t-admin", w.alpha, RoleAdmin)
	w.tMember = e.teamMember("t-member", w.alpha, RoleMember)
	w.uAdmin = e.teamMember("u-admin", w.beta, RoleAdmin)
	w.outsider, w.outsiderP = e.user("outsider")

	u, k, err := e.s.CreateUser(ctx, e.adminP, "newbie", "")
	wantErr(t, err, nil)
	w.newbie, w.newbieKey = u, k.Key

	// Revoked: authenticated, then the key is revoked.
	rev := e.teamMember("revoked-admin", w.alpha, RoleAdmin)
	w.revoked = rev.P
	wantErr(t, e.s.RevokeKey(ctx, e.adminP, rev.P.KeyID), nil)

	// Demoted: was a team admin when captured.
	dem := e.teamMember("demoted-admin", w.alpha, RoleAdmin)
	w.demoted = dem.P
	wantErr(t, e.s.SetTeamRole(ctx, e.adminP, w.alpha.ID, dem.User.ID, RoleMember), nil)

	// Forged: a plain member claiming to be a global admin.
	w.forged = w.tMember.P
	w.forged.GlobalAdmin = true
	return w
}

// actors returns the principals under test, by name.
func (w *world) actors() map[string]Principal {
	return map[string]Principal{
		"root":     w.adminP,
		"tAdmin":   w.tAdmin.P,
		"tMember":  w.tMember.P,
		"uAdmin":   w.uAdmin.P,
		"outsider": w.outsiderP,
		"zero":     {},
		"revoked":  w.revoked,
		"demoted":  w.demoted,
		"forged":   w.forged,
	}
}

var actorNames = []string{"root", "tAdmin", "tMember", "uAdmin", "outsider", "zero", "revoked", "demoted", "forged"}

// clone copies the world's database into a fresh store, so each matrix case
// starts from identical state without paying to rebuild it. (Building a world
// is dozens of SQL transactions, which is slow under the race detector.)
func (w *world) clone(t *testing.T) *world {
	t.Helper()
	if _, err := w.s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gandalf.db")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path, WithSecretKey(testKey(1)), fastSync)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	c := &clock{t: w.clock.t}
	s.now = c.now
	e := *w.env
	e.s, e.clock, e.path = s, c, path
	cp := *w
	cp.env = &e
	return &cp
}

func TestAuthorisationMatrix(t *testing.T) {
	ctx := context.Background()
	base := newWorld(t)

	tests := []struct {
		name    string
		allowed []string // Everyone else must get ErrForbidden.
		run     func(w *world, p Principal) error
	}{
		{"CreateUser", []string{"root"}, func(w *world, p Principal) error {
			_, _, err := w.s.CreateUser(ctx, p, "brand-new", "")
			return err
		}},
		{"GetUser other", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.GetUser(ctx, p, w.newbie.ID)
			return err
		}},
		{"ListUsers", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.ListUsers(ctx, p, Page{})
			return err
		}},
		{"SetEmail other", []string{"root"}, func(w *world, p Principal) error {
			return w.s.SetEmail(ctx, p, w.newbie.ID, "n@example.com")
		}},
		{"SetGlobalAdmin", []string{"root"}, func(w *world, p Principal) error {
			return w.s.SetGlobalAdmin(ctx, p, w.newbie.ID, true)
		}},
		{"DeleteUser dry run", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.DeleteUser(ctx, p, w.newbie.ID, true)
			return err
		}},
		{"DeleteUser", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.DeleteUser(ctx, p, w.newbie.ID, false)
			return err
		}},
		{"IssueKey", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.IssueKey(ctx, p, w.newbie.ID, "k", 0)
			return err
		}},
		{"ListKeys other", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.ListKeys(ctx, p, w.newbie.ID)
			return err
		}},
		{"RevokeKey", []string{"root"}, func(w *world, p Principal) error {
			return w.s.RevokeKey(ctx, p, w.newbieKey.ID)
		}},
		{"CreateTeam", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.CreateTeam(ctx, p, "gamma", "Gamma")
			return err
		}},
		{"ArchiveTeam", []string{"root"}, func(w *world, p Principal) error {
			return w.s.ArchiveTeam(ctx, p, w.alpha.ID)
		}},
		{"ListTeams", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.ListTeams(ctx, p, true, Page{})
			return err
		}},
		{"MembershipsOf other", []string{"root"}, func(w *world, p Principal) error {
			_, err := w.s.MembershipsOf(ctx, p, w.newbie.ID)
			return err
		}},
		{"TeamBySlug", // demoted is still a plain member of alpha, and forged is a member
			// whose invented admin flag is ignored.
			[]string{"root", "tAdmin", "tMember", "demoted", "forged"}, func(w *world, p Principal) error {
				_, err := w.s.TeamBySlug(ctx, p, "alpha")
				return err
			}},
		{"MembersOf", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			_, err := w.s.MembersOf(ctx, p, w.alpha.ID)
			return err
		}},
		{"AddMember", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			return w.s.AddMember(ctx, p, w.alpha.ID, w.newbie.ID, RoleMember)
		}},
		{"AddMember as admin", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			return w.s.AddMember(ctx, p, w.alpha.ID, w.newbie.ID, RoleAdmin)
		}},
		{"RemoveMember", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			return w.s.RemoveMember(ctx, p, w.alpha.ID, w.tMember.User.ID)
		}},
		{"SetTeamRole promote", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			return w.s.SetTeamRole(ctx, p, w.alpha.ID, w.tMember.User.ID, RoleAdmin)
		}},
		{"UserByHandle other", []string{"root", "tAdmin", "uAdmin"}, func(w *world, p Principal) error {
			_, err := w.s.UserByHandle(ctx, p, "newbie")
			return err
		}},
		{"GenerateRemoteKey team vault", []string{"root", "tAdmin"}, func(w *world, p Principal) error {
			_, err := w.s.GenerateRemoteKey(ctx, p, w.alpha.ID)
			return err
		}},
		{"GenerateRemoteKey other's personal vault", nil, func(w *world, p Principal) error {
			_, err := w.s.GenerateRemoteKey(ctx, p, w.newbie.ID)
			return err
		}},
	}

	for _, tt := range tests {
		for _, name := range actorNames {
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				w := base.clone(t)
				before := w.dump()
				err := tt.run(w, w.actors()[name])

				if slices.Contains(tt.allowed, name) {
					wantErr(t, err, nil)
					return
				}
				wantErr(t, err, ErrForbidden)
				if after := w.dump(); after != before {
					t.Errorf("a forbidden call changed the database")
				}
			})
		}
	}
}

func TestSelfServiceRules(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	m := w.tMember

	t.Run("read own record including email", func(t *testing.T) {
		wantErr(t, w.s.SetEmail(ctx, m.P, m.User.ID, sampleEmail), nil)
		u, err := w.s.GetUser(ctx, m.P, m.User.ID)
		wantErr(t, err, nil)
		if u.Email != sampleEmail || u.Handle != "t-member" {
			t.Errorf("own record = %v / %q", u, u.Email)
		}
	})
	t.Run("list own keys", func(t *testing.T) {
		keys, err := w.s.ListKeys(ctx, m.P, m.User.ID)
		wantErr(t, err, nil)
		if len(keys) != 1 {
			t.Errorf("got %d keys", len(keys))
		}
	})
	t.Run("list own teams", func(t *testing.T) {
		ms, err := w.s.MembershipsOf(ctx, m.P, m.User.ID)
		wantErr(t, err, nil)
		if len(ms) != 1 || ms[0].Slug != "alpha" || ms[0].Role != RoleMember {
			t.Errorf("memberships = %+v", ms)
		}
	})
	t.Run("look up self by handle", func(t *testing.T) {
		u, err := w.s.UserByHandle(ctx, m.P, "t-member")
		wantErr(t, err, nil)
		if u.ID != m.User.ID || u.Email != sampleEmail {
			t.Errorf("self lookup = %v", u)
		}
	})
	t.Run("cannot look up others by handle", func(t *testing.T) {
		for _, h := range []string{"newbie", "no-such-handle"} {
			_, err := w.s.UserByHandle(ctx, m.P, h)
			wantErr(t, err, ErrForbidden) // Same answer whether or not it exists.
		}
	})
	t.Run("revoke own non-current key", func(t *testing.T) {
		spare, err := w.s.IssueKey(ctx, w.adminP, m.User.ID, "spare", 0)
		wantErr(t, err, nil)
		wantErr(t, w.s.RevokeOwnKey(ctx, m.P, spare.Key.ID), nil)
		wantErr(t, w.s.RevokeOwnKey(ctx, m.P, m.P.KeyID), ErrCurrentKey)
	})
	t.Run("cannot issue own key", func(t *testing.T) {
		_, err := w.s.IssueKey(ctx, m.P, m.User.ID, "mine", 0)
		wantErr(t, err, ErrForbidden)
	})
	t.Run("cannot read other users' keys, teams or records", func(t *testing.T) {
		_, err := w.s.ListKeys(ctx, m.P, w.tAdmin.User.ID)
		wantErr(t, err, ErrForbidden)
		_, err = w.s.MembershipsOf(ctx, m.P, w.tAdmin.User.ID)
		wantErr(t, err, ErrForbidden)
		_, err = w.s.GetUser(ctx, m.P, w.tAdmin.User.ID)
		wantErr(t, err, ErrForbidden)
	})
	t.Run("team slug lookup does not reveal other teams", func(t *testing.T) {
		_, err := w.s.TeamBySlug(ctx, m.P, "beta")
		wantErr(t, err, ErrForbidden)
		_, err = w.s.TeamBySlug(ctx, m.P, "no-such-team")
		wantErr(t, err, ErrForbidden)
		got, err := w.s.TeamBySlug(ctx, m.P, "alpha")
		wantErr(t, err, nil)
		if got.ID != w.alpha.ID {
			t.Errorf("TeamBySlug = %+v", got)
		}
	})
	t.Run("global admin gets not-found where others get forbidden", func(t *testing.T) {
		_, err := w.s.TeamBySlug(ctx, w.adminP, "no-such-team")
		wantErr(t, err, ErrNotFound)
		_, err = w.s.UserByHandle(ctx, w.adminP, "no-such-handle")
		wantErr(t, err, ErrNotFound)
	})
}

func TestTeamAdminScopeIsOneTeam(t *testing.T) {
	// An administrator of alpha has no power over beta, even over members
	// they could name, and a team's roster is hidden from other teams.
	w := newWorld(t)
	ctx := context.Background()

	wantErr(t, w.s.AddMember(ctx, w.tAdmin.P, w.beta.ID, w.newbie.ID, RoleMember), ErrForbidden)
	wantErr(t, w.s.RemoveMember(ctx, w.tAdmin.P, w.beta.ID, w.uAdmin.User.ID), ErrForbidden)
	wantErr(t, w.s.SetTeamRole(ctx, w.tAdmin.P, w.beta.ID, w.uAdmin.User.ID, RoleMember), ErrForbidden)
	_, err := w.s.MembersOf(ctx, w.tAdmin.P, w.beta.ID)
	wantErr(t, err, ErrForbidden)

	// A nonexistent team is indistinguishable from someone else's.
	wantErr(t, w.s.AddMember(ctx, w.tAdmin.P, "t_nosuchteam", w.newbie.ID, RoleMember), ErrForbidden)
	wantErr(t, w.s.AddMember(ctx, w.adminP, "t_nosuchteam", w.newbie.ID, RoleMember), ErrNotFound)
}

func TestTeamAdminCapabilities(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a := w.tAdmin.P

	wantErr(t, w.s.AddMember(ctx, a, w.alpha.ID, w.newbie.ID, RoleMember), nil)
	wantErr(t, w.s.AddMember(ctx, a, w.alpha.ID, w.newbie.ID, RoleMember), ErrAlreadyMember)
	wantErr(t, w.s.SetTeamRole(ctx, a, w.alpha.ID, w.newbie.ID, RoleAdmin), nil)
	wantErr(t, w.s.SetTeamRole(ctx, a, w.alpha.ID, w.newbie.ID, RoleMember), nil)
	wantErr(t, w.s.RemoveMember(ctx, a, w.alpha.ID, w.newbie.ID), nil)
	wantErr(t, w.s.RemoveMember(ctx, a, w.alpha.ID, w.newbie.ID), ErrNotMember)

	members, err := w.s.MembersOf(ctx, a, w.alpha.ID)
	wantErr(t, err, nil)
	if len(members) < 2 {
		t.Errorf("roster = %+v", members)
	}

	// Cannot add a deleted or unknown user, or use an unknown role.
	wantErr(t, w.s.AddMember(ctx, a, w.alpha.ID, "u_nosuchuser", RoleMember), ErrNotFound)
	wantErr(t, w.s.AddMember(ctx, a, w.alpha.ID, w.newbie.ID, Role("owner")), ErrInvalidArgument)
	wantErr(t, w.s.SetTeamRole(ctx, a, w.alpha.ID, w.tMember.User.ID, Role("")), ErrInvalidArgument)
}

func TestStalePrincipalsGainNothing(t *testing.T) {
	// Authorisation re-reads the database, so a Principal that was valid
	// when issued confers nothing after the fact.
	w := newWorld(t)
	ctx := context.Background()

	if !w.demoted.IsTeamAdmin(w.alpha.ID) {
		t.Fatal("test setup: the captured principal should claim team admin")
	}
	wantErr(t, w.s.AddMember(ctx, w.demoted, w.alpha.ID, w.newbie.ID, RoleMember), ErrForbidden)

	// Erased users' principals stop working at once too.
	_, err := w.s.DeleteUser(ctx, w.adminP, w.tAdmin.User.ID, false)
	wantErr(t, err, nil)
	wantErr(t, w.s.AddMember(ctx, w.tAdmin.P, w.alpha.ID, w.newbie.ID, RoleMember), ErrForbidden)

	// Expired keys likewise.
	w.clock.advance(DefaultKeyTTL + 1)
	_, err = w.s.ListUsers(ctx, w.adminP, Page{})
	wantErr(t, err, ErrForbidden)
}

func TestPrincipalHelpers(t *testing.T) {
	p := Principal{
		UserID: "u_me",
		Memberships: []Membership{
			{TeamID: "t_a", Slug: "alpha", Role: RoleAdmin},
			{TeamID: "t_b", Slug: "beta", Role: RoleMember},
		},
	}
	admin := Principal{UserID: "u_root", GlobalAdmin: true}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"member of alpha", p.IsMemberOf("t_a"), true},
		{"member of beta", p.IsMemberOf("t_b"), true},
		{"not member of gamma", p.IsMemberOf("t_c"), false},
		{"admin of alpha", p.IsTeamAdmin("t_a"), true},
		{"not admin of beta", p.IsTeamAdmin("t_b"), false},
		{"can manage alpha", p.CanManageTeam("t_a"), true},
		{"cannot manage beta", p.CanManageTeam("t_b"), false},
		{"global admin manages any team", admin.CanManageTeam("t_zzz"), true},
		{"global admin is not implicitly a member", admin.IsMemberOf("t_a"), false},
		{"own personal vault remote", p.CanManageRemote("u_me"), true},
		{"other personal vault remote", p.CanManageRemote("u_you"), false},
		{"global admin on other personal vault remote", admin.CanManageRemote("u_me"), false},
		{"global admin on own personal vault remote", admin.CanManageRemote("u_root"), true},
		{"team admin team vault remote", p.CanManageRemote("t_a"), true},
		{"team member team vault remote", p.CanManageRemote("t_b"), false},
		{"global admin any team vault remote", admin.CanManageRemote("t_anything"), true},
		{"unknown vault kind", admin.CanManageRemote("x_whatever"), false},
		{"empty vault id", admin.CanManageRemote(""), false},
		{"zero principal, empty vault id", Principal{}.CanManageRemote(""), false},
		{"zero principal", Principal{}.CanManageRemote("u_"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}

	if r, ok := p.RoleInSlug("beta"); !ok || r != RoleMember {
		t.Errorf("RoleInSlug(beta) = %q, %v", r, ok)
	}
	if _, ok := p.RoleInSlug("gamma"); ok {
		t.Errorf("RoleInSlug(gamma) found a role")
	}
}
