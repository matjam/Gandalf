package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/matjam/gandalf/internal/embed"
)

// Path is where a vault's index lives, relative to its root. It sits beside
// the other machinery in the dot-directory, and is excluded from version
// control by the .gitignore seeded there: it is derived from the notes,
// specific to one embedding model, and rebuildable at any time.
const Path = ".gandalf/index.db"

// schemaVersion is bumped when the tables change.
//
// The index is a cache, so a version mismatch drops and rebuilds rather than
// migrating. Migrating derived data is work in service of nothing: the notes
// still say what they said, and rebuilding is how correctness is restored
// anyway.
const schemaVersion = 1

// Store is a vault's search index.
type Store struct {
	db *sql.DB

	// model and dims record what produced the stored vectors, so a changed
	// embedder is detected instead of compared against.
	model string
	dims  int
}

// Open returns the index for a vault, creating or rebuilding it as needed.
func Open(root string, embedder embed.Embedder) (*Store, error) {
	abs := filepath.Join(root, filepath.FromSlash(Path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create index directory: %w", err)
	}

	db, err := sql.Open("sqlite", "file:"+abs+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}

	s := &Store{db: db, model: embedder.Model(), dims: embedder.Dims()}
	if err := s.prepare(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close releases the index.
func (s *Store) Close() error { return s.db.Close() }

// prepare creates the schema, discarding an index built by another schema
// version or another embedding model.
func (s *Store) prepare() error {
	var (
		version int
		model   string
		dims    int
	)

	row := s.db.QueryRow(`SELECT version, model, dims FROM meta LIMIT 1`)
	switch err := row.Scan(&version, &model, &dims); {
	case err == nil && version == schemaVersion && model == s.model && dims == s.dims:
		return nil
	case err == nil:
		// Vectors from a different model are not comparable with ours, and a
		// silent comparison would return confident nonsense.
		if err := s.drop(); err != nil {
			return err
		}
	}

	return s.create()
}

// drop removes the index's tables.
func (s *Store) drop() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS chunks`,
		`DROP TABLE IF EXISTS chunks_fts`,
		`DROP TABLE IF EXISTS meta`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("reset index: %w", err)
		}
	}
	return nil
}

// create builds the schema.
func (s *Store) create() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			version INTEGER NOT NULL,
			model   TEXT    NOT NULL,
			dims    INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id        INTEGER PRIMARY KEY,
			ref       TEXT NOT NULL,
			path      TEXT NOT NULL,
			heading   TEXT NOT NULL,
			title     TEXT NOT NULL,
			text      TEXT NOT NULL,
			hash      TEXT NOT NULL,
			embedding BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS chunks_by_path ON chunks(path)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			text,
			content='chunks',
			content_rowid='id'
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("create index schema: %w", err)
		}
	}

	if _, err := s.db.Exec(`DELETE FROM meta`); err != nil {
		return fmt.Errorf("reset index metadata: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO meta (version, model, dims) VALUES (?, ?, ?)`,
		schemaVersion, s.model, s.dims); err != nil {
		return fmt.Errorf("record index metadata: %w", err)
	}

	return nil
}

// Hashes returns the chunk fingerprints currently stored for a note, so a
// caller can tell whether it needs re-embedding.
func (s *Store) Hashes(path string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT hash FROM chunks WHERE path = ?`, path)
	if err != nil {
		return nil, fmt.Errorf("read chunk hashes: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out[hash] = true
	}
	return out, rows.Err()
}

// Paths returns every note path with chunks in the index.
func (s *Store) Paths() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT path FROM chunks`)
	if err != nil {
		return nil, fmt.Errorf("read indexed paths: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out[path] = true
	}
	return out, rows.Err()
}

// Replace swaps all the chunks stored for a note, in one transaction, so a
// failure part-way cannot leave a note half-indexed.
func (s *Store) Replace(ctx context.Context, path string, chunks []Chunk, vectors []embed.Vector) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("have %d chunks and %d vectors", len(chunks), len(vectors))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin index write: %w", err)
	}
	defer tx.Rollback()

	if err := deleteChunks(tx, path); err != nil {
		return err
	}

	for i, c := range chunks {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO chunks (ref, path, heading, title, text, hash, embedding)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.Ref, c.Path, c.Heading, c.Title, c.Text, c.Hash, embed.Encode(vectors[i]))
		if err != nil {
			return fmt.Errorf("store chunk: %w", err)
		}

		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("store chunk: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chunks_fts (rowid, text) VALUES (?, ?)`, id, c.Text); err != nil {
			return fmt.Errorf("store chunk text: %w", err)
		}
	}

	return tx.Commit()
}

// Forget removes a note's chunks, for a note that has been deleted.
func (s *Store) Forget(path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin index write: %w", err)
	}
	defer tx.Rollback()

	if err := deleteChunks(tx, path); err != nil {
		return err
	}
	return tx.Commit()
}

// Count returns how many chunks are indexed.
func (s *Store) Count() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count chunks: %w", err)
	}
	return n, nil
}

// deleteChunks removes a note's rows from both tables.
func deleteChunks(tx *sql.Tx, path string) error {
	if _, err := tx.Exec(
		`DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE path = ?)`, path); err != nil {
		return fmt.Errorf("clear chunk text: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chunks WHERE path = ?`, path); err != nil {
		return fmt.Errorf("clear chunks: %w", err)
	}
	return nil
}
