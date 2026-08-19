package vault

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// LinkIndex records which notes point at which, in both directions.
type LinkIndex struct {
	// outgoing maps a note path to the note paths it links to.
	outgoing map[string][]string

	// inbound maps a note path to the note paths linking to it.
	inbound map[string][]string
}

// BuildLinkIndex reads every note and works out what links to what.
//
// It reads the whole vault rather than tracking changes incrementally: a
// vault is a few hundred small files, and an index that can be rebuilt from
// the notes cannot drift away from them. Incremental updates would be faster
// and would eventually be wrong.
func (v *Vault) BuildLinkIndex() (*LinkIndex, error) {
	paths, err := v.List()
	if err != nil {
		return nil, err
	}

	idx := &LinkIndex{
		outgoing: make(map[string][]string, len(paths)),
		inbound:  make(map[string][]string, len(paths)),
	}
	resolver := newIndex(paths)

	for _, notePath := range paths {
		note, err := v.Read(notePath)
		if err != nil {
			// A note too broken to parse is lint's business.
			continue
		}

		for _, target := range note.OutgoingLinks() {
			resolved := resolver.resolve(target)
			if resolved == "" || resolved == notePath {
				// Unresolvable links are reported by lint; a self-link is not
				// a backlink worth recording.
				continue
			}
			idx.outgoing[notePath] = append(idx.outgoing[notePath], resolved)
			idx.inbound[resolved] = append(idx.inbound[resolved], notePath)
		}
	}

	return idx, nil
}

// Inbound returns the notes linking to the given path.
func (idx *LinkIndex) Inbound(notePath string) []string {
	links := append([]string(nil), idx.inbound[notePath]...)
	sort.Strings(links)
	return dedupe(links)
}

// Outgoing returns the notes the given path links to.
func (idx *LinkIndex) Outgoing(notePath string) []string {
	links := append([]string(nil), idx.outgoing[notePath]...)
	sort.Strings(links)
	return dedupe(links)
}

// ApplyBacklinks updates the notes whose inbound links changed when source's
// outgoing links went from old to new, and reports which it rewrote.
//
// This is the write path. Rebuilding the whole vault on every write is correct
// and costs a full read of every note; here the set of affected notes is known
// exactly — the targets the source stopped linking to, and the ones it started
// linking to — so the work is proportional to a note's links rather than to
// the vault's size. Resolving names still needs the list of note paths, but
// that is a directory walk rather than a read of every file.
//
// Drift is possible if notes are changed outside these tools, which is what
// RebuildBacklinks and the lint check exist for.
func (v *Vault) ApplyBacklinks(sourcePath string, old, new []string) ([]string, error) {
	paths, err := v.List()
	if err != nil {
		return nil, err
	}
	resolver := newIndex(paths)

	source := strings.TrimSuffix(path.Clean(sourcePath), path.Ext(sourcePath))

	before := resolve(resolver, sourcePath, old)
	after := resolve(resolver, sourcePath, new)

	var changed []string
	for target := range before {
		if after[target] {
			continue
		}
		ok, err := v.editBacklinks(target, source, false)
		if err != nil {
			return nil, err
		}
		if ok {
			changed = append(changed, target)
		}
	}
	for target := range after {
		if before[target] {
			continue
		}
		ok, err := v.editBacklinks(target, source, true)
		if err != nil {
			return nil, err
		}
		if ok {
			changed = append(changed, target)
		}
	}

	sort.Strings(changed)
	return changed, nil
}

// editBacklinks adds or removes one entry from a note's maintained block,
// reporting whether the note needed changing.
func (v *Vault) editBacklinks(targetPath, source string, add bool) (bool, error) {
	note, err := v.Read(targetPath)
	if err != nil {
		// A target that cannot be parsed is lint's problem; failing the write
		// that happened to link to it would be the wrong place to complain.
		return false, nil
	}

	links := note.Backlinks()
	updated := make([]string, 0, len(links)+1)
	found := false
	for _, l := range links {
		if l == source {
			found = true
			continue
		}
		updated = append(updated, l)
	}

	switch {
	case add && found:
		return false, nil
	case add:
		updated = append(updated, source)
	case !found:
		return false, nil
	}

	note.SetBacklinks(updated)
	if err := v.Write(note); err != nil {
		return false, fmt.Errorf("update backlinks in %q: %w", targetPath, err)
	}
	return true, nil
}

// resolve turns a note's link targets into the paths they address, dropping
// anything unresolvable or pointing at the note itself.
func resolve(resolver *noteIndex, sourcePath string, targets []string) map[string]bool {
	out := map[string]bool{}
	for _, target := range targets {
		resolved := resolver.resolve(target)
		if resolved == "" || resolved == path.Clean(sourcePath) {
			continue
		}
		out[resolved] = true
	}
	return out
}

// RebuildBacklinks brings every note's maintained block into line with what
// actually links to it, and reports which notes changed.
//
// Only notes whose block is wrong are written, so a rebuild over an unchanged
// vault touches nothing — which matters when the vault is in git.
func (v *Vault) RebuildBacklinks() ([]string, error) {
	idx, err := v.BuildLinkIndex()
	if err != nil {
		return nil, err
	}

	paths, err := v.List()
	if err != nil {
		return nil, err
	}

	var changed []string
	for _, notePath := range paths {
		note, err := v.Read(notePath)
		if err != nil {
			continue
		}

		want := make([]string, 0, len(idx.Inbound(notePath)))
		for _, source := range idx.Inbound(notePath) {
			want = append(want, strings.TrimSuffix(source, path.Ext(source)))
		}

		if equal(note.Backlinks(), want) {
			continue
		}

		note.SetBacklinks(want)
		if err := v.Write(note); err != nil {
			return nil, fmt.Errorf("update backlinks in %q: %w", notePath, err)
		}
		changed = append(changed, notePath)
	}

	return changed, nil
}

// Unresolved returns the link targets that name no note in the vault.
//
// Resolution matches how a link is read everywhere else — a full path or a
// bare filename, as Obsidian accepts — so a caller asking whether a note's
// links are dead gets the same answer lint would give.
func (v *Vault) Unresolved(targets []string) ([]string, error) {
	paths, err := v.List()
	if err != nil {
		return nil, err
	}
	resolver := newIndex(paths)

	var dead []string
	for _, target := range targets {
		if resolver.resolve(target) == "" {
			dead = append(dead, target)
		}
	}
	return dead, nil
}

// Referrers returns the notes linking to the given path, as vault paths.
func (v *Vault) Referrers(notePath string) ([]string, error) {
	idx, err := v.BuildLinkIndex()
	if err != nil {
		return nil, err
	}
	return idx.Inbound(path.Clean(notePath)), nil
}

// dedupe removes consecutive duplicates from a sorted slice.
func dedupe(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// equal reports whether two link lists hold the same targets.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)

	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
