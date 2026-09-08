package topic

import (
	"os"
	"path/filepath"
	"testing"
)

func valid() Topic {
	return Topic{ID: "style", Path: "Gandalf/Style.md", Title: "Style", When: "Every reply."}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	set, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Topics) != 0 {
		t.Errorf("got %d topics from a vault that declared none", len(set.Topics))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()

	set := &Set{}
	if err := set.Add(valid()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Save(root, set); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := loaded.Lookup("style")
	if !ok || got != valid() {
		t.Errorf("Lookup after round trip = %+v, %v", got, ok)
	}
	if _, ok := loaded.ByPath("Gandalf/Style.md"); !ok {
		t.Error("ByPath did not find the saved topic")
	}
}

func TestLoadRefusesCorruptFile(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, filepath.FromSlash(StorePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(root); err == nil {
		t.Error("a corrupt registry loaded as if it were empty")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Topic)
	}{
		{"empty id", func(tp *Topic) { tp.ID = "" }},
		{"uppercase id", func(tp *Topic) { tp.ID = "Style" }},
		{"empty title", func(tp *Topic) { tp.Title = "" }},
		{"empty when", func(tp *Topic) { tp.When = "" }},
		{"not markdown", func(tp *Topic) { tp.Path = "Gandalf/Style.txt" }},
		{"outside the folder", func(tp *Topic) { tp.Path = "Standards/Style.md" }},
		{"nested", func(tp *Topic) { tp.Path = "Gandalf/sub/Style.md" }},
		{"escaping", func(tp *Topic) { tp.Path = "Gandalf/../Style.md" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tp := valid()
			tc.mutate(&tp)
			if err := tp.Validate(); err == nil {
				t.Errorf("%+v validated", tp)
			}
		})
	}

	if err := valid().Validate(); err != nil {
		t.Errorf("a valid topic was refused: %v", err)
	}
}

func TestAddRefusesCollisions(t *testing.T) {
	set := &Set{}
	if err := set.Add(valid()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := set.Add(valid()); err == nil {
		t.Error("the same id was declared twice")
	}

	other := valid()
	other.ID = "house-style"
	if err := set.Add(other); err == nil {
		t.Error("two topics were filed at one path")
	}
}

func TestRemove(t *testing.T) {
	set := &Set{}
	if err := set.Add(valid()); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if !set.Remove("style") {
		t.Error("Remove did not find the topic")
	}
	if set.Remove("style") {
		t.Error("Remove found a topic that was already gone")
	}
	if len(set.Topics) != 0 {
		t.Errorf("%d topics remain", len(set.Topics))
	}
}
