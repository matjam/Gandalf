package accounts

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// secrets are distinctive strings that must never reach a log line or an
// error message.
var (
	secretHandle = "zzhandlezz"
	secretEmail  = "zz.person@zzexample.org"
	secretLabel  = "zz label zz"
	secretRemote = "git@zzhost.example:zzowner/zzrepo.git"
)

func TestNothingPersonalIsLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := newEnvOpts(t, WithSecretKey(testKey(1)), WithLogger(logger))
	ctx := context.Background()

	var plaintexts []string
	plaintexts = append(plaintexts, e.adminKey.Plaintext)

	// Exercise every operation that logs, with personal values throughout.
	u, k, err := e.s.CreateUser(ctx, e.adminP, secretHandle, secretEmail)
	wantErr(t, err, nil)
	plaintexts = append(plaintexts, k.Plaintext)
	uP := e.auth(k.Plaintext)

	extra, err := e.s.IssueKey(ctx, e.adminP, u.ID, secretLabel, 0)
	wantErr(t, err, nil)
	plaintexts = append(plaintexts, extra.Plaintext)

	team, err := e.s.CreateTeam(ctx, e.adminP, "logteam", "Log Team")
	wantErr(t, err, nil)
	wantErr(t, e.s.AddMember(ctx, e.adminP, team.ID, u.ID, RoleAdmin), nil)
	wantErr(t, e.s.SetTeamRole(ctx, e.adminP, team.ID, u.ID, RoleMember), nil)
	wantErr(t, e.s.RemoveMember(ctx, e.adminP, team.ID, u.ID), nil)
	wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, u.ID, true), nil)
	wantErr(t, e.s.SetGlobalAdmin(ctx, e.adminP, u.ID, false), nil)
	wantErr(t, e.s.RevokeOwnKey(ctx, uP, extra.Key.ID), nil)
	wantErr(t, e.s.RevokeKey(ctx, e.adminP, k.Key.ID), nil)
	_, err = e.s.GenerateRemoteKey(ctx, e.adminP, team.ID)
	wantErr(t, err, nil)
	wantErr(t, e.s.SetRemoteURL(ctx, e.adminP, team.ID, secretRemote), nil)
	pem, err := e.s.RemotePrivateKey(ctx, team.ID)
	wantErr(t, err, nil)
	wantErr(t, e.s.ArchiveTeam(ctx, e.adminP, team.ID), nil)
	_, err = e.s.DeleteUser(ctx, e.adminP, u.ID, false)
	wantErr(t, err, nil)

	// Log lines for failures too: collect the errors of refused operations.
	var errTexts []string
	_, _, err = e.s.CreateUser(ctx, e.adminP, "Bad "+secretHandle, secretEmail)
	errTexts = append(errTexts, err.Error())
	err = e.s.SetEmail(ctx, e.adminP, e.admin.ID, "bad "+secretEmail)
	errTexts = append(errTexts, err.Error())
	err = e.s.SetRemoteURL(ctx, e.adminP, e.admin.ID, "https://"+secretRemote)
	errTexts = append(errTexts, err.Error())
	_, err = e.s.Authenticate(ctx, e.adminKey.Plaintext+"x")
	errTexts = append(errTexts, err.Error())

	logged := buf.String()
	if logged == "" {
		t.Fatal("nothing was logged; the test would pass vacuously")
	}
	for _, bad := range append([]string{secretHandle, secretEmail, secretLabel, secretRemote, "zzexample", string(pem[:40])}, plaintexts...) {
		if strings.Contains(logged, bad) {
			t.Errorf("log output contains %q", bad)
		}
		for _, et := range errTexts {
			if strings.Contains(et, bad) {
				t.Errorf("error text %q contains %q", et, bad)
			}
		}
	}
	// What is logged is ids.
	for _, want := range []string{u.ID, team.ID, "key issued", "user erased"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
}

func TestValuesRefusePrintingPersonalData(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, k, err := e.s.CreateUser(ctx, e.adminP, secretHandle, secretEmail)
	wantErr(t, err, nil)
	full, err := e.s.GetUser(ctx, e.adminP, u.ID)
	wantErr(t, err, nil)
	p := e.auth(k.Plaintext)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	logger.Info("test", "user", full, "principal", p, "issued", k)

	values := map[string]any{"User": full, "*User": &full, "Principal": p, "IssuedKey": k, "*IssuedKey": &k}
	for name, v := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			out := fmt.Sprintf(format, v)
			for _, bad := range []string{secretHandle, secretEmail, k.Plaintext} {
				if strings.Contains(out, bad) {
					t.Errorf("Sprintf(%q, %s) contains %q: %s", format, name, bad, out)
				}
			}
		}
	}
	for _, bad := range []string{secretHandle, secretEmail, k.Plaintext} {
		if strings.Contains(logBuf.String(), bad) {
			t.Errorf("slog output contains %q: %s", bad, logBuf.String())
		}
	}
	if !strings.Contains(logBuf.String(), u.ID) {
		t.Errorf("slog output lacks the user id: %s", logBuf.String())
	}

	// The field itself is still readable on purpose.
	if k.Plaintext == "" || full.Email != secretEmail {
		t.Errorf("explicit field access broken")
	}
}
