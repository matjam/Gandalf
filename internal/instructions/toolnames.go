package instructions

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// renamedTools maps the tool names Gandalf used to expose to the ones it does
// now. The prefix was redundant: a client already namespaces a server's tools,
// so gandalf_note_read read as gandalf/gandalf_note_read wherever it was shown.
//
// The map is kept rather than deleted because the vault outlives the binary.
// Documents seeded by an older release still name the old tools, and a
// document is only useful if what it tells the model to call exists.
var renamedTools = map[string]string{
	"gandalf_boot":            "boot",
	"gandalf_search":          "search",
	"gandalf_list":            "list",
	"gandalf_note_read":       "note_read",
	"gandalf_session_start":   "session_start",
	"gandalf_note_new":        "note_new",
	"gandalf_note_append":     "note_append",
	"gandalf_note_replace":    "note_replace",
	"gandalf_note_update":     "note_update",
	"gandalf_note_delete":     "note_delete",
	"gandalf_category_list":   "category_list",
	"gandalf_category_create": "category_create",
	"gandalf_category_retire": "category_retire",
	"gandalf_category_delete": "category_delete",
	"gandalf_lint":            "lint",
	"gandalf_correct":         "correct",
	"gandalf_git_remote":      "git_remote",
}

// oldToolName matches any of the retired names as a whole word.
//
// The rewrite runs in one direction only. Old names are unambiguous —
// gandalf_list appears nowhere by accident — while the new ones are ordinary
// English words, so a reverse pass over prose would maul it.
var oldToolName = regexp.MustCompile(`\bgandalf_(` + strings.Join(oldSuffixes(), "|") + `)\b`)

func oldSuffixes() []string {
	// Longest first, so note_read is not clipped to a shorter alternative that
	// happens to be its prefix.
	suffixes := make([]string, 0, len(renamedTools))
	for old := range renamedTools {
		suffixes = append(suffixes, strings.TrimPrefix(old, "gandalf_"))
	}
	for i := range suffixes {
		for j := i + 1; j < len(suffixes); j++ {
			if len(suffixes[j]) > len(suffixes[i]) {
				suffixes[i], suffixes[j] = suffixes[j], suffixes[i]
			}
		}
	}
	return suffixes
}

// RenameTools rewrites retired tool names in the shipped documents a vault
// holds, and reports which documents changed.
//
// It exists because Update deliberately will not touch a document the user has
// edited, which is right: adopting this build's text over a correction would
// make the vault's authority over the binary a fiction. But that leaves an
// edited document naming tools the binary no longer has, and a contract that
// tells the model to call gandalf_correct on a server offering correct is
// worse than either version on its own.
//
// So the two split the work. Update adopts whole bodies for documents with no
// local edits; this rewrites nothing but the identifiers, in exactly the
// documents Update declines to handle. Renaming a token cannot change what a
// sentence means, which is what makes it safe to do to prose somebody wrote.
//
// Only shipped documents are touched. A session note mentioning an old tool
// name is a record of what happened, not an instruction to follow, and
// rewriting it would edit history to no benefit.
func RenameTools(v *vault.Vault, on schema.Date) ([]string, error) {
	if on.IsZero() {
		on = schema.Today()
	}

	statuses, err := Doctor(v)
	if err != nil {
		return nil, err
	}

	var renamed []string
	for _, s := range statuses {
		// Anything Update can handle is left to Update: it will bring in a
		// body that already uses the current names.
		if s.State != StateModified && s.State != StateDiverged {
			continue
		}

		note, err := v.Read(s.Doc.Path)
		if err != nil {
			return nil, fmt.Errorf("rename tools in %q: %w", s.Doc.Path, err)
		}

		before := note.Body
		note.Body = oldToolName.ReplaceAllStringFunc(before, func(match string) string {
			return renamedTools[match]
		})
		if note.Body == before {
			continue
		}

		note.Touch(on)
		if err := v.Write(note); err != nil {
			return nil, fmt.Errorf("rename tools in %q: %w", s.Doc.Path, err)
		}
		renamed = append(renamed, s.Doc.Path)
	}

	return renamed, nil
}
