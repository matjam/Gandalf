package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/importer"
	"github.com/matjam/gandalf/internal/vault"
)

// importVault moves an existing markdown vault into a Gandalf vault.
func importVault(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	from := fs.String("from", "", "the vault to import from (required)")
	root := fs.String("vault", ".", "path to the destination vault root")
	rulesPath := fs.String("rules", "", "JSON rules mapping source paths to categories")
	apply := fs.Bool("apply", false, "write the import; without it, only the plan is printed")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *from == "" {
		return fmt.Errorf("import needs -from, the vault to read")
	}

	rules := &importer.Rules{}
	if *rulesPath != "" {
		loaded, err := importer.LoadRules(*rulesPath)
		if err != nil {
			return err
		}
		rules = loaded
	}

	src, err := vault.Open(*from)
	if err != nil {
		return err
	}
	dst, err := vault.Open(*root)
	if err != nil {
		return err
	}
	if src.Root() == dst.Root() {
		return fmt.Errorf("the source and destination are the same vault")
	}

	plan, err := importer.Build(src, dst, rules)
	if err != nil {
		return err
	}

	fmt.Print(plan.Summary())

	if !*apply {
		fmt.Println("\nnothing written. Re-run with -apply once the plan looks right")
		return nil
	}
	if len(plan.Moves) == 0 {
		return fmt.Errorf("nothing to import")
	}

	result, err := importer.Apply(dst, plan)
	if err != nil {
		return err
	}

	fmt.Printf("\nimported %d note(s), rewrote %d link(s)\n", result.Imported, result.Rewritten)
	if len(result.Dangling) > 0 {
		fmt.Printf("%d link target(s) were not part of the import and were left alone:\n  %s\n",
			len(result.Dangling), strings.Join(result.Dangling, "\n  "))
		fmt.Println("run `gandalf lint` to see which notes carry them")
	}

	return nil
}
