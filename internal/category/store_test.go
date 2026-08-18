package category

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFallsBackToDefaults(t *testing.T) {
	root := t.TempDir()

	if Exists(root) {
		t.Fatal("a fresh directory reports declared categories")
	}

	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Categories) != len(Defaults().Categories) {
		t.Errorf("loaded %d categories, want the defaults", len(set.Categories))
	}

	// Falling back must not write: a vault gains the file when something
	// establishes it, not merely by being read.
	if Exists(root) {
		t.Error("Load wrote the category file")
	}
}

func TestSaveAndLoad(t *testing.T) {
	root := t.TempDir()

	set := Defaults()
	if err := set.Add(Category{
		Name: "incident", Plural: "incidents", Rule: RuleDated,
		Folder: "Incidents", Description: "Something broke.",
		Tags: []string{"incident"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := Save(root, set); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !Exists(root) {
		t.Fatal("Save did not create the category file")
	}

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cat, ok := loaded.Lookup("incident")
	if !ok {
		t.Fatal("the added category did not survive a round trip")
	}
	if cat.Folder != "Incidents" || cat.Rule != RuleDated {
		t.Errorf("category = %+v, want it unchanged", cat)
	}
	if strings.Join(cat.Tags, ",") != "incident" {
		t.Errorf("tags = %v", cat.Tags)
	}
}

// TestCorruptFileIsAnError checks the failure is loud. Falling back to the
// defaults would silently refile every note in a vault whose categories had
// been customised.
func TestCorruptFileIsAnError(t *testing.T) {
	root := t.TempDir()

	if err := Save(root, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	abs := filepath.Join(root, filepath.FromSlash(StorePath))
	if err := os.WriteFile(abs, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Load(root); err == nil {
		t.Error("Load accepted a corrupt file")
	}
}

func TestInvalidFileIsAnError(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, filepath.FromSlash(StorePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Parseable JSON describing a category that cannot work.
	const bad = `{"categories":[{"name":"session","plural":"sessions","rule":"dated"}]}`
	if err := os.WriteFile(abs, []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(root)
	if err == nil {
		t.Fatal("Load accepted a category with no folder")
	}
	if !strings.Contains(err.Error(), "needs a folder") {
		t.Errorf("error = %q", err)
	}
}

func TestSaveRefusesAnInvalidSet(t *testing.T) {
	root := t.TempDir()

	if err := Save(root, &Set{}); err == nil {
		t.Error("Save accepted a set with no categories")
	}
	if Exists(root) {
		t.Error("Save wrote a file it had rejected")
	}
}

func TestReplace(t *testing.T) {
	set := Defaults()

	cat, ok := set.Lookup("standard")
	if !ok {
		t.Fatal("no standard category")
	}
	cat.Description = "House rules."

	if err := set.Replace("standard", cat); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, _ := set.Lookup("standard"); got.Description != "House rules." {
		t.Errorf("description = %q", got.Description)
	}

	if err := set.Replace("nonexistent", cat); err == nil {
		t.Error("replacing a category that does not exist succeeded")
	}
}
