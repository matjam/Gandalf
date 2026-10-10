package accounts

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const sampleRemote = "git@github.com:sample-owner/private-notes.git"

func TestGenerateRemoteKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("alice")

	r, err := e.s.GenerateRemoteKey(ctx, p, u.ID)
	wantErr(t, err, nil)
	if r.VaultID != u.ID || r.SetBy != u.ID || r.URL != "" {
		t.Errorf("remote = %+v", r)
	}

	// The public key is a valid authorized_keys line for an ed25519 key.
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(r.PublicKey))
	if err != nil {
		t.Fatalf("public key does not parse: %v", err)
	}
	if pub.Type() != ssh.KeyAlgoED25519 {
		t.Errorf("key type = %s, want ed25519", pub.Type())
	}
	if comment != "gandalf-"+u.ID {
		t.Errorf("comment = %q", comment)
	}

	// The private key round-trips and matches the public one.
	pem, err := e.s.RemotePrivateKey(ctx, u.ID)
	wantErr(t, err, nil)
	raw, err := ssh.ParseRawPrivateKey(pem)
	if err != nil {
		t.Fatalf("private key does not parse: %v", err)
	}
	priv, ok := raw.(*ed25519.PrivateKey)
	if !ok {
		t.Fatalf("private key is %T", raw)
	}
	signer, err := ssh.NewSignerFromKey(*priv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), pub.Marshal()) {
		t.Errorf("private key does not match the public key")
	}
}

func TestRemoteEncryptedAtRest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("alice")

	_, err := e.s.GenerateRemoteKey(ctx, p, u.ID)
	wantErr(t, err, nil)
	wantErr(t, e.s.SetRemoteURL(ctx, p, u.ID, sampleRemote), nil)
	pem, err := e.s.RemotePrivateKey(ctx, u.ID)
	wantErr(t, err, nil)

	var urlEnc, keyEnc []byte
	if err := e.s.db.QueryRow(`SELECT url_enc, private_key_enc FROM vault_remotes WHERE vault_id = ?`, u.ID).
		Scan(&urlEnc, &keyEnc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(urlEnc, []byte("github.com")) || bytes.Contains(urlEnc, []byte("sample-owner")) {
		t.Errorf("remote URL stored in plaintext")
	}
	if bytes.Contains(keyEnc, []byte("PRIVATE KEY")) || bytes.Contains(keyEnc, []byte("openssh-key-v1")) {
		t.Errorf("private key stored in plaintext")
	}

	// And nowhere in the files, including the WAL.
	if _, err := e.s.db.Exec(`PRAGMA wal_checkpoint(FULL)`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{e.path, e.path + "-wal"} {
		content, err := readFile(f)
		if err != nil {
			continue
		}
		for _, needle := range [][]byte{[]byte("sample-owner"), []byte("openssh-key-v1"), pem[:60]} {
			if bytes.Contains(content, needle) {
				t.Errorf("%s contains %q", f, needle)
			}
		}
	}
}

func TestRemoteRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("alice")

	first, err := e.s.GenerateRemoteKey(ctx, p, u.ID)
	wantErr(t, err, nil)
	wantErr(t, e.s.SetRemoteURL(ctx, p, u.ID, sampleRemote), nil)
	oldPEM, err := e.s.RemotePrivateKey(ctx, u.ID)
	wantErr(t, err, nil)

	e.clock.advance(time.Hour)
	// A global administrator may not touch someone's personal vault remote,
	// so rotation is the owner's act.
	second, err := e.s.GenerateRemoteKey(ctx, p, u.ID)
	wantErr(t, err, nil)

	if second.PublicKey == first.PublicKey {
		t.Errorf("rotation kept the same public key")
	}
	if second.URL != sampleRemote {
		t.Errorf("rotation lost the URL: %q", second.URL)
	}
	if !second.Created.Equal(first.Created) || !second.Updated.After(first.Updated) {
		t.Errorf("created/updated = %v / %v (first %v / %v)", second.Created, second.Updated, first.Created, first.Updated)
	}
	newPEM, err := e.s.RemotePrivateKey(ctx, u.ID)
	wantErr(t, err, nil)
	if bytes.Equal(oldPEM, newPEM) {
		t.Errorf("rotation kept the same private key")
	}
	if n := e.rawCount(`SELECT COUNT(*) FROM vault_remotes`); n != 1 {
		t.Errorf("%d remote rows after rotation, want 1", n)
	}

	got, err := e.s.GetRemote(ctx, p, u.ID)
	wantErr(t, err, nil)
	if got != second {
		t.Errorf("GetRemote = %+v, want %+v", got, second)
	}
}

