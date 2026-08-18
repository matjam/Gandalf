// Command gandalf manages an Obsidian-compatible markdown vault: it owns note
// metadata so agents do not have to, and validates what is already there.
//
// The MCP server that exposes these operations to an agent is not built yet;
// for now the lint subcommand runs the same checks from a shell.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gandalf:", err)
		os.Exit(1)
	}
}

// run dispatches a subcommand. It returns an error for anything that should
// set a non-zero exit status, including lint findings.
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}

	switch cmd := args[0]; cmd {
	case "lint":
		return lint(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

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

func usage() {
	fmt.Fprint(os.Stderr, `gandalf — vault tooling for agent memory

usage:
  gandalf lint [-vault DIR] [-strict] [NOTE...]

  lint    validate note metadata and links; with no NOTE arguments,
          every note in the vault is checked
`)
}
