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
	case "serve":
		return serve(args[1:])
	case "init":
		return initVault(args[1:])
	case "update":
		return updateVault(args[1:])
	case "import":
		return importVault(args[1:])
	case "reindex":
		return reindex(args[1:])
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
  gandalf serve   -vault DIR [-http ADDR] [-no-seed] [-no-git] [embedding flags]
  gandalf init    [-vault DIR] [-restore] [-git-remote URL] [-no-git]
  gandalf update  [-vault DIR]
  gandalf import  -from DIR [-vault DIR] [-rules FILE] [-apply]
  gandalf reindex [-vault DIR] [embedding flags]
  gandalf doctor  [-vault DIR]
  gandalf lint    [-vault DIR] [-strict] [NOTE...]

embedding flags:
  -embed BACKEND    http (default) or none to disable search
  -embed-url URL    OpenAI-compatible endpoint (default http://localhost:11434/v1)
  -embed-model NAME embedding model (default nomic-embed-text)
  -embed-dims N     vector length the model returns (default 768)

  serve   run the MCP server over stdio, seeding the vault first unless
          -no-seed is given; maintains a git repo of the vault unless
          -no-git is given, committing every change and syncing a remote
          when one is configured. With -http ADDR it serves the same tools
          over HTTP instead, so agents on other machines can use one vault;
          that mode requires a bearer token in GANDALF_HTTP_TOKEN, and the
          address decides which interface the vault is reachable on
  import  move an existing markdown vault in, preserving dates and
          rewriting links; prints the plan and writes nothing without -apply
  reindex build the search index up front, rather than on the first search
  init    create the vault if needed and seed any missing GandalfOS
          documents; never overwrites an existing file; creates a git
          repository unless -no-git is given
  update  adopt this build's text for documents you have not edited,
          leaving edited and diverged ones alone
  doctor  report how the vault's copy of each shipped document compares
          with this build, without changing anything
  lint    validate note metadata and links; with no NOTE arguments,
          every note in the vault is checked
`)
}
