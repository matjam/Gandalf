package accounts

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// erasureFixture is a user with everything erasure has to deal with: an
// email, two keys, memberships in a live and an archived team, and a
// personal vault remote.
type erasureFixture struct {
	*env
	target   User
	keyIDs   []string
	keyPlain []string
	live     Team
	archived Team
}

func newErasureFixture(t *testing.T) *erasureFixture {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	f := &erasureFixture{env: e}

	u, k, err := e.s.CreateUser(ctx, e.adminP, "victim", "victim@example.com")
	wantErr(t, err, nil)
	f.target = u
	k2, err := e.s.IssueKey(ctx, e.adminP, u.ID, "second", 0)
	wantErr(t, err, nil)
	f.keyIDs = []string{k.Key.ID, k2.Key.ID}
	f.keyPlain = []string{k.Plaintext, k2.Plaintext}

	f.live, f.archived = e.newTeam("live"), e.newTeam("shelved")
	e.add(f.live, u, RoleAdmin)
	e.add(f.archived, u, RoleMember)
	wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, f.archived.ID), nil)

	victimP := e.auth(k.Plaintext)
	if _, err := e.s.GenerateRemoteKey(ctx, victimP, u.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.s.SetRemoteURL(ctx, victimP, u.ID, "git@github.com:victim/notes.git"), nil)
	return f
}

func TestDeleteUserDryRunChangesNothing(t *testing.T) {
	f := newErasureFixture(t)
	ctx := context.Background()
	before := f.dump()

	plan, err := f.s.DeleteUser(ctx, f.adminP, f.target.ID, true)
	wantErr(t, err, nil)

	if f.dump() != before {
		t.Errorf("dry run changed the database")
	}
	if !plan.DryRun {
		t.Errorf("plan does not say it is a dry run")
	}
	f.checkPlan(t, plan)

	// Everything still works afterwards.
	for _, k := range f.keyPlain {
		if _, err := f.s.Authenticate(ctx, k); err != nil {
			t.Errorf("key stopped working after a dry run: %v", err)
		}
	}
	u, err := f.s.GetUser(ctx, f.adminP, f.target.ID)
	wantErr(t, err, nil)
	if u.Handle != "victim" || u.Email != "victim@example.com" {
		t.Errorf("record changed by dry run: %v", u)
	}
}

// checkPlan asserts the plan describes the fixture.
func (f *erasureFixture) checkPlan(t *testing.T, plan ErasurePlan) {
	t.Helper()
	if plan.UserID != f.target.ID || plan.PersonalVaultID != f.target.ID {
		t.Errorf("plan ids = %q / %q, want %q", plan.UserID, plan.PersonalVaultID, f.target.ID)
	}
	gotKeys := slices.Clone(plan.KeysRevoked)
	wantKeys := slices.Clone(f.keyIDs)
	slices.Sort(gotKeys)
	slices.Sort(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("keys revoked = %v, want %v", gotKeys, wantKeys)
	}
	slugs := map[string]Role{}
	for _, m := range plan.MembershipsRemoved {
		slugs[m.Slug] = m.Role
	}
	if len(slugs) != 2 || slugs["live"] != RoleAdmin || slugs["shelved"] != RoleMember {
		t.Errorf("memberships removed = %+v (archived-team membership must be included)", plan.MembershipsRemoved)
	}
	if !plan.EmailCleared || !plan.RemoteCleared {
		t.Errorf("EmailCleared %v, RemoteCleared %v; want both", plan.EmailCleared, plan.RemoteCleared)
	}
	suffix := strings.TrimPrefix(f.target.ID, "u_")
	if plan.AnonymisedHandle != "deleted-"+suffix[:6] {
		t.Errorf("anonymised handle = %q, want deleted-%s", plan.AnonymisedHandle, suffix[:6])
	}
	if strings.Contains(plan.AnonymisedHandle, "victim") {
		t.Errorf("anonymised handle contains the original")
	}
}

