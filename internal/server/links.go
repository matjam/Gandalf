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
//
// With several vaults the tool form also says which vault a link is in, as
// @team:ref, and the same boundary is where that is added and removed. The
// disk form never carries it: each vault is opened on its own, so a link out
// of one into another would resolve to nothing there. That is why a link
// between vaults is refused rather than stored.

// links carries the result of translating a note's links on the way in.
type links struct {
	// Related are the frontmatter link targets, as vault paths.
	Related []string

	// Body is the note text with refs rewritten to paths.
	Body string

	// Missing are refs whose targets are not in the vault.
	Missing []string

	// Cross are links that point into a different vault from the note they
	// are in.
	Cross []string
}

// err returns the failure to report when links cannot be written.
func (l links) err() error {
	var problems []string

	if len(l.Cross) > 0 {
		problems = append(problems, fmt.Sprintf(
			"these links point into a different vault: %s. Links between vaults are not allowed, "+
				"because Obsidian cannot resolve a link from one vault into another and the note would "+
				"carry a dead link. Name the other note in prose instead, or copy what is needed into this vault",
			strings.Join(l.Cross, ", ")))
	}
	if len(l.Missing) > 0 {
		problems = append(problems, fmt.Sprintf(
			"these links point at notes that do not exist: %s. Create them first, or drop the links — "+
				"Gandalf will not write a link it knows is dead, and will not create an empty note to satisfy one",
			strings.Join(l.Missing, ", ")))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; and "))
}

// linkStatus is what became of one link target on its way in.
type linkStatus int

const (
	// linkNotRef means the target is not a ref, and is kept as written.
	linkNotRef linkStatus = iota

	// linkOK means the target is a ref to a note that exists in this vault.
	linkOK

	// linkMissing means the target is a ref to a note this vault lacks.
	linkMissing

	// linkCross means the target is in another vault.
	linkCross
)

// resolveLinks translates a note's incoming links and reports every target
// that is missing or in another vault.
//
// Every problem is collected rather than failing on the first, for the same
// reason the schema reports all its violations at once: one round trip per
// broken link is a waste of everybody's time.
func (c *scoped) resolveLinks(related []string, body string) links {
	out := links{Body: body}

	for _, raw := range related {
		path, status := c.classify(raw)
		switch status {
		case linkNotRef:
			// Not a ref at all; keep whatever the caller wrote and let lint
			// have an opinion about it.
			out.Related = append(out.Related, vault.LinkTarget(raw))
		case linkMissing:
			out.Missing = append(out.Missing, raw)
		case linkCross:
			out.Cross = append(out.Cross, raw)
		default:
			out.Related = append(out.Related, path)
		}
	}

	out.Body = vault.RewriteWikilinks(body, func(target string) string {
		path, status := c.classify(target)
		switch status {
		case linkMissing:
			out.Missing = append(out.Missing, target)
			return ""
		case linkCross:
			out.Cross = append(out.Cross, target)
			return ""
		case linkOK:
			return path
		default:
			// Already a path, or not a ref: leave it exactly as written.
			return ""
		}
	})

	return out
}

// classify decides what a link target is, from the point of view of a note in
// this vault.
//
// The rules follow from there being one place a link can resolve: the vault
// the note is in. A target prefixed with this vault's own @team: is the same
// as one without, and is stripped to an in-vault link. A target prefixed with
// any other vault is refused without looking it up, so a link cannot be used to
// ask whether a team exists or a note is there. An unprefixed target is looked
// for here; if it is absent here but the caller's personal vault has it, that
// is what the caller meant, and it is refused as a cross-vault link rather than
// reported as simply missing, which would send them off to create a duplicate.
func (c *scoped) classify(raw string) (string, linkStatus) {
	target := strings.TrimSpace(raw)

	slug, rest, err := splitScope(target)
	if err != nil {
		// Something like [[@mention]]: not a ref of ours, so not ours to judge.
		return "", linkNotRef
	}
	if slug != "" {
		if slug != c.slug {
			return "", linkCross
		}
		target = rest
	}

	path, ok := c.linkPath(target)
	switch {
	case path == "":
		return "", linkNotRef
	case ok:
		return path, linkOK
	}

	if c.slug != "" && slug == "" {
		if _, found := c.srv.personal().linkPath(target); found {
			return "", linkCross
		}
	}
	return path, linkMissing
}

// isRef reports whether a wikilink target is a ref rather than a path or
// ordinary prose. Topics count: they are addressable even though no category
// declares them.
func (c *scoped) isRef(target string) bool {
	if strings.HasPrefix(strings.TrimSpace(target), KindTopic+":") {
		return true
	}
	_, err := c.vault.ParseRef(target)
	return err == nil
}

// linkPath returns the vault path an unprefixed ref addresses, without its
// extension, and whether that note exists. An empty path means the target was
// not a ref.
func (c *scoped) linkPath(raw string) (string, bool) {
	if !c.isRef(raw) {
		return "", false
	}

	// Resolving also freezes aliases: a note saved with [[session:latest]]
	// would otherwise mean something different every time it was read.
	_, path, err := c.resolve(raw)
	if err != nil {
		return "", false
	}

	return strings.TrimSuffix(path, ".md"), c.vault.Exists(path)
}

// toRefs rewrites path links in body text to refs, so a model reading a note
// gets links it can pass straight back to these tools. In a team vault the
// refs carry the team, because that is how the caller will have to name them.
func (c *scoped) toRefs(text string) string {
	return vault.RewriteWikilinks(text, func(target string) string {
		if strings.HasPrefix(target, "@") {
			// Already scoped, or an @ that is not a ref at all: leave it.
			return ""
		}
		if c.isRef(target) {
			return ""
		}

		ref := c.canonical(target + ".md")
		if ref.Kind == vault.KindPath {
			// A note outside the filing conventions has no better name than
			// the one it already has.
			return ""
		}
		return c.qualify(ref)
	})
}

// toPaths rewrites ref links in text to the form stored on disk. It is for
// matching rather than writing, so unlike resolveLinks it has no opinion about
// whether the target exists: an anchor names text in a note, and that text may
// well contain a link that is dead.
func (c *scoped) toPaths(text string) string {
	return vault.RewriteWikilinks(text, func(target string) string {
		slug, rest, err := splitScope(target)
		if err != nil || (slug != "" && slug != c.slug) {
			return ""
		}
		path, _ := c.linkPath(rest)
		return path
	})
}

// refsFor renders stored link targets as refs, for a tool result.
func (c *scoped) refsFor(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		ref := c.canonical(target + ".md")
		if ref.Kind == vault.KindPath {
			out = append(out, target)
			continue
		}
		out = append(out, c.qualify(ref))
	}
	return out
}
