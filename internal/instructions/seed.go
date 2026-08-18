package instructions

import (
	"fmt"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// SeedResult records what happened to one document during seeding.
type SeedResult struct {
	Doc     Doc
	Created bool

	// Reason explains a skip.
	Reason string
}

// Seed writes any shipped document the vault does not already have, and leaves
// everything else alone.
//
// It never overwrites. A document already present is the user's, whether they
// edited it, replaced it, or seeded it from an earlier version — deciding
// otherwise would mean a release could silently revert a correction.
func Seed(v *vault.Vault, on schema.Date) ([]SeedResult, error) {
	if on.IsZero() {
		on = schema.Today()
	}

	results := make([]SeedResult, 0, len(docs))
	for _, doc := range docs {
		if v.Exists(doc.Path) {
			results = append(results, SeedResult{
				Doc:    doc,
				Reason: "already present",
			})
			continue
		}

		note, err := build(v, doc, on)
		if err != nil {
			return nil, err
		}
		if err := v.Write(note); err != nil {
			return nil, fmt.Errorf("seed %q: %w", doc.Path, err)
		}

		results = append(results, SeedResult{Doc: doc, Created: true})
	}

	return results, nil
}

// build turns a shipped document into a note, stamped with the version and
// content fingerprint that let doctor tell later what has changed since.
func build(v *vault.Vault, doc Doc, on schema.Date) (*vault.Note, error) {
	body, err := doc.Body()
	if err != nil {
		return nil, err
	}

	note, err := v.NewNote(vault.NewNoteRequest{
		Type:    doc.Type,
		Title:   doc.Title,
		Path:    doc.Path,
		Tags:    doc.Tags,
		Related: doc.Related,
		Author:  schema.AuthorAgent,
		Status:  schema.StatusComplete,
		Body:    body,
		On:      on,
	})
	if err != nil {
		return nil, fmt.Errorf("build %q: %w", doc.Path, err)
	}

	note.FM.Extra = map[string]any{
		KeyVersion: Version,
		KeySeed:    HashBody(body),
	}

	return note, nil
}

// Created counts the documents a seeding run wrote.
func Created(results []SeedResult) int {
	var n int
	for _, r := range results {
		if r.Created {
			n++
		}
	}
	return n
}
