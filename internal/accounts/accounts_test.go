package accounts

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateHandle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"two letters", "ab", true},
		{"letters digits hyphen", "a1-b2", true},
		{"max length", "a" + strings.Repeat("b", 31), true},
		{"trailing hyphen", "ab-", true},
		{"too long", "a" + strings.Repeat("b", 32), false},
		{"too short", "a", false},
		{"empty", "", false},
		{"uppercase", "Alice", false},
		{"leading digit", "1abc", false},
		{"leading hyphen", "-abc", false},
		{"underscore", "a_b", false},
		{"space", "a b", false},
		{"dot", "a.b", false},
		{"unicode", "ålice", false},
		{"newline suffix", "abc\n", false},
		{"reserved erased prefix", "deleted-abc123", false},
		{"erased prefix as a word", "deletedx", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateHandle(tt.in)
			if tt.ok {
				wantErr(t, err, nil)
				return
			}
			wantErr(t, err, ErrInvalidHandle)
			if len(tt.in) >= 5 && strings.Contains(err.Error(), tt.in) {
				t.Errorf("error %q echoes the rejected handle", err)
			}
		})
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"simple", "platform", true},
		{"hyphenated", "team-1", true},
		{"uppercase", "Platform", false},
		{"too short", "a", false},
		{"too long", "a" + strings.Repeat("b", 32), false},
		{"slash", "a/b", false},
		{"dotdot", "..", false},
		// Unlike handles, slugs may start with the erased prefix.
		{"erased prefix", "deleted-team", true},
		{"reserved: personal", "personal", false},
		{"personal as a prefix is fine", "personal-ops", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSlug(tt.in)
			if tt.ok {
				wantErr(t, err, nil)
				return
			}
			wantErr(t, err, ErrInvalidSlug)
		})
	}
}

func TestHandleAndSlugEnforcedByStore(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, _, err := e.s.CreateUser(ctx, e.adminP, "Bad Handle", ""); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("CreateUser bad handle: %v", err)
	}
	if _, _, err := e.s.CreateUser(ctx, e.adminP, "root-admin", ""); !errors.Is(err, ErrHandleTaken) {
		t.Errorf("CreateUser duplicate handle: %v", err)
	}
	if _, err := e.s.CreateTeam(ctx, e.adminP, "Bad Slug", "x"); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("CreateTeam bad slug: %v", err)
	}
	if _, err := e.s.CreateTeam(ctx, e.adminP, "personal", "Personal"); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("CreateTeam reserved slug: %v", err)
	}
	if n := e.rawCount(`SELECT COUNT(*) FROM teams WHERE slug = 'personal'`); n != 0 {
		t.Errorf("reserved slug was stored")
	}
	if _, err := e.s.CreateTeam(ctx, e.adminP, "ops", "again"); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("CreateTeam duplicate slug: %v", err)
	}
	if _, err := e.s.CreateTeam(ctx, e.adminP, "fine-slug", " "); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("CreateTeam blank name: %v", err)
	}
	if _, err := e.s.CreateTeam(ctx, e.adminP, "fine-slug", "bad\x00name"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("CreateTeam control character name: %v", err)
	}
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id, err := newID("u_")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, "u_") || len(id) != 2+26 || id != strings.ToLower(id) {
			t.Fatalf("id %q has the wrong shape", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestParseSecretKey(t *testing.T) {
	raw := testKey(7)
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"std padded", base64.StdEncoding.EncodeToString(raw), true},
		{"std unpadded", base64.RawStdEncoding.EncodeToString(raw), true},
		{"url padded", base64.URLEncoding.EncodeToString(raw), true},
		{"url unpadded", base64.RawURLEncoding.EncodeToString(raw), true},
		{"surrounding whitespace", "  " + base64.StdEncoding.EncodeToString(raw) + "\n", true},
		{"empty", "", false},
		{"not base64", "this is not base64!!", false},
		{"too short", base64.StdEncoding.EncodeToString(raw[:16]), false},
		{"too long", base64.StdEncoding.EncodeToString(append(raw, 1)), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSecretKey(tt.in)
			if !tt.ok {
				wantErr(t, err, ErrSecretKey)
				if s := strings.TrimSpace(tt.in); s != "" && strings.Contains(err.Error(), s) {
					t.Errorf("error %q echoes the key", err)
				}
				return
			}
			wantErr(t, err, nil)
			if string(got) != string(raw) {
				t.Errorf("decoded key differs")
			}
		})
	}
}

func TestGenerateSecretKeyRoundtrip(t *testing.T) {
	s, err := GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSecretKey(s); err != nil {
		t.Errorf("generated key does not parse: %v", err)
	}
}

func TestWithSecretKeyRejectsBadLength(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), WithSecretKey([]byte("short")))
	wantErr(t, err, ErrSecretKey)
}

// A database left with looser permissions (restored from a backup, or created
// by an earlier build) is tightened on open: it holds credential hashes.
func TestOpenTightensAnExistingDatabaseFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	path := filepath.Join(t.TempDir(), "gandalf.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(context.Background(), path, fastSync)
	wantErr(t, err, nil)
	defer s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("database mode = %o, want 600", perm)
	}
}

func TestOpenCreatesPrivateFileAndPersists(t *testing.T) {
	e := newEnv(t)

	// Windows has no POSIX mode bits to assert: access there is the ACL the
	// parent directory hands down.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(e.path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("database mode = %o, want 600", perm)
		}
		dir, err := os.Stat(filepath.Dir(e.path))
		if err != nil {
			t.Fatal(err)
		}
		if perm := dir.Mode().Perm(); perm != 0o700 {
			t.Errorf("directory mode = %o, want 700", perm)
		}
	}

	// Reopening keeps the data and does not re-run migrations destructively.
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(context.Background(), e.path, WithSecretKey(testKey(1)))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if _, err := s2.Authenticate(context.Background(), e.adminKey.Plaintext); err != nil {
		t.Errorf("key does not authenticate after reopen: %v", err)
	}
	var v int
	if err := s2.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v); err != nil || v != len(migrations) {
		t.Errorf("schema version = %d (%v), want %d", v, err, len(migrations))
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.db.Exec(`UPDATE schema_version SET version = ?`, len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	e.s.Close()

	_, err := Open(context.Background(), e.path)
	if err == nil || !strings.Contains(err.Error(), "newer than this build") {
		t.Errorf("Open on newer schema = %v, want refusal", err)
	}
}

func TestOpenPathWithURICharacters(t *testing.T) {
	// '?' is not a legal character in a Windows file name; the others are what
	// would break a file: URI, and the fragment and escape are enough there.
	name := "a?b#c%d"
	if runtime.GOOS == "windows" {
		name = "a#b%25c"
	}
	dir := filepath.Join(t.TempDir(), name)
	s, err := Open(context.Background(), filepath.Join(dir, "gandalf.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "gandalf.db")); err != nil {
		t.Errorf("database not at the requested path: %v", err)
	}
}

func TestPageNorm(t *testing.T) {
	tests := []struct {
		name string
		in   Page
		want Page
	}{
		{"zero", Page{}, Page{Limit: DefaultPageSize}},
		{"negative", Page{Limit: -3, Offset: -9}, Page{Limit: DefaultPageSize}},
		{"capped", Page{Limit: MaxPageSize * 10, Offset: 4}, Page{Limit: MaxPageSize, Offset: 4}},
		{"kept", Page{Limit: 7, Offset: 2}, Page{Limit: 7, Offset: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.norm(); got != tt.want {
				t.Errorf("norm(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
