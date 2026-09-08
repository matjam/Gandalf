// Package topic holds the operating topics a vault declares for itself.
//
// Gandalf ships a fixed set of topics — shipping, diagnostics, and so on —
// whose homes come from a manifest compiled into the binary. A vault that
// wants another one, such as a house style guide, used to need a new release.
// This package is the registry that removes that step: it records which extra
// documents in the Gandalf folder are topics, so they are listed at boot and
// addressed as topic:<id> like the shipped ones.
//
// The registry is structural machinery in the same sense categories are. It
// lives beside them in the dot-directory, travels with the vault, and is the
// single place a vault-declared topic is named.
package topic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// StorePath is where a vault's own topics are declared, relative to its root.
const StorePath = ".gandalf/topics.json"

// Folder is where topics are filed. It is the folder the shipped topics use,
// so a vault-declared one sits beside them rather than in a place of its own.
const Folder = "Gandalf"

// Topic is one operating topic the vault declares.
type Topic struct {
	// ID is the name the topic is addressed by, as topic:<id>.
	ID string `json:"id"`

	// Path is the note's vault-relative path, under Folder.
	Path string `json:"path"`

	Title string `json:"title"`

	// When describes the work that should send the model here. It is what the
	// boot payload's topic table shows.
	When string `json:"when"`
}

// Set is every topic a vault declares, in declaration order.
type Set struct {
	Topics []Topic `json:"topics"`
}

// idPattern is the shape an id must take: lowercase words joined by single
// hyphens, which is also what a title slugifies to.
var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate reports what is wrong with a single topic.
func (t Topic) Validate() error {
	switch {
	case t.ID == "":
		return fmt.Errorf("a topic needs an id")
	case !idPattern.MatchString(t.ID):
		return fmt.Errorf("topic id %q must be lowercase words joined by hyphens", t.ID)
	case strings.TrimSpace(t.Title) == "":
		return fmt.Errorf("topic %q needs a title", t.ID)
	case strings.TrimSpace(t.When) == "":
		return fmt.Errorf("topic %q needs a when: the work that should send the model to it", t.ID)
	}

	clean := path.Clean(t.Path)
	switch {
	case clean != t.Path:
		return fmt.Errorf("topic %q path %q is not in canonical form", t.ID, t.Path)
	case !strings.HasSuffix(clean, ".md"):
		return fmt.Errorf("topic %q path %q is not a markdown file", t.ID, t.Path)
	case path.Dir(clean) != Folder:
		return fmt.Errorf("topic %q path %q is not directly under %s/", t.ID, t.Path, Folder)
	}

	return nil
}

// Validate reports what is wrong with the set as a whole.
func (s *Set) Validate() error {
	ids := map[string]bool{}
	paths := map[string]bool{}

	for _, t := range s.Topics {
		if err := t.Validate(); err != nil {
			return err
		}
		if ids[t.ID] {
			return fmt.Errorf("topic %q is declared twice", t.ID)
		}
		if paths[t.Path] {
			return fmt.Errorf("topic path %q is declared twice", t.Path)
		}
		ids[t.ID] = true
		paths[t.Path] = true
	}

	return nil
}

// Lookup returns the topic with the given id.
func (s *Set) Lookup(id string) (Topic, bool) {
	for _, t := range s.Topics {
		if t.ID == id {
			return t, true
		}
	}
	return Topic{}, false
}

// ByPath returns the topic filed at a vault path.
func (s *Set) ByPath(notePath string) (Topic, bool) {
	for _, t := range s.Topics {
		if t.Path == notePath {
			return t, true
		}
	}
	return Topic{}, false
}

// Add declares a topic, refusing one whose id or path is already taken.
func (s *Set) Add(t Topic) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if _, taken := s.Lookup(t.ID); taken {
		return fmt.Errorf("topic %q already exists", t.ID)
	}
	if existing, taken := s.ByPath(t.Path); taken {
		return fmt.Errorf("topic %q would be filed at %q, which topic %q already uses", t.ID, t.Path, existing.ID)
	}

	s.Topics = append(s.Topics, t)
	return nil
}

// Remove drops a topic by id, reporting whether it was there.
func (s *Set) Remove(id string) bool {
	for i, t := range s.Topics {
		if t.ID == id {
			s.Topics = slices.Delete(s.Topics, i, i+1)
			return true
		}
	}
	return false
}

// Load reads a vault's topics. A vault that has declared none has an empty
// set rather than a missing file.
func Load(root string) (*Set, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StorePath)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &Set{}, nil
	case err != nil:
		return nil, fmt.Errorf("read topics: %w", err)
	}

	set := &Set{}
	if err := json.Unmarshal(data, set); err != nil {
		// An empty set here would make every vault-declared topic
		// unaddressable while the file that names them sits right there.
		return nil, fmt.Errorf("parse topics at %s: %w", StorePath, err)
	}
	if err := set.Validate(); err != nil {
		return nil, fmt.Errorf("topics at %s: %w", StorePath, err)
	}

	return set, nil
}

// Save writes a vault's topics.
func Save(root string, set *Set) error {
	if err := set.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("encode topics: %w", err)
	}
	data = append(data, '\n')

	abs := filepath.Join(root, filepath.FromSlash(StorePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create topics directory: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("write topics: %w", err)
	}

	return nil
}
