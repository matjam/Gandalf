package vault

import (
	"strings"
	"testing"
)

// linked writes a note with the given body, returning its path.
func linked(t *testing.T, v *Vault, title, body string) string {
	t.Helper()

	n, err := v.NewNote(NewNoteRequest{
		Type: typeStandard, Title: title, Tags: []string{"standards"}, Body: body,
	})
	if err != nil {
		t.Fatalf("NewNote(%q): %v", title, err)
	}
	if err := v.Write(n); err != nil {
		t.Fatalf("Write(%q): %v", title, err)
	}
	return n.Path
}

func TestSplitBacklinks(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		content, block string
	}{
		{
			name:    "no block",
			body:    "# Note\n\nProse.\n",
			content: "# Note\n\nProse.\n",
		},
		{
			name:    "with a block",
			body:    "# Note\n\nProse.\n\n## Backlinks\n\n- [[Other]]\n",
			content: "# Note\n\nProse.\n",
			block:   "## Backlinks\n\n- [[Other]]\n",
		},
		{
			name:    "a heading that merely mentions backlinks is not the block",
			body:    "# Note\n\n## Backlinks and other ideas\n\nProse.\n",
			content: "# Note\n\n## Backlinks and other ideas\n\nProse.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, block := SplitBacklinks(tc.body)
			if content != tc.content {
				t.Errorf("content = %q, want %q", content, tc.content)
			}
			if block != tc.block {
				t.Errorf("block = %q, want %q", block, tc.block)
			}
		})
	}
}

func TestSetBacklinks(t *testing.T) {
	n := &Note{Body: "# Note\n\nProse.\n"}

	n.SetBacklinks([]string{"Sessions/2026/08/b", "Sessions/2026/08/a", "Sessions/2026/08/a"})

	if got := n.Content(); got != "# Note\n\nProse." {
		t.Errorf("content = %q, want it untouched", got)
	}
	if got := strings.Join(n.Backlinks(), ","); got != "Sessions/2026/08/a,Sessions/2026/08/b" {
		t.Errorf("backlinks = %q, want them sorted and deduplicated", got)
	}

	// Setting none removes the block rather than leaving an empty heading.
	n.SetBacklinks(nil)
	if strings.Contains(n.Body, BacklinksHeading) {
		t.Errorf("an empty block was left behind:\n%s", n.Body)
	}
	if got := n.Content(); got != "# Note\n\nProse." {
		t.Errorf("content = %q after removal", got)
	}
}

// TestOutgoingLinksExcludeTheBlock is the property that stops backlinks from
// recursing: what points at me is not what I point at.
func TestOutgoingLinksExcludeTheBlock(t *testing.T) {
	n := &Note{Body: "# Note\n\nSee [[Standards/security]].\n\n## Backlinks\n\n- [[Sessions/2026/08/a]]\n"}
	n.FM.Related = []string{"Standards/privacy"}

	got := strings.Join(n.OutgoingLinks(), ",")
	if want := "Standards/privacy,Standards/security"; got != want {
		t.Errorf("outgoing = %q, want %q", got, want)
	}
}

func TestAppendKeepsTheBlockLast(t *testing.T) {
	n := &Note{Body: "# Note\n\nProse.\n"}
	n.SetBacklinks([]string{"Sessions/2026/08/a"})

	n.Append("Later", "More prose.")

	added := strings.Index(n.Body, "More prose.")
	block := strings.Index(n.Body, BacklinksHeading)
	switch {
	case added < 0:
		t.Fatalf("the appended content is missing:\n%s", n.Body)
	case added > block:
		t.Errorf("content landed below the block:\n%s", n.Body)
	}

	if got := strings.Join(n.Backlinks(), ","); got != "Sessions/2026/08/a" {
		t.Errorf("backlinks = %q, want them preserved", got)
	}
}

func TestRebuildBacklinks(t *testing.T) {
	v := newVault(t)

	target := linked(t, v, "Target", "# Target\n\nProse.\n")
	source := linked(t, v, "Source", "# Source\n\nSee [[Standards/target]].\n")

	changed, err := v.RebuildBacklinks()
	if err != nil {
		t.Fatalf("RebuildBacklinks: %v", err)
	}
	if strings.Join(changed, ",") != target {
		t.Errorf("changed = %v, want only %q", changed, target)
	}

	note, err := v.Read(target)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := strings.Join(note.Backlinks(), ","); got != "Standards/source" {
		t.Errorf("backlinks = %q", got)
	}

	// A second rebuild changes nothing, which matters in a git-tracked vault.
	changed, err = v.RebuildBacklinks()
	if err != nil {
		t.Fatalf("second RebuildBacklinks: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("a settled vault reported %v as changed", changed)
	}

	_ = source
}

func TestRebuildIgnoresSelfLinks(t *testing.T) {
	v := newVault(t)

	self := linked(t, v, "Self", "# Self\n\nSee [[Standards/self]].\n")

	if _, err := v.RebuildBacklinks(); err != nil {
		t.Fatalf("RebuildBacklinks: %v", err)
	}

	note, err := v.Read(self)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(note.Backlinks()) != 0 {
		t.Errorf("a self-link produced a backlink: %v", note.Backlinks())
	}
}

func TestReferrers(t *testing.T) {
	v := newVault(t)

	target := linked(t, v, "Target", "# Target\n\nProse.\n")
	linked(t, v, "First", "# First\n\nSee [[Standards/target]].\n")
	linked(t, v, "Second", "# Second\n\nAlso [[Standards/target]].\n")

	referrers, err := v.Referrers(target)
	if err != nil {
		t.Fatalf("Referrers: %v", err)
	}
	if got := strings.Join(referrers, ","); got != "Standards/first.md,Standards/second.md" {
		t.Errorf("referrers = %q", got)
	}

	none, err := v.Referrers("Standards/first.md")
	if err != nil {
		t.Fatalf("Referrers: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unreferenced note reported %v", none)
	}
}

func TestLinkIndexDirections(t *testing.T) {
	v := newVault(t)

	target := linked(t, v, "Target", "# Target\n\nProse.\n")
	source := linked(t, v, "Source", "# Source\n\nSee [[Standards/target]].\n")

	idx, err := v.BuildLinkIndex()
	if err != nil {
		t.Fatalf("BuildLinkIndex: %v", err)
	}

	if got := strings.Join(idx.Outgoing(source), ","); got != target {
		t.Errorf("outgoing from source = %q, want %q", got, target)
	}
	if got := strings.Join(idx.Inbound(target), ","); got != source {
		t.Errorf("inbound to target = %q, want %q", got, source)
	}
	if len(idx.Inbound(source)) != 0 {
		t.Errorf("source has inbound links: %v", idx.Inbound(source))
	}
}

func TestDelete(t *testing.T) {
	v := newVault(t)

	path := linked(t, v, "Doomed", "# Doomed\n")
	if !v.Exists(path) {
		t.Fatal("the note was not written")
	}

	if err := v.Delete(path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if v.Exists(path) {
		t.Error("the note is still there")
	}
	if err := v.Delete(path); err == nil {
		t.Error("deleting a missing note succeeded")
	}
	if err := v.Delete("../outside.md"); err == nil {
		t.Error("deleting outside the vault succeeded")
	}
}
