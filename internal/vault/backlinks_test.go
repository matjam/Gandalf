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

// TestApplyBacklinksTouchesOnlyWhatChanged is the write path. A full rebuild
// is correct and reads every note; this reads only the targets whose inbound
// links actually moved.
func TestApplyBacklinksTouchesOnlyWhatChanged(t *testing.T) {
	v := newVault(t)

	first := linked(t, v, "First", "# First\n")
	second := linked(t, v, "Second", "# Second\n")
	linked(t, v, "Bystander", "# Bystander\n")
	source := linked(t, v, "Source", "# Source\n\nSee [[Standards/first]].\n")

	changed, err := v.ApplyBacklinks(source, nil, []string{"Standards/first"})
	if err != nil {
		t.Fatalf("ApplyBacklinks: %v", err)
	}
	if strings.Join(changed, ",") != first {
		t.Errorf("changed = %v, want only %q", changed, first)
	}

	note, err := v.Read(first)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := strings.Join(note.Backlinks(), ","); got != "Standards/source" {
		t.Errorf("backlinks = %q", got)
	}

	// Moving the link moves the backlink: one note loses it, one gains it, and
	// the bystander is not rewritten.
	changed, err = v.ApplyBacklinks(source, []string{"Standards/first"}, []string{"Standards/second"})
	if err != nil {
		t.Fatalf("ApplyBacklinks: %v", err)
	}
	if want := first + "," + second; strings.Join(changed, ",") != want {
		t.Errorf("changed = %v, want %q", changed, want)
	}

	if note, _ = v.Read(first); len(note.Backlinks()) != 0 {
		t.Errorf("the old target kept its backlink: %v", note.Backlinks())
	}
	if note, _ = v.Read(second); strings.Join(note.Backlinks(), ",") != "Standards/source" {
		t.Errorf("the new target did not gain one: %v", note.Backlinks())
	}
}

func TestApplyBacklinksIsIdempotent(t *testing.T) {
	v := newVault(t)

	target := linked(t, v, "Target", "# Target\n")
	source := linked(t, v, "Source", "# Source\n\nSee [[Standards/target]].\n")

	if _, err := v.ApplyBacklinks(source, nil, []string{"Standards/target"}); err != nil {
		t.Fatalf("ApplyBacklinks: %v", err)
	}

	changed, err := v.ApplyBacklinks(source, []string{"Standards/target"}, []string{"Standards/target"})
	if err != nil {
		t.Fatalf("second ApplyBacklinks: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("an unchanged link set rewrote %v", changed)
	}

	note, _ := v.Read(target)
	if got := strings.Join(note.Backlinks(), ","); got != "Standards/source" {
		t.Errorf("backlinks = %q, want no duplicate", got)
	}
}

// TestApplyBacklinksMatchesAFullRebuild pins the incremental path against the
// one that reads everything, since the whole point is that they agree.
func TestApplyBacklinksMatchesAFullRebuild(t *testing.T) {
	v := newVault(t)

	linked(t, v, "Alpha", "# Alpha\n")
	linked(t, v, "Beta", "# Beta\n")
	source := linked(t, v, "Source", "# Source\n\nSee [[Standards/alpha]] and [[Standards/beta]].\n")

	if _, err := v.ApplyBacklinks(source, nil, []string{"Standards/alpha", "Standards/beta"}); err != nil {
		t.Fatalf("ApplyBacklinks: %v", err)
	}

	rebuilt, err := v.RebuildBacklinks()
	if err != nil {
		t.Fatalf("RebuildBacklinks: %v", err)
	}
	if len(rebuilt) != 0 {
		t.Errorf("a full rebuild disagreed with the incremental update, rewriting %v", rebuilt)
	}
}

// TestLintDetectsBacklinkDrift covers what makes incremental updates safe:
// nothing repairs a note edited outside the tools, so lint has to notice.
func TestLintDetectsBacklinkDrift(t *testing.T) {
	v := newVault(t)

	target := linked(t, v, "Target", "# Target\n")
	linked(t, v, "Source", "# Source\n\nSee [[Standards/target]].\n")

	findings, err := v.Lint(target)
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}

	var found bool
	for _, f := range findings {
		if strings.Contains(f.Message, "backlinks are out of date") {
			found = true
		}
	}
	if !found {
		t.Errorf("lint did not notice the missing backlink: %v", findings)
	}

	if _, err := v.RebuildBacklinks(); err != nil {
		t.Fatalf("RebuildBacklinks: %v", err)
	}
	if findings, err = v.Lint(target); err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("still unclean after a rebuild: %v", findings)
	}
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
