package instructions

import (
	"fmt"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// UpdateResult records what happened to one document during an update.
type UpdateResult struct {
	Doc     Doc
	State   State
	Updated bool
}

// Update adopts this build's text for every document the vault still holds
// exactly as it was seeded.
//
// Seeding never overwrites, because a release able to revert a correction
// would make the vault's authority over the binary a fiction. That reasoning
// covers a document the user has changed, and only that: a document in
// StateOutdated has content identical to what was written when it was seeded,
// so there is no edit to revert and the vault is simply behind a release.
// Modified and diverged documents are left alone and reported, since merging
// an edit is the user's call.
//
// Without this the gap is permanent, and it is not cosmetic: gandalf_boot
// serves the vault's copy, so an agent goes on reading instructions that
// describe a tool surface the binary no longer has.
//
// Frontmatter the user may have edited is preserved. Only the body, the
// version stamp and the fingerprint change.
func Update(v *vault.Vault, on schema.Date) ([]UpdateResult, error) {
	if on.IsZero() {
		on = schema.Today()
	}

	statuses, err := Doctor(v)
	if err != nil {
		return nil, err
	}

	results := make([]UpdateResult, 0, len(statuses))
	var wrote bool

	for _, s := range statuses {
		result := UpdateResult{Doc: s.Doc, State: s.State}

		if s.State == StateOutdated {
			if err := adopt(v, s.Doc, on); err != nil {
				return nil, err
			}
			result.Updated = true
			wrote = true
		}

		results = append(results, result)
	}

	if wrote {
		// An adopted document may link where its previous version did not,
		// and it was written straight to disk rather than through the tools,
		// so nothing has maintained the backlinks.
		if _, err := v.RebuildBacklinks(); err != nil {
			return nil, err
		}
	}

	return results, nil
}

// adopt replaces one document's body with this build's, keeping the note's own
// metadata, its created date, and its maintained backlinks block.
func adopt(v *vault.Vault, doc Doc, on schema.Date) error {
	body, err := doc.Body()
	if err != nil {
		return err
	}

	note, err := v.Read(doc.Path)
	if err != nil {
		return fmt.Errorf("update %q: %w", doc.Path, err)
	}
	if _, err := note.ReplaceContent(body); err != nil {
		return fmt.Errorf("update %q: %w", doc.Path, err)
	}

	if note.FM.Extra == nil {
		note.FM.Extra = map[string]any{}
	}
	note.FM.Extra[KeyVersion] = Version
	note.FM.Extra[KeySeed] = HashBody(body)
	note.Touch(on)

	if err := v.Write(note); err != nil {
		return fmt.Errorf("update %q: %w", doc.Path, err)
	}

	return nil
}

// Updated counts the documents an update run rewrote.
func Updated(results []UpdateResult) int {
	var n int
	for _, r := range results {
		if r.Updated {
			n++
		}
	}
	return n
}

// HeldBack returns the documents an update deliberately left alone because the
// user had changed them.
func HeldBack(results []UpdateResult) []UpdateResult {
	var out []UpdateResult
	for _, r := range results {
		if r.State == StateModified || r.State == StateDiverged {
			out = append(out, r)
		}
	}
	return out
}