func TestDeleteUserErases(t *testing.T) {
	f := newErasureFixture(t)
	ctx := context.Background()

	plan, err := f.s.DeleteUser(ctx, f.adminP, f.target.ID, false)
	wantErr(t, err, nil)
	if plan.DryRun || plan.AlreadyDeleted {
		t.Errorf("plan flags = %+v", plan)
	}
	f.checkPlan(t, plan)

	t.Run("keys no longer authenticate", func(t *testing.T) {
		for _, k := range f.keyPlain {
			if _, err := f.s.Authenticate(ctx, k); !isInvalidKey(err) {
				t.Errorf("erased user's key: %v, want ErrInvalidKey", err)
			}
		}
		if n := f.rawCount(`SELECT COUNT(*) FROM keys WHERE user_id = ? AND revoked IS NULL`, f.target.ID); n != 0 {
			t.Errorf("%d keys left unrevoked", n)
		}
	})
	t.Run("record is anonymised", func(t *testing.T) {
		u, err := f.s.GetUser(ctx, f.adminP, f.target.ID)
		wantErr(t, err, nil)
		if !u.IsDeleted() || u.Handle != plan.AnonymisedHandle || u.Email != "" || u.HasEmail || u.GlobalAdmin {
			t.Errorf("erased record = %+v", u)
		}
		if enc := rawEmail(t, f.env, f.target.ID); len(enc) != 0 {
			t.Errorf("email_enc not cleared")
		}
		if n := f.rawCount(`SELECT COUNT(*) FROM users WHERE handle = 'victim'`); n != 0 {
			t.Errorf("original handle still in the database")
		}
	})
	t.Run("memberships and remote are gone", func(t *testing.T) {
		if n := f.rawCount(`SELECT COUNT(*) FROM memberships WHERE user_id = ?`, f.target.ID); n != 0 {
			t.Errorf("%d memberships remain", n)
		}
		if n := f.rawCount(`SELECT COUNT(*) FROM vault_remotes WHERE vault_id = ?`, f.target.ID); n != 0 {
			t.Errorf("remote remains")
		}
	})
	t.Run("not listed, not resolvable by old handle", func(t *testing.T) {
		list, err := f.s.ListUsers(ctx, f.adminP, Page{})
		wantErr(t, err, nil)
		for _, u := range list {
			if u.ID == f.target.ID {
				t.Errorf("erased user in ListUsers")
			}
		}
		_, err = f.s.UserByHandle(ctx, f.adminP, "victim")
		wantErr(t, err, ErrNotFound)
	})
	t.Run("handle can be reused", func(t *testing.T) {
		_, _, err := f.s.CreateUser(ctx, f.adminP, "victim", "")
		wantErr(t, err, nil)
	})
	t.Run("cannot be modified afterwards", func(t *testing.T) {
		_, err := f.s.IssueKey(ctx, f.adminP, f.target.ID, "zombie", 0)
		wantErr(t, err, ErrNotFound)
		wantErr(t, f.s.AddMember(ctx, f.adminP, f.live.ID, f.target.ID, RoleMember), ErrNotFound)
		wantErr(t, f.s.SetEmail(ctx, f.adminP, f.target.ID, "a@example.com"), ErrNotFound)
		wantErr(t, f.s.SetGlobalAdmin(ctx, f.adminP, f.target.ID, true), ErrNotFound)
	})
	t.Run("a second erasure is idempotent", func(t *testing.T) {
		before := f.dump()
		again, err := f.s.DeleteUser(ctx, f.adminP, f.target.ID, false)
		wantErr(t, err, nil)
		if !again.AlreadyDeleted || again.PersonalVaultID != f.target.ID || len(again.KeysRevoked) != 0 {
			t.Errorf("repeat plan = %+v", again)
		}
		if f.dump() != before {
			t.Errorf("repeat erasure changed the database")
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		_, err := f.s.DeleteUser(ctx, f.adminP, "u_nosuchuser", false)
		wantErr(t, err, ErrNotFound)
	})
}

func TestDeleteUserLeavesNoPersonalDataBehind(t *testing.T) {
	f := newErasureFixture(t)
	ctx := context.Background()
	// A label can say anything, including a name. Labels are the remaining
	// Personal field on a retained row, so they must go with the person.
	if _, err := f.s.db.Exec(`UPDATE keys SET label = 'victims laptop' WHERE user_id = ?`, f.target.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.DeleteUser(ctx, f.adminP, f.target.ID, false)
	wantErr(t, err, nil)

	if n := f.rawCount(`SELECT COUNT(*) FROM keys WHERE user_id = ? AND label != ''`, f.target.ID); n != 0 {
		t.Errorf("%d erased user's keys still carry a label (Personal data)", n)
	}
}

func TestDeleteUserHandleCollisionFallsBackToLongerSuffix(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, _ := e.user("someone")
	suffix := strings.TrimPrefix(u.ID, "u_")

	// Occupy the six-character anonymised handle with another erased user.
	other, _ := e.user("another")
	if _, err := e.s.db.Exec(`UPDATE users SET handle = ?, deleted = 1 WHERE id = ?`, "deleted-"+suffix[:6], other.ID); err != nil {
		t.Fatal(err)
	}

	plan, err := e.s.DeleteUser(ctx, e.adminP, u.ID, false)
	wantErr(t, err, nil)
	if plan.AnonymisedHandle != "deleted-"+suffix[:7] {
		t.Errorf("handle = %q, want 7-character fallback", plan.AnonymisedHandle)
	}
}
