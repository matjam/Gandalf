package instructions

import (
	"fmt"

	"github.com/matjam/gandalf/internal/vault"
)

// State is what has happened to a shipped document since it was seeded.
type State string

const (
	// StateAbsent means the vault has no note at that path and never had one.
	// Either it was never seeded, or a later release added it.
	StateAbsent State = "absent"

	// StateRemoved means the document was seeded and the user then deleted it.
	// Seeding leaves it alone from then on, because deleting a document is a
	// decision in the same way editing one is.
	StateRemoved State = "removed"

	// StateUnmanaged means a note exists at that path with no seed stamp: the
	// user's own file, which Gandalf did not write and will not touch.
	StateUnmanaged State = "unmanaged"

	// StateCurrent means the vault copy matches what shipped.
	StateCurrent State = "current"

	// StateModified means the user edited it. This is the intended state, not
	// a fault.
	StateModified State = "modified"

	// StateOutdated means the shipped version changed and the vault copy has
	// no local edits, so adopting the new one would lose nothing.
	StateOutdated State = "outdated"

	// StateDiverged means both sides changed. Merging is the user's call.
	StateDiverged State = "diverged"
)

// Status is the result of examining one shipped document in a vault.
type Status struct {
	Doc   Doc
	State State

	// SeededVersion is the instruction-set version recorded in the note, or
	// zero when it carries no stamp.
	SeededVersion int
}

// String renders a status as a single line.
func (s Status) String() string {
	return fmt.Sprintf("%-12s %s", s.State, s.Doc.Path)
}

// Doctor reports how each shipped document stands in the vault. It only ever
// reads: divergence is the expected outcome of using the vault, and repairing
// it automatically would undo the user's corrections.
func Doctor(v *vault.Vault) ([]Status, error) {
	ledger, err := LoadLedger(v)
	if err != nil {
		return nil, err
	}

	statuses := make([]Status, 0, len(docs))
	for _, doc := range docs {
		status, err := inspect(v, doc, ledger)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

// inspect classifies a single document.
func inspect(v *vault.Vault, doc Doc, ledger *Ledger) (Status, error) {
	if !v.Exists(doc.Path) {
		if ledger.Records(doc.ID) {
			return Status{Doc: doc, State: StateRemoved}, nil
		}
		return Status{Doc: doc, State: StateAbsent}, nil
	}

	note, err := v.Read(doc.Path)
	if err != nil {
		return Status{}, fmt.Errorf("inspect %q: %w", doc.Path, err)
	}

	seeded, ok := stamp(note)
	if !ok {
		return Status{Doc: doc, State: StateUnmanaged}, nil
	}

	shipped, err := doc.Hash()
	if err != nil {
		return Status{}, err
	}

	// Fingerprint the note's own content, not the maintained backlinks block:
	// otherwise linking to a seeded standard would report it as modified by
	// the user, who did nothing.
	status := Status{Doc: doc, SeededVersion: version(note)}
	switch current := HashBody(note.Content()); {
	case current == seeded && seeded == shipped:
		status.State = StateCurrent
	case current == seeded:
		status.State = StateOutdated
	case seeded == shipped:
		status.State = StateModified
	default:
		status.State = StateDiverged
	}

	return status, nil
}

// stamp returns the fingerprint recorded when the note was seeded.
func stamp(n *vault.Note) (string, bool) {
	raw, ok := n.FM.Extra[KeySeed]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

// version returns the instruction-set version recorded in the note, or zero if
// it is missing or unreadable.
func version(n *vault.Note) int {
	switch v := n.FM.Extra[KeyVersion].(type) {
	case int:
		return v
	case uint64:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// Count returns how many statuses are in the given state.
func Count(statuses []Status, state State) int {
	var n int
	for _, s := range statuses {
		if s.State == state {
			n++
		}
	}
	return n
}
