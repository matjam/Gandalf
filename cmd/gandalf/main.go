// Command gandalf manages an Obsidian-compatible markdown vault: it owns note
// metadata so agents do not have to, seeds the GandalfOS instruction set into
// a vault, and validates what is already there.
//
// The MCP server that exposes these operations to an agent is not built yet.
package main

import (
	"fmt"
	"os"
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
	case "init":
		return initVault(args[1:])
	case "doctor":
		return doctor(args[1:])
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

func usage() {
	fmt.Fprint(os.Stderr, `gandalf — vault tooling for agent memory

usage:
  gandalf init   [-vault DIR]
  gandalf doctor [-vault DIR]
  gandalf lint   [-vault DIR] [-strict] [NOTE...]

  init    create the vault if needed and seed any missing GandalfOS
          documents; never overwrites an existing file
  doctor  report how the vault's copy of each shipped document compares
          with this build, without changing anything
  lint    validate note metadata and links; with no NOTE arguments,
          every note in the vault is checked
`)
}
