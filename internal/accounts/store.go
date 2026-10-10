package accounts

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the accounts database.
//
// All methods are safe for concurrent use. Writes serialise on SQLite's
// write lock (see Open), which is what lets invariants such as "never remove
// the last administrator" be checked and applied without a race.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD // nil when no secret key is configured
	log  *slog.Logger
	now  func() time.Time
}

// Option configures Open.
type Option func(*Store) error

// WithSecretKey supplies the 32-byte key that encrypts email addresses and
// the vault deploy keys' private halves. Without it the store works but
// refuses to hold any of them.
func WithSecretKey(key []byte) Option {
	return func(s *Store) error {
		aead, err := newAEAD(key)
		if err != nil {
			return err
		}
		s.aead = aead
		return nil
	}
}

// WithLogger sets where security events (key issued, key revoked, user
// erased, bootstrap) are logged. Only opaque ids are logged: never a handle,
// email, label or key. The default discards everything.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) error {
		if l != nil {
			s.log = l
		}
		return nil
	}
}

// Open opens (creating and migrating if necessary) the accounts database at
// path. The directory is created private (0700) and the database file is
// created 0600 before SQLite sees it: it holds credential hashes and
// encrypted personal data, and SQLite would otherwise create it with the
// process umask. Its -wal and -shm companions inherit the main file's mode.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	s := &Store{
		log: slog.New(slog.DiscardHandler),
		now: time.Now,
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create accounts directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create accounts database: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("create accounts database: %w", err)
	}

	// _txlock=immediate makes every transaction take SQLite's write lock at
	// BEGIN. A deferred transaction that reads and then writes can fail with
	// SQLITE_BUSY when another writer got in between (busy_timeout does not
	// help once a read snapshot is stale). Taking the lock up front serialises
	// writers, so a check-then-write sequence inside a transaction is atomic.
	// Plain reads outside transactions are unaffected and run concurrently
	// under WAL.
	//
	// foreign_keys is off by default in SQLite and per connection, so it is
	// set here, in the DSN, where every pooled connection gets it.
	dsn := "file:" + escapePath(path) +
		"?_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open accounts database: %w", err)
	}
	s.db = db

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open accounts database: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// escapePath makes a filesystem path safe inside a SQLite file: URI, where
// '?' and '#' would otherwise start the query and fragment.
func escapePath(p string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// querier is the part of *sql.DB and *sql.Tx the read helpers need, so the
// same query code runs inside or outside a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// inTx runs fn in a write transaction, committing if it returns nil and
// rolling back otherwise. fn's error is returned unwrapped so sentinel
// errors survive to the caller.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful commit

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