func TestRemoteURL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u, p := e.user("alice")

	t.Run("needs a key first", func(t *testing.T) {
		wantErr(t, e.s.SetRemoteURL(ctx, p, u.ID, sampleRemote), ErrNotFound)
		_, err := e.s.GetRemote(ctx, p, u.ID)
		wantErr(t, err, ErrNotFound)
		_, err = e.s.RemotePrivateKey(ctx, u.ID)
		wantErr(t, err, ErrNotFound)
	})

	_, err := e.s.GenerateRemoteKey(ctx, p, u.ID)
	wantErr(t, err, nil)

	t.Run("set and clear", func(t *testing.T) {
		wantErr(t, e.s.SetRemoteURL(ctx, p, u.ID, sampleRemote), nil)
		r, err := e.s.GetRemote(ctx, p, u.ID)
		wantErr(t, err, nil)
		if r.URL != sampleRemote {
			t.Errorf("URL = %q", r.URL)
		}

		wantErr(t, e.s.ClearRemoteURL(ctx, p, u.ID), nil)
		r, err = e.s.GetRemote(ctx, p, u.ID)
		wantErr(t, err, nil)
		if r.URL != "" || r.PublicKey == "" {
			t.Errorf("after clear: %+v", r)
		}
		if _, err := e.s.RemotePrivateKey(ctx, u.ID); err != nil {
			t.Errorf("clearing the URL lost the key: %v", err)
		}
	})
	t.Run("empty URL needs ClearRemoteURL", func(t *testing.T) {
		wantErr(t, e.s.SetRemoteURL(ctx, p, u.ID, ""), ErrInvalidArgument)
	})
}

func TestValidateRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"scp style", "git@github.com:owner/repo.git", true},
		{"scp style custom user", "deploy_1@git.example.org:team/notes", true},
		{"ssh url", "ssh://git@github.com/owner/repo.git", true},
		{"ssh url with port", "ssh://git@host.example:2222/owner/repo", true},
		{"https refused", "https://github.com/owner/repo.git", false},
		{"http refused", "http://github.com/owner/repo.git", false},
		{"git protocol refused", "git://github.com/owner/repo.git", false},
		{"file refused", "file:///etc/passwd", false},
		{"local path refused", "/srv/repos/notes.git", false},
		{"ext transport (command execution)", "ext::sh -c touch% /tmp/pwned", false},
		{"ext transport without spaces", "ext::sh", false},
		{"option injection", "-oProxyCommand=evil", false},
		{"option injection after user", "--upload-pack=evil", false},
		{"host starting with dash scp", "git@-oProxyCommand=x:repo", false},
		{"ssh host starting with dash", "ssh://-oProxyCommand=x/repo", false},
		{"ssh url without host", "ssh:///repo", false},
		{"ssh url without path", "ssh://git@host", false},
		{"whitespace", "git@host:owner/my repo", false},
		{"newline", "git@host:owner/repo\n", false},
		{"control char", "git@host:owner/re\x00po", false},
		{"query string", "ssh://git@host/repo?x=y", false},
		{"empty", "", false},
		{"too long", "git@host:" + strings.Repeat("a", maxRemoteURLBytes), false},
		{"no user", "github.com:owner/repo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteURL(tt.in)
			if tt.ok {
				wantErr(t, err, nil)
				return
			}
			wantErr(t, err, ErrInvalidArgument)
			if len(tt.in) > 8 && strings.Contains(err.Error(), tt.in) {
				t.Errorf("error echoes the URL")
			}
		})
	}
}

