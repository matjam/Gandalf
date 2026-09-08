package server

import (
	"strings"
	"testing"

	"github.com/matjam/gandalf/internal/vault"
)

// A vault used to be able to hold only the topics the binary shipped: adding
// one meant a manifest entry and a release. topic_new declares one in the
// vault itself, and from then on it is addressed, listed, corrected, and
// deleted exactly like a shipped topic.

func declareStyle(t *testing.T, h *harness) TopicNewOutput {
	t.Helper()

	var out TopicNewOutput
	h.call("topic_new", TopicNewInput{
		Title:   "Style",
		When:    "Every reply, document, and code comment.",
		Content: "## Sentences\n\nShort declaratives.",
		Tags:    []string{"style"},
	}, &out)
	return out
}

func TestTopicNewIsAddressableLikeAShippedTopic(t *testing.T) {
	h := newHarness(t)
	out := declareStyle(t, h)

	if out.Topic.Ref != "topic:style" {
		t.Fatalf("ref = %q, want topic:style", out.Topic.Ref)
	}
	if out.Note.Type != topicType {
		t.Errorf("type = %q, want %q", out.Note.Type, topicType)
	}
	if !h.vault.Exists("Gandalf/Style.md") {
		t.Error("the note was not filed beside the shipped topics")
	}

	// Read back by ref, and by the bare id the boot table implies.
	for _, alias := range []string{"topic:style", "style"} {
		var note NoteOutput
		h.call("note_read", NoteReadInput{Ref: alias}, &note)
		if note.Ref != "topic:style" {
			t.Errorf("%q read back as %q", alias, note.Ref)
		}
		if !strings.Contains(note.Content, "Short declaratives.") {
			t.Errorf("%q: content lost: %q", alias, note.Content)
		}
	}

	// The path canonicalises to the topic ref, so search and lint agree with
	// boot about the note's name.
	if ref := CanonicalRef(h.vault, "Gandalf/Style.md"); ref.String() != "topic:style" {
		t.Errorf("CanonicalRef = %q, want topic:style", ref)
	}
}

func TestTopicNewIsListedAtBoot(t *testing.T) {
	h := newHarness(t)
	declareStyle(t, h)

	var boot BootOutput
	h.call("boot", BootInput{}, &boot)

	var found bool
	for _, topic := range boot.Topics {
		if topic.Ref == "topic:style" {
			found = true
			if topic.When != "Every reply, document, and code comment." {
				t.Errorf("when = %q", topic.When)
			}
		}
	}
	if !found {
		t.Error("boot did not list the declared topic")
	}

	var list ListOutput
	h.call("list", ListInput{Kind: "topics"}, &list)
	var listed bool
	for _, topic := range list.Topics {
		listed = listed || topic.Ref == "topic:style"
	}
	if !listed {
		t.Error("list topics did not include the declared topic")
	}
}

func TestTopicNewLinksToTheContract(t *testing.T) {
	h := newHarness(t)
	out := declareStyle(t, h)

	var linked bool
	for _, r := range out.Note.Related {
		linked = linked || r == "topic:operating"
	}
	if !linked {
		t.Errorf("related = %v, want a link to topic:operating", out.Note.Related)
	}

	var contract NoteOutput
	h.call("note_read", NoteReadInput{Ref: "topic:operating"}, &contract)
	if !strings.Contains(contract.Content, "[[topic:style]]") {
		t.Error("the contract's backlinks do not list the new topic")
	}
}

func TestTopicNewAcceptsCorrections(t *testing.T) {
	h := newHarness(t)
	declareStyle(t, h)

	var out CorrectOutput
	h.call("correct", CorrectInput{
		Target:   "topic:style",
		Guidance: "Never end with a question.",
	}, &out)
	if out.Ref != "topic:style" {
		t.Errorf("correction landed in %q", out.Ref)
	}

	var note NoteOutput
	h.call("note_read", NoteReadInput{Ref: "topic:style"}, &note)
	if !strings.Contains(note.Content, "Never end with a question.") {
		t.Error("the correction was not recorded in the topic")
	}
}

func TestTopicNewRefusesCollisions(t *testing.T) {
	h := newHarness(t)
	declareStyle(t, h)

	tests := []struct {
		name  string
		input TopicNewInput
		want  string
	}{
		{
			name:  "a shipped topic",
			input: TopicNewInput{Title: "Shipping", When: "Git."},
			want:  "Gandalf ships",
		},
		{
			name:  "a topic already declared",
			input: TopicNewInput{Title: "Style", When: "Prose."},
			want:  "already exists",
		},
		{
			name:  "no when",
			input: TopicNewInput{Title: "Reviews"},
			want:  "needs a when",
		},
		{
			name:  "no usable id",
			input: TopicNewInput{Title: "!!!", When: "Nothing."},
			want:  "empty id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := h.callErr("topic_new", tc.input)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("error %q does not say %q", msg, tc.want)
			}
		})
	}
}

func TestTopicNewSurvivesReopen(t *testing.T) {
	h := newHarness(t)
	declareStyle(t, h)

	reopened, err := vault.Open(h.vault.Root())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := New(reopened, "test")
	ref, path, err := s.resolve("topic:style")
	if err != nil {
		t.Fatalf("resolve after reopen: %v", err)
	}
	if ref.String() != "topic:style" || path != "Gandalf/Style.md" {
		t.Errorf("resolved to %q at %q", ref, path)
	}
}

func TestDeletingATopicUnregistersIt(t *testing.T) {
	h := newHarness(t)
	declareStyle(t, h)

	var out NoteDeleteOutput
	h.call("note_delete", NoteDeleteInput{Ref: "topic:style"}, &out)
	if !out.Deleted {
		t.Fatal("not deleted")
	}

	if msg := h.callErr("note_read", NoteReadInput{Ref: "topic:style"}); !strings.Contains(msg, "no such topic") {
		t.Errorf("after deletion, note_read said %q", msg)
	}

	var boot BootOutput
	h.call("boot", BootInput{}, &boot)
	for _, topic := range boot.Topics {
		if topic.Ref == "topic:style" {
			t.Error("boot still advertises the deleted topic")
		}
	}
}

func TestTopicFile(t *testing.T) {
	tests := map[string]string{
		"Style":            "Style.md",
		"External Content": "External Content.md",
		"Code  Review":     "Code Review.md",
		"a/b:c":            "abc.md",
	}
	for title, want := range tests {
		if got := topicFile(title); got != want {
			t.Errorf("topicFile(%q) = %q, want %q", title, got, want)
		}
	}
}
