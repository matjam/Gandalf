package main

import (
	"fmt"
	"os"

	"github.com/matjam/gandalf/internal/git"
	"github.com/matjam/gandalf/internal/vault"
)

// ensureVaultGit creates the vault's git repository when missing and commits
// the current tree. Failures are reported but do not undo whatever the caller
// already wrote: losing a commit is recoverable, losing a note is not.
func ensureVaultGit(v *vault.Vault) error {
	repo := git.Open(v.Root())
	if err := repo.Ensure(); err != nil {
		return fmt.Errorf("git: %w", err)
	}
	return nil
}

// commitVault records the vault's current tree when it is a git repository.
// A missing or disabled repo is a silent no-op.
func commitVault(v *vault.Vault, message string) {
	repo := git.Open(v.Root())
	if err := repo.Commit(message); err != nil {
		fmt.Fprintf(os.Stderr, "gandalf: git commit: %v\n", err)
	}
}