func TestRemoteRequiresSecretKey(t *testing.T) {
	s, _ := newBareEnv(t)
	ctx := context.Background()
	_, _, k, err := s.Bootstrap(ctx, "root-admin", "", "ops", "Ops")
	wantErr(t, err, nil)
	p, err := s.Authenticate(ctx, k.Plaintext)
	wantErr(t, err, nil)

	_, err = s.GenerateRemoteKey(ctx, p, p.UserID)
	wantErr(t, err, ErrNoSecretKey)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM vault_remotes`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a row was stored despite the refusal: %d, %v", n, err)
	}
}

func TestRemoteAuthorisation(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	// A global administrator with a personal vault of their own may manage
	// that, but never another user's.
	tests := []struct {
		name    string
		actor   Principal
		vaultID string
		want    error
	}{
		{"user, own personal vault", w.outsiderP, w.outsider.ID, nil},
		{"user, another's personal vault", w.outsiderP, w.newbie.ID, ErrForbidden},
		{"global admin, own personal vault", w.adminP, w.admin.ID, nil},
		{"global admin, another's personal vault", w.adminP, w.outsider.ID, ErrForbidden},
		{"team admin, own team", w.tAdmin.P, w.alpha.ID, nil},
		{"team admin, other team", w.tAdmin.P, w.beta.ID, ErrForbidden},
		{"team member, own team", w.tMember.P, w.alpha.ID, ErrForbidden},
		{"global admin, any team", w.adminP, w.beta.ID, nil},
		{"global admin, nonexistent team", w.adminP, "t_nosuchteam", ErrNotFound},
		{"team admin, nonexistent team", w.tAdmin.P, "t_nosuchteam", ErrForbidden},
		{"global admin, unknown vault kind", w.adminP, "x_other", ErrForbidden},
		{"zero principal", Principal{}, w.outsider.ID, ErrForbidden},
	}
	for _, tt := range tests {
		t.Run("generate/"+tt.name, func(t *testing.T) {
			c := w.clone(t)
			_, err := c.s.GenerateRemoteKey(ctx, tt.actor, tt.vaultID)
			wantErr(t, err, tt.want)
		})
		t.Run("get/"+tt.name, func(t *testing.T) {
			c := w.clone(t)
			if tt.want == nil {
				// Reading needs a key to exist; make one as someone allowed.
				_, err := c.s.GenerateRemoteKey(ctx, tt.actor, tt.vaultID)
				wantErr(t, err, nil)
				_, err = c.s.GetRemote(ctx, tt.actor, tt.vaultID)
				wantErr(t, err, nil)
				return
			}
			_, err := c.s.GetRemote(ctx, tt.actor, tt.vaultID)
			wantErr(t, err, tt.want)
		})
		t.Run("set url/"+tt.name, func(t *testing.T) {
			c := w.clone(t)
			if tt.want == nil {
				_, err := c.s.GenerateRemoteKey(ctx, tt.actor, tt.vaultID)
				wantErr(t, err, nil)
			}
			err := c.s.SetRemoteURL(ctx, tt.actor, tt.vaultID, sampleRemote)
			if tt.want == nil {
				wantErr(t, err, nil)
				return
			}
			wantErr(t, err, tt.want)
		})
	}

	t.Run("another user's remote is unreadable to a global admin", func(t *testing.T) {
		c := w.clone(t)
		_, err := c.s.GenerateRemoteKey(ctx, w.outsiderP, w.outsider.ID)
		wantErr(t, err, nil)
		_, err = c.s.GetRemote(ctx, w.adminP, w.outsider.ID)
		wantErr(t, err, ErrForbidden)
		wantErr(t, c.s.ClearRemoteURL(ctx, w.adminP, w.outsider.ID), ErrForbidden)
	})
	t.Run("archived team vault is read-only", func(t *testing.T) {
		c := w.clone(t)
		_, err := c.s.GenerateRemoteKey(ctx, w.adminP, w.beta.ID)
		wantErr(t, err, nil)
		wantErr(t, c.s.ArchiveTeam(ctx, w.adminP, w.beta.ID), nil)
		_, err = c.s.GenerateRemoteKey(ctx, w.adminP, w.beta.ID)
		wantErr(t, err, ErrTeamArchived)
		wantErr(t, c.s.SetRemoteURL(ctx, w.adminP, w.beta.ID, sampleRemote), ErrTeamArchived)
		if _, err := c.s.GetRemote(ctx, w.adminP, w.beta.ID); err != nil {
			t.Errorf("reading an archived team's remote: %v", err)
		}
	})
	t.Run("GetRemote never exposes the private key", func(t *testing.T) {
		c := w.clone(t)
		r, err := c.s.GenerateRemoteKey(ctx, w.tAdmin.P, w.alpha.ID)
		wantErr(t, err, nil)
		pem, err := c.s.RemotePrivateKey(ctx, w.alpha.ID)
		wantErr(t, err, nil)
		if strings.Contains(r.PublicKey, "PRIVATE") || bytes.Contains(pem, []byte(r.PublicKey)) {
			t.Errorf("public/private key mixed up")
		}
	})
}

func TestRemoteCiphertextBoundToItsRow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, aP := e.user("alice")
	b, bP := e.user("bobby")
	for _, c := range []struct {
		u User
		p Principal
	}{{a, aP}, {b, bP}} {
		_, err := e.s.GenerateRemoteKey(ctx, c.p, c.u.ID)
		wantErr(t, err, nil)
		wantErr(t, e.s.SetRemoteURL(ctx, c.p, c.u.ID, sampleRemote), nil)
	}

	// Moving alice's encrypted key into bob's row must not decrypt.
	if _, err := e.s.db.Exec(`UPDATE vault_remotes SET private_key_enc =
		(SELECT private_key_enc FROM vault_remotes WHERE vault_id = ?) WHERE vault_id = ?`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RemotePrivateKey(ctx, b.ID); !isErr(err, ErrDecrypt) {
		t.Errorf("swapped key: %v, want ErrDecrypt", err)
	}

	// Nor may the URL ciphertext stand in for the key ciphertext within a row.
	if _, err := e.s.db.Exec(`UPDATE vault_remotes SET private_key_enc = url_enc WHERE vault_id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RemotePrivateKey(ctx, a.ID); !isErr(err, ErrDecrypt) {
		t.Errorf("url ciphertext as key: %v, want ErrDecrypt", err)
	}
}
