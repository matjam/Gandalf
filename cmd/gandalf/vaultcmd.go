package main

import (
	"flag"
	"fmt"

	"github.com/matjam/gandalf/internal/git"
	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// initVault creates the vault if needed and seeds any missing documents.
func initVault(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")
	restore := fs.Bool("restore", false, "re-add documents that were deleted")
	remote := fs.String("git-remote", "", "optional git remote URL to configure on the vault")
	noGit := fs.Bool("no-git", false, "do not create or maintain a git repository")
	if err := fs.Parse(args); err != nil {
		return err
	}

	v, err := vault.Open(*root)
	if err != nil {
		return err
	}

	results, err := instructions.Seed(v, schema.Today(), *restore)
	if err != nil {
		return err
	}

	for _, r := range results {
		if r.Created {
			fmt.Println("created", r.Doc.Path)
		}
	}

	created, removed := instructions.Created(results), instructions.Removed(results)
	fmt.Printf("\n%s: %d created, %d already present (GandalfOS v%d)\n",
		v.Root(), created, len(results)-created-removed, instructions.Version)

	if removed > 0 {
		fmt.Printf("%d document(s) you deleted were left out; `gandalf init -restore` puts them back\n", removed)
	}

	if !*noGit {
		if *remote != "" {
			repo := git.Open(v.Root())
			if _, err := repo.SetRemote(*remote); err != nil {
				return err
			}
			fmt.Println("git remote:", *remote)
		} else if err := ensureVaultGit(v); err != nil {
			return err
		}
	}

	return nil
}

// updateVault adopts this build's text for documents the user has not edited.
func updateVault(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")
	if err := fs.Parse(args); err != nil {
		return err
	}

	v, err := vault.Open(*root)
	if err != nil {
		return err
	}

	results, err := instructions.Update(v, schema.Today())
	if err != nil {
		return err
	}

	// Documents Update leaves alone still name the tools of the release that
	// seeded them. Rewriting just the identifiers keeps an edited document
	// pointing at tools that exist, without adopting text over the user's
	// edits.
	renamed, err := instructions.RenameTools(v, schema.Today())
	if err != nil {
		return err
	}

	for _, r := range results {
		if r.Updated {
			fmt.Println("updated", r.Doc.Path)
		}
	}
	for _, path := range renamed {
		fmt.Println("renamed tools in", path)
	}

	held := instructions.HeldBack(results)
	for _, r := range held {
		fmt.Printf("kept     %-12s %s\n", r.State, r.Doc.Path)
	}

	fmt.Printf("\n%s: %d updated, %d kept because you had edited them (GandalfOS v%d)\n",
		v.Root(), instructions.Updated(results), len(held), instructions.Version)

	if len(held) > 0 {
		fmt.Println("compare those against this build by hand; merging an edit is your call, not the tool's")
	}

	commitVault(v, "gandalf: update shipped documents")
	return nil
}

// doctor reports how the vault's documents compare with this build.
func doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")
	if err := fs.Parse(args); err != nil {
		return err
	}

	v, err := vault.Open(*root)
	if err != nil {
		return err
	}

	statuses, err := instructions.Doctor(v)
	if err != nil {
		return err
	}

	for _, s := range statuses {
		if s.State != instructions.StateCurrent {
			fmt.Println(s)
		}
	}

	fmt.Printf("\nGandalfOS v%d: %d current, %d modified, %d outdated, %d diverged, %d absent, %d removed, %d unmanaged\n",
		instructions.Version,
		instructions.Count(statuses, instructions.StateCurrent),
		instructions.Count(statuses, instructions.StateModified),
		instructions.Count(statuses, instructions.StateOutdated),
		instructions.Count(statuses, instructions.StateDiverged),
		instructions.Count(statuses, instructions.StateAbsent),
		instructions.Count(statuses, instructions.StateRemoved),
		instructions.Count(statuses, instructions.StateUnmanaged),
	)

	if n := instructions.Count(statuses, instructions.StateAbsent); n > 0 {
		fmt.Printf("run `gandalf init` to add %d missing document(s)\n", n)
	}
	if n := instructions.Count(statuses, instructions.StateRemoved); n > 0 {
		fmt.Printf("%d document(s) you deleted stay deleted; `gandalf init -restore` puts them back\n", n)
	}

	// Divergence is the point of the design, so it is reported and never
	// treated as failure. A broken repository is the opposite: nothing is
	// being committed or synced, and a doctor that exits cleanly over that is
	// a doctor nobody runs in a script.
	report, broken := gitReport(v.Root())
	fmt.Println()
	for _, line := range report {
		fmt.Println(line)
	}
	if broken {
		return git.ErrBroken
	}
	return nil
}

// gitReport describes the health of the vault's repository, one line to a
// sentence, and says whether it is broken.
//
// It looks at the repository from scratch rather than asking a running server,
// since doctor is a separate process, and uses the same check the server's sync
// tick does: HEAD's commit and tree must read, and no loose object may be empty.
// The empty ones are named, because deleting them is the usual repair and a
// repair needs names.
func gitReport(root string) (lines []string, broken bool) {
	repo := git.Open(root)
	health := repo.Inspect()

	switch health.State {
	case git.StateAbsent:
		return []string{"git: no repository here; `gandalf init` creates one"}, false

	case git.StateDisabled:
		return []string{"git: disabled for this vault in " + git.ConfigPath}, false

	case git.StateBroken:
		lines = append(lines,
			"git: BROKEN: "+health.Err.Error(),
			"git: changes are not being committed or synced while it is; notes are still written to disk",
			"git: repair or remove the damaged objects named above; a running server notices without a restart",
		)
		return lines, true
	}

	lines = append(lines, "git: healthy")
	if cfg, err := git.LoadConfig(root); err == nil && cfg.URL != "" && cfg.IsEnabled() {
		lines = append(lines, fmt.Sprintf("git: syncing with %s every %s", cfg.URL, cfg.Interval()))
	}
	return lines, false
}
