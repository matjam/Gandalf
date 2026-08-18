package main

import (
	"flag"
	"fmt"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// initVault creates the vault if needed and seeds any missing documents.
func initVault(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")
	restore := fs.Bool("restore", false, "re-add documents that were deleted")
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
	// treated as failure.
	return nil
}
