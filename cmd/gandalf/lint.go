package main

import (
	"flag"
	"fmt"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// lint validates a vault and reports findings on stdout.
func lint(args []string) error {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")
	strict := fs.Bool("strict", false, "treat warnings as failures")
	if err := fs.Parse(args); err != nil {
		return err
	}

	v, err := vault.Open(*root)
	if err != nil {
		return err
	}

	findings, err := v.Lint(fs.Args()...)
	if err != nil {
		return err
	}

	var errors, warnings int
	for _, f := range findings {
		fmt.Println(f)
		if f.Severity == schema.SeverityError {
			errors++
		} else {
			warnings++
		}
	}

	switch {
	case len(findings) == 0:
		fmt.Println("no findings")
		return nil
	case errors > 0:
		return fmt.Errorf("%d error(s), %d warning(s)", errors, warnings)
	case *strict:
		return fmt.Errorf("%d warning(s)", warnings)
	default:
		return nil
	}
}
