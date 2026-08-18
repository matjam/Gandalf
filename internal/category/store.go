package category

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StorePath is where a vault's categories are declared, relative to its root.
// It sits beside the seed ledger in a dot-directory: it is structural
// machinery rather than memory, and listing or linting it as a note would be
// noise.
const StorePath = ".gandalf/categories.json"

// Load reads a vault's categories, falling back to the shipped defaults when
// the vault has none. The defaults are not written on read; seeding does that,
// so a vault only gains the file once something has been established.
func Load(root string) (*Set, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StorePath)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Defaults(), nil
	case err != nil:
		return nil, fmt.Errorf("read categories: %w", err)
	}

	set := &Set{}
	if err := json.Unmarshal(data, set); err != nil {
		// Falling back to defaults here would quietly refile every note in a
		// vault whose categories had been customised.
		return nil, fmt.Errorf("parse categories at %s: %w", StorePath, err)
	}
	if err := set.Validate(); err != nil {
		return nil, fmt.Errorf("categories at %s: %w", StorePath, err)
	}

	return set, nil
}

// Save writes a vault's categories.
func Save(root string, set *Set) error {
	if err := set.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("encode categories: %w", err)
	}
	data = append(data, '\n')

	abs := filepath.Join(root, filepath.FromSlash(StorePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create category directory: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("write categories: %w", err)
	}

	return nil
}

// Exists reports whether a vault has declared its own categories.
func Exists(root string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(StorePath)))
	return err == nil
}
