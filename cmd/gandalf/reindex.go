package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/matjam/gandalf/internal/index"
	"github.com/matjam/gandalf/internal/vault"
)

// reindex brings the search index into line with the vault.
//
// Search reindexes on demand, so this exists for the cases where doing it
// up front is better than paying for it mid-session: after a bulk import, on a
// new machine, or after changing the embedding model.
func reindex(args []string) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	root := fs.String("vault", ".", "path to the vault root")

	var embedding embedderFlags
	embedding.register(fs)

	if err := fs.Parse(args); err != nil {
		return err
	}

	embedder, err := embedding.build()
	if err != nil {
		return err
	}
	if embedder == nil {
		return fmt.Errorf("reindex needs an embedding model; -embed none disables search entirely")
	}

	v, err := vault.Open(*root)
	if err != nil {
		return err
	}

	store, err := index.Open(v.Root(), embedder)
	if err != nil {
		return err
	}
	defer store.Close()

	report, err := index.Reindex(context.Background(), v, store, embedder, func(path string) string {
		return v.RefFor(path).String()
	})
	if err != nil {
		return err
	}

	fmt.Printf("%s: %d indexed, %d unchanged, %d removed, %d chunks total (%s)\n",
		v.Root(), report.Indexed, report.Unchanged, report.Removed, report.Chunks, embedder.Model())

	return nil
}
