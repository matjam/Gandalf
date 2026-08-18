package instructions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/matjam/gandalf/internal/category"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// SeedResult records what happened to one document during seeding.
type SeedResult struct {
	Doc     Doc
	Created bool

	// Reason explains a skip.
	Reason string
}

// Reasons a document was left alone.
const (
	ReasonPresent = "already present"
	ReasonRemoved = "removed by the user"
)

// Seed writes any shipped document the vault has never had, and leaves
// everything else alone.
//
// It never overwrites, and it never restores. A document already present is
// the user's, whether they edited it or seeded it from an earlier release; a
// document they deleted is a decision, and putting it back would revert that
// decision as surely as overwriting an edit would. Either would make the
// vault's authority over the binary a fiction.
//
// Passing restore re-adds documents that were deleted, for the case where that
// is what the user actually wants.
func Seed(v *vault.Vault, on schema.Date, restore bool) ([]SeedResult, error) {
	if on.IsZero() {
		on = schema.Today()
	}

	// Write the categories out on first use. Loading falls back to the
	// defaults in memory, but a file nobody can see is not something a user
	// can edit, and editing it is the whole point of categories being data.
	if !category.Exists(v.Root()) {
		if err := v.SetCategories(v.Categories()); err != nil {
			return nil, err
		}
	}

	if err := writeMachineryIgnore(v); err != nil {
		return nil, err
	}

	ledger, err := LoadLedger(v)
	if err != nil {
		return nil, err
	}

	results := make([]SeedResult, 0, len(docs))
	var wrote bool

	for _, doc := range docs {
		switch {
		case v.Exists(doc.Path):
			// Record documents that predate the ledger, so deleting one later
			// is recognised as a deletion rather than treated as never seeded.
			if !ledger.Records(doc.ID) {
				ledger.Record(doc.ID, on)
				wrote = true
			}
			results = append(results, SeedResult{Doc: doc, Reason: ReasonPresent})
			continue

		case ledger.Records(doc.ID) && !restore:
			results = append(results, SeedResult{Doc: doc, Reason: ReasonRemoved})
			continue
		}

		note, err := build(v, doc, on)
		if err != nil {
			return nil, err
		}
		if err := v.Write(note); err != nil {
			return nil, fmt.Errorf("seed %q: %w", doc.Path, err)
		}

		ledger.Record(doc.ID, on)
		wrote = true
		results = append(results, SeedResult{Doc: doc, Created: true})
	}

	if wrote {
		if err := ledger.Save(v); err != nil {
			return nil, err
		}
	}

	return results, nil
}

// machineryIgnore keeps derived files out of version control while leaving the
// declarations in it.
//
// Categories and the seed ledger must travel with the vault: a category added
// on one machine has to exist on the next, and a document deleted on one must
// stay deleted. The search index must not travel — it is derived from the
// notes, specific to one embedding model, and rebuildable in seconds.
const machineryIgnore = `# Derived data. The declarations beside it are meant to be committed.
index.db
index.db-wal
index.db-shm
`

// writeMachineryIgnore places the ignore file, leaving an existing one alone
// in case the user has edited it.
func writeMachineryIgnore(v *vault.Vault) error {
	abs := filepath.Join(v.Root(), ".gandalf", ".gitignore")
	if _, err := os.Stat(abs); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create machinery directory: %w", err)
	}
	if err := os.WriteFile(abs, []byte(machineryIgnore), 0o644); err != nil {
		return fmt.Errorf("write machinery ignore file: %w", err)
	}
	return nil
}

// build turns a shipped document into a note, stamped with the version and
// content fingerprint that let doctor tell later what has changed since.
func build(v *vault.Vault, doc Doc, on schema.Date) (*vault.Note, error) {
	body, err := doc.Body()
	if err != nil {
		return nil, err
	}

	note, err := v.NewNote(vault.NewNoteRequest{
		Type:    doc.Type,
		Title:   doc.Title,
		Path:    doc.Path,
		Tags:    doc.Tags,
		Related: doc.Related,
		Author:  schema.AuthorAgent,
		Status:  schema.StatusComplete,
		Body:    body,
		On:      on,
	})
	if err != nil {
		return nil, fmt.Errorf("build %q: %w", doc.Path, err)
	}

	note.FM.Extra = map[string]any{
		KeyVersion: Version,
		KeySeed:    HashBody(body),
	}

	if doc.NoIndex {
		excluded := false
		note.FM.Index = &excluded
	}

	return note, nil
}

// Created counts the documents a seeding run wrote.
func Created(results []SeedResult) int {
	var n int
	for _, r := range results {
		if r.Created {
			n++
		}
	}
	return n
}

// Removed counts the documents left out because the user deleted them.
func Removed(results []SeedResult) int {
	var n int
	for _, r := range results {
		if r.Reason == ReasonRemoved {
			n++
		}
	}
	return n
}
