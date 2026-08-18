package instructions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// LedgerPath is where the record of seeded documents lives, relative to the
// vault root. It sits in a dot-directory so the vault's own listing and
// linting skip it, and so it stays out of the way in an editor: it is
// machinery, not memory.
//
// It belongs inside the vault rather than in a machine-local state directory
// because it has to travel with the vault. A standard deleted on one machine
// must stay deleted on the next one to sync.
const LedgerPath = ".gandalf/seeded.json"

// Ledger records which documents have ever been seeded into a vault.
//
// Without it, seeding cannot tell a document that was never written from one
// the user deliberately deleted, and would restore the second — reverting a
// decision, which is the one thing seeding must never do.
type Ledger struct {
	// Seeded maps a document ID to the date it was first written.
	Seeded map[string]string `json:"seeded"`
}

// LoadLedger reads a vault's ledger, returning an empty one when the vault has
// never been seeded.
func LoadLedger(v *vault.Vault) (*Ledger, error) {
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(LedgerPath)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &Ledger{Seeded: map[string]string{}}, nil
	case err != nil:
		return nil, fmt.Errorf("read seed ledger: %w", err)
	}

	ledger := &Ledger{}
	if err := json.Unmarshal(data, ledger); err != nil {
		// A corrupt ledger must not be treated as "nothing was ever seeded":
		// that would restore every document the user had deleted.
		return nil, fmt.Errorf("parse seed ledger at %s: %w", LedgerPath, err)
	}
	if ledger.Seeded == nil {
		ledger.Seeded = map[string]string{}
	}

	return ledger, nil
}

// Save writes the ledger back to the vault.
func (l *Ledger) Save(v *vault.Vault) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encode seed ledger: %w", err)
	}
	data = append(data, '\n')

	abs := filepath.Join(v.Root(), filepath.FromSlash(LedgerPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("write seed ledger: %w", err)
	}

	return nil
}

// Records reports whether a document has been seeded before.
func (l *Ledger) Records(id string) bool {
	_, ok := l.Seeded[id]
	return ok
}

// Record notes that a document has been seeded.
func (l *Ledger) Record(id string, on schema.Date) {
	l.Seeded[id] = on.String()
}
