package accounts

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; the schema version is the number applied.
// A migration is never edited once released: fix it with another one. The
// accounts database is the system of record (unlike the search index, it
// cannot be rebuilt), so there is no drop-and-recreate path.
//
// Open refuses to start on a database newer than this build understands,
// rather than serve requests against a schema it may misread.
//
// Classification of every column, per the privacy standard:
//
//	Personal    identifies or can identify a person
//	Credential  authenticates someone; never logged, never returned
//	Internal    not personal, not for publication
var migrations = []string{
	// 1: users, teams, memberships, keys, vault remotes.
	`
CREATE TABLE users (
    id           TEXT PRIMARY KEY,                       -- Internal. Opaque "u_" + random; also the personal vault's id.
    handle       TEXT NOT NULL UNIQUE                    -- Personal (a chosen name). Replaced by "deleted-<id suffix>" on erasure.
                 CHECK (handle = lower(handle)),
    email_enc    BLOB,                                   -- Personal. AES-256-GCM, nonce-prefixed; NULL when absent or erased.
    global_admin INTEGER NOT NULL DEFAULT 0              -- Internal.
                 CHECK (global_admin IN (0, 1)),
    created      INTEGER NOT NULL,                       -- Internal. Unix seconds.
    deleted      INTEGER                                 -- Internal. Unix seconds; non-NULL means erased and anonymised.
);

CREATE TABLE teams (
    id       TEXT PRIMARY KEY,                           -- Internal. Opaque "t_" + random; also the team vault's id.
    slug     TEXT NOT NULL UNIQUE                        -- Internal. Lowercase name used to address the team.
             CHECK (slug = lower(slug)),
    name     TEXT NOT NULL,                              -- Internal. Display name.
    created  INTEGER NOT NULL,                           -- Internal.
    archived INTEGER                                     -- Internal. Non-NULL means archived.
);

CREATE TABLE memberships (
    team_id TEXT NOT NULL REFERENCES teams(id),          -- Internal.
    user_id TEXT NOT NULL REFERENCES users(id),          -- Internal.
    role    TEXT NOT NULL CHECK (role IN ('admin', 'member')), -- Internal.
    added   INTEGER NOT NULL,                            -- Internal.
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX memberships_by_user ON memberships(user_id);

CREATE TABLE keys (
    id      TEXT PRIMARY KEY,                            -- Internal. Opaque "k_" + random.
    user_id TEXT NOT NULL REFERENCES users(id),          -- Internal.
    label   TEXT NOT NULL,                               -- Personal (free text typed by a person).
    hash    BLOB NOT NULL UNIQUE                         -- Credential. SHA-256 of the full key. The UNIQUE index serves Authenticate.
            CHECK (length(hash) = 32),
    created INTEGER NOT NULL,                            -- Internal.
    expires INTEGER NOT NULL,                            -- Internal. Keys always expire.
    revoked INTEGER                                      -- Internal. Non-NULL means revoked.
);
CREATE INDEX keys_by_user ON keys(user_id);

-- One git remote per vault. vault_id is a users.id (personal vault) or a
-- teams.id (team vault); it is polymorphic, so existence is checked in code
-- rather than by a foreign key, and erasure deletes the personal row.
CREATE TABLE vault_remotes (
    vault_id        TEXT PRIMARY KEY,                    -- Internal.
    url_enc         BLOB,                                -- Personal (a remote URL can embed a person's account name). AES-256-GCM; NULL when no remote is set.
    public_key      TEXT NOT NULL,                       -- Internal. OpenSSH authorized_keys line; meant to be shown.
    private_key_enc BLOB NOT NULL,                       -- Credential. OpenSSH PEM private key, AES-256-GCM, nonce-prefixed.
    created         INTEGER NOT NULL,                    -- Internal.
    updated         INTEGER NOT NULL,                    -- Internal.
    set_by          TEXT NOT NULL                        -- Internal. users.id of whoever last changed it.
);
`,
}

// migrate brings the database to the current schema inside one transaction,
// so a failure leaves it exactly as it was and Open reports the error.
func (s *Store) migrate(ctx context.Context) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
			return fmt.Errorf("create schema_version: %w", err)
		}

		var current int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		if current > len(migrations) {
			return fmt.Errorf("accounts database is schema version %d, newer than this build supports (%d)",
				current, len(migrations))
		}
		if current == len(migrations) {
			return nil
		}

		for i := current; i < len(migrations); i++ {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("apply accounts migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_version`); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version) VALUES (?)`, len(migrations)); err != nil {
			return fmt.Errorf("record schema version: %w", err)
		}
		return nil
	})
}
