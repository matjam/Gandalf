package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/server"
	"github.com/matjam/gandalf/internal/vault"
)

// version identifies this build to MCP clients. It is overridden at release
// time with -ldflags "-X main.version=...".
var version = "dev"

// serve runs the MCP server over stdio until the client disconnects.
func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	root := fs.String("vault", "", "path to the vault root (required)")
	noSeed := fs.Bool("no-seed", false, "do not seed missing GandalfOS documents on startup")
	if err := fs.Parse(args); err != nil {
		return err
	}

	v, err := prepare(*root, !*noSeed)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return server.New(v, version).Run(ctx)
}

// prepare opens the vault the server will serve, seeding it unless told not
// to. Seeding on startup is what makes "point it at a directory" a complete
// install step; it only ever adds what is missing.
func prepare(root string, seed bool) (*vault.Vault, error) {
	if root == "" {
		return nil, fmt.Errorf("serve needs -vault; the server will not guess where your notes live")
	}

	v, err := vault.Open(root)
	if err != nil {
		return nil, err
	}

	if !seed {
		return v, nil
	}

	// Serving never restores deleted documents: a server starting up is not
	// the moment to reverse a decision the user made.
	results, err := instructions.Seed(v, schema.Today(), false)
	if err != nil {
		return nil, err
	}
	// stdout carries the protocol, so anything human-readable goes to stderr.
	if n := instructions.Created(results); n > 0 {
		fmt.Fprintf(os.Stderr, "gandalf: seeded %d document(s) into %s\n", n, v.Root())
	}

	return v, nil
}
