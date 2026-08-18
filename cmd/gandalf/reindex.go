package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/matjam/gandalf/internal/index"
	"github.com/matjam/gandalf/internal/server"
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

	// Progress is on by default. Embedding a vault on a CPU is minutes of
	// work, and a command that prints nothing for minutes is one you cannot
	// tell from a hung one — which is exactly how this command was first
	// experienced.
	quiet := fs.Bool("quiet", false, "print only the summary")
	all := fs.Bool("all", false, "report every note, including ones already up to date")

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

	if !*quiet {
		fmt.Printf("reindexing %s with %s\n", v.Root(), embedder.Model())
	}

	started := time.Now()
	var embedTime time.Duration

	// The same namer the tools use, so an index built here addresses notes the
	// way a session will ask for them.
	report, err := index.ReindexWith(context.Background(), v, store, embedder,
		func(path string) string { return server.CanonicalRef(v, path).String() },
		func(ev index.Event) {
			if ev.Outcome == index.Embedded {
				embedTime += ev.Elapsed
			}
			if *quiet {
				return
			}
			if ev.Outcome != index.Embedded && !*all {
				// Unchanged notes are the common case and cost nothing. Naming
				// every one of them buries the ones actually being worked on.
				return
			}
			line := fmt.Sprintf("[%*d/%d] %-9s %s",
				digits(ev.Total), ev.Done, ev.Total, ev.Outcome, ev.Ref)
			if ev.Outcome == index.Embedded {
				line += fmt.Sprintf("  (%d chunk(s), %s)", ev.Chunks, round(ev.Elapsed))
			}
			fmt.Println(line)
		})
	if err != nil {
		// Report what landed before the failure: the pass is resumable, so how
		// far it got is the useful part of the error.
		fmt.Fprintf(os.Stderr, "reindex stopped after %s (%d indexed, %d unchanged)\n",
			round(time.Since(started)), report.Indexed, report.Unchanged)
		return err
	}

	total := time.Since(started)

	fmt.Printf("\n%s: %d indexed, %d unchanged, %d removed, %d chunks total (%s)\n",
		v.Root(), report.Indexed, report.Unchanged, report.Removed, report.Chunks, embedder.Model())
	fmt.Printf("took %s", round(total))
	if report.Indexed > 0 {
		fmt.Printf(", %s embedding (%s per indexed note)",
			round(embedTime), round(embedTime/time.Duration(report.Indexed)))
	}
	fmt.Println()

	return nil
}

// digits is how wide a counter has to be to hold n, so the progress lines stay
// in column.
func digits(n int) int {
	width := 1
	for n >= 10 {
		n /= 10
		width++
	}
	return width
}

// round trims a duration to something worth reading. Sub-second precision
// matters for one note; it is noise for a run that took four minutes.
func round(d time.Duration) time.Duration {
	switch {
	case d >= time.Minute:
		return d.Round(time.Second)
	case d >= time.Second:
		return d.Round(10 * time.Millisecond)
	default:
		return d.Round(time.Millisecond)
	}
}
