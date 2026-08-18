package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/matjam/gandalf/internal/git"
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
	noGit := fs.Bool("no-git", false, "do not create or maintain a git repository")

	var embedding embedderFlags
	embedding.register(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Configuration is checked before anything is written: a mistyped flag
	// should not leave a seeded vault behind on its way to an error.
	embedder, err := embedding.build()
	if err != nil {
		return err
	}

	v, repo, err := prepare(*root, !*noSeed, !*noGit)
	if err != nil {
		return err
	}

	// The endpoint is not contacted here. Search reports its own failure when
	// called, so an unreachable model never stops a session starting.
	srv := server.WithSearch(v, version, embedder)
	if repo != nil {
		srv = srv.WithGit(repo)
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if repo != nil {
		repo.StartSync(ctx)
	}

	return srv.Run(ctx)
}

// prepare opens the vault the server will serve, seeding it unless told not
// to. Seeding on startup is what makes "point it at a directory" a complete
// install step; it only ever adds what is missing. Git maintenance is set up
// the same way: a fresh vault becomes a repository without a separate step.
func prepare(root string, seed, useGit bool) (*vault.Vault, *git.Repo, error) {
	if root == "" {
		return nil, nil, fmt.Errorf("serve needs -vault; the server will not guess where your notes live")
	}

	v, err := vault.Open(root)
	if err != nil {
		return nil, nil, err
	}

	if seed {
		// Serving never restores deleted documents: a server starting up is not
		// the moment to reverse a decision the user made.
		results, err := instructions.Seed(v, schema.Today(), false)
		if err != nil {
			return nil, nil, err
		}
		// stdout carries the protocol, so anything human-readable goes to stderr.
		if n := instructions.Created(results); n > 0 {
			fmt.Fprintf(os.Stderr, "gandalf: seeded %d document(s) into %s\n", n, v.Root())
		}

		// A vault seeded by an older release names tools this build no longer
		// offers. Nothing else will fix that for an edited document, and a
		// contract naming tools that do not exist is worse than useless: it
		// tells the model to do something it cannot do.
		renamed, err := instructions.RenameTools(v, schema.Today())
		if err != nil {
			return nil, nil, err
		}
		if len(renamed) > 0 {
			fmt.Fprintf(os.Stderr, "gandalf: updated tool names in %d edited document(s)\n", len(renamed))
		}
	}

	if !useGit {
		return v, nil, nil
	}

	repo := git.Open(v.Root())
	if err := repo.Ensure(); err != nil {
		fmt.Fprintf(os.Stderr, "gandalf: git: %v\n", err)
		return v, nil, nil
	}
	return v, repo, nil
}
