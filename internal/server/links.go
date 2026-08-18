package server

import (
	"fmt"
	"strings"

	"github.com/matjam/gandalf/internal/vault"
)

// Notes are addressed by ref at the tool boundary and by path on disk, so
// links are translated in both directions.
//
// The disk form is not negotiable: the vault is opened in Obsidian and kept in
// git, where a link has to be a vault-relative path or it resolves to nothing.
// The tool form is not negotiable either, since a model handed a path will
// start constructing paths. Translating at the boundary is what lets both be
// true at once — and it is the moment the target is known, so it is also where
// a link to a note that does not exist can be caught.

// links carries the result of translating a note's links on the way in.
type links struct {
	// Related are the frontmatter link targets, as vault paths.
	Related []string

	// Body is the note text with refs rewritten to paths.
	Body string

	// Missing are refs whose targets are not in the vault.
	Missing []string
}

// err returns the failure to report when links point at nothing.
func (l links) err() error {
	if len(l.Missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"these links point at notes that do not exist: %s. Create them first, or drop the links — "+
			"Gandalf will not write a link it knows is dead, and will not create an empty note to satisfy one",
		strings.Join(l.Missing, ", "))
}

// resolveLinks translates a note's incoming links and reports every target
// that is missing.
//
// Every missing target is collected rather than failing on the first, for the
// same reason the schema reports all its violations at once: one round trip
// per broken link is a waste of everybody's time.
func (s *Server) resolveLinks(related []string, body string) links {
	out := links{Body: body}

	for _, raw := range related {
		path, ok := s.linkPath(raw)
		switch {
		case path == "":
			// Not a ref at all; keep whatever the caller wrote and let lint
			// have an opinion about it.
			out.Related = append(out.Related, vault.LinkTarget(raw))
		case !ok:
			out.Missing = append(out.Missing, raw)
		default:
			out.Related = append(out.Related, path)
		}
	}

	out.Body = vault.RewriteWikilinks(body, func(target string) string {
		path, ok := s.linkPath(target)
		if path == "" {
			// Already a path, or not a ref: leave it exactly as written.
			return ""
		}
		if !ok {
			out.Missing = append(out.Missing, target)
			return ""
		}
		return path
	})

	return out
}

// isRef reports whether a wikilink target is a ref rather than a path or
// ordinary prose. Topics count: they are addressable even though no category
// declares them.
func (s *Server) isRef(target string) bool {
	if strings.HasPrefix(strings.TrimSpace(target), KindTopic+":") {
		return true
	}
	_, err := s.vault.ParseRef(target)
	return err == nil
}

// linkPath returns the vault path a ref addresses, without its extension, and
// whether that note exists. An empty path means the target was not a ref.
func (s *Server) linkPath(raw string) (string, bool) {
	if !s.isRef(raw) {
		return "", false
	}

	// Resolving also freezes aliases: a note saved with [[session:latest]]
	// would otherwise mean something different every time it was read.
	_, path, err := s.resolve(raw)
	if err != nil {
		return "", false
	}

	return strings.TrimSuffix(path, ".md"), s.vault.Exists(path)
}

// toRefs rewrites path links in body text to refs, so a model reading a note
// gets links it can pass straight back to these tools.
func (s *Server) toRefs(text string) string {
	return vault.RewriteWikilinks(text, func(target string) string {
		if s.isRef(target) {
			return ""
		}

		ref := s.canonical(target + ".md")
		if ref.Kind == vault.KindPath {
			// A note outside the filing conventions has no better name than
			// the one it already has.
			return ""
		}
		return ref.String()
	})
}

// toPaths rewrites ref links in text to the form stored on disk. It is for
// matching rather than writing, so unlike resolveLinks it has no opinion about
// whether the target exists: an anchor names text in a note, and that text may
// well contain a link that is dead.
func (s *Server) toPaths(text string) string {
	return vault.RewriteWikilinks(text, func(target string) string {
		path, _ := s.linkPath(target)
		return path
	})
}

// refsFor renders stored link targets as refs, for a tool result.
func (s *Server) refsFor(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		ref := s.canonical(target + ".md")
		if ref.Kind == vault.KindPath {
			out = append(out, target)
			continue
		}
		out = append(out, ref.String())
	}
	return out
}
