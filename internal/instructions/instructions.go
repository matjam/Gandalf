// Package instructions holds GandalfOS: the operating contract, topics, and
// standards that ship inside the binary and are seeded into a vault the first
// time it is used.
//
// Seeding is a starting point, not a source of truth. Once a document is in
// the vault the vault owns it, because a correction the user makes has to
// survive the next release or the correction protocol is theatre. Drift from
// the shipped defaults is reported; it is never repaired.
package instructions

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
)

// Version is the revision of the shipped instruction set, stamped into every
// seeded document to record what wrote it.
//
// It tracks releases, not edits. Drift is detected by comparing content
// fingerprints, which is exact and needs no help; bumping this on every change
// to the wording would be churn that told doctor nothing it did not already
// know.
const Version = 1

// Frontmatter keys Gandalf writes into seeded documents to track their origin.
// They are unmanaged keys as far as the note schema is concerned, so they are
// preserved verbatim on every write.
const (
	KeyVersion = "gandalf-version"
	KeySeed    = "gandalf-seed"
)

//go:embed content
var content embed.FS

// Role says how a document reaches the model.
type Role string

const (
	// RoleCore is returned by boot, in full, at the start of every session.
	RoleCore Role = "core"

	// RoleTopic is fetched on demand when the work touches its subject.
	RoleTopic Role = "topic"

	// RoleReference is written for humans reading the vault; the model reads
	// it only if something points there.
	RoleReference Role = "reference"
)

// Doc is one shipped document: where it comes from, where it goes, and when
// the model should read it.
type Doc struct {
	// ID is the stable name a topic is requested by.
	ID string

	// Source is the path within the embedded content tree.
	Source string

	// Path is where the document is written in the vault.
	Path string

	Title string
	Type  schema.NoteType
	Role  Role
	Tags  []string

	// Related are wikilink targets, without delimiters.
	Related []string

	// When describes the work that should send the model here. It is what the
	// boot payload's topic table shows, so it is written for a reader deciding
	// whether to spend a tool call.
	When string
}

// Body returns the document's shipped content.
func (d Doc) Body() (string, error) {
	data, err := content.ReadFile(d.Source)
	if err != nil {
		return "", fmt.Errorf("read shipped document %q: %w", d.ID, err)
	}
	return string(data), nil
}

// Hash returns the fingerprint of the document's shipped content, used to tell
// a vault copy that was edited from one that was not.
func (d Doc) Hash() (string, error) {
	body, err := d.Body()
	if err != nil {
		return "", err
	}
	return HashBody(body), nil
}

// HashBody fingerprints note content. Leading and trailing whitespace is
// ignored so that the normalisation applied when a note is written does not
// register as an edit by the user.
func HashBody(body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:])[:16]
}

// Docs returns the shipped instruction set in seeding order.
func Docs() []Doc { return docs }

// Lookup returns the shipped document with the given ID.
func Lookup(id string) (Doc, bool) {
	for _, d := range docs {
		if d.ID == id {
			return d, true
		}
	}
	return Doc{}, false
}

// Topics returns the documents a model can fetch on demand, in the order they
// should be offered.
func Topics() []Doc {
	var out []Doc
	for _, d := range docs {
		if d.Role == RoleTopic {
			out = append(out, d)
		}
	}
	return out
}

// Core returns the documents returned in full at session start.
func Core() []Doc {
	var out []Doc
	for _, d := range docs {
		if d.Role == RoleCore {
			out = append(out, d)
		}
	}
	return out
}
