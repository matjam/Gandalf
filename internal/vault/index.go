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
