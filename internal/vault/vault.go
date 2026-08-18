package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/matjam/gandalf/internal/category"
)

// ErrOutsideVault is returned for a path that would escape the vault root.
var ErrOutsideVault = errors.New("path is outside the vault")

// Vault is a markdown vault rooted at a directory on disk. All note paths
// crossing this API are vault-relative and slash-separated, which keeps the
// tool surface identical on every platform.
type Vault struct {
	root       string
	categories *category.Set
	depth      category.Depth
}

// Open returns the vault rooted at dir, creating the directory if it does not
// yet exist. An existing path that is not a directory is an error.
func Open(dir string) (*Vault, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve vault root %q: %w", dir, err)
	}

	switch info, err := os.Stat(abs); {
	case err == nil && !info.IsDir():
		return nil, fmt.Errorf("vault root %q is not a directory", abs)
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, fmt.Errorf("create vault root %q: %w", abs, err)
		}
	default:
		return nil, fmt.Errorf("open vault root %q: %w", abs, err)
	}

	categories, err := category.Load(abs)
	if err != nil {
		return nil, err
	}

	return &Vault{root: abs, categories: categories, depth: category.DepthMonth}, nil
}

// Categories returns the categories this vault declares.
func (v *Vault) Categories() *category.Set { return v.categories }

// SetCategories replaces the vault's categories and writes them back, so the
// declaration and what the vault does with it cannot drift apart.
func (v *Vault) SetCategories(set *category.Set) error {
	if err := category.Save(v.root, set); err != nil {
		return err
	}
	v.categories = set
	return nil
}

// Depth returns how deeply dated notes are nested.
func (v *Vault) Depth() category.Depth { return v.depth }

// Root returns the vault's absolute root directory.
func (v *Vault) Root() string { return v.root }

// Read parses the note at a vault-relative path.
func (v *Vault) Read(rel string) (*Note, error) {
	abs, err := v.abs(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read note %q: %w", rel, err)
	}
	return ParseNote(path.Clean(rel), data)
}

// Write serialises a note to disk, creating parent directories as needed.
//
// Notes carrying unresolved parse issues are refused: their affected fields
// are unset, so writing would silently discard whatever the file actually
// said. Fix the file or the request first.
func (v *Vault) Write(n *Note) error {
	if len(n.Issues) > 0 {
		return fmt.Errorf("refusing to write %q: %d unresolved frontmatter issue(s)", n.Path, len(n.Issues))
	}
	abs, err := v.abs(n.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", n.Path, err)
	}
	return writeFileAtomic(abs, n.Render())
}

// Exists reports whether a note exists at a vault-relative path.
func (v *Vault) Exists(rel string) bool {
	abs, err := v.abs(rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

// List returns every markdown note in the vault, sorted, as vault-relative
// paths. Dot-directories are skipped: .git and .obsidian hold no notes.
func (v *Vault) List() ([]string, error) {
	var notes []string

	err := filepath.WalkDir(v.root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if abs != v.root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		rel, err := filepath.Rel(v.root, abs)
		if err != nil {
			return err
		}
		notes = append(notes, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}

	return notes, nil
}

// abs converts a vault-relative path to an absolute one, refusing anything
// that escapes the root.
func (v *Vault) abs(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrOutsideVault)
	}
	if path.IsAbs(rel) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q is absolute", ErrOutsideVault, rel)
	}
	clean := path.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", ErrOutsideVault, rel)
	}
	return filepath.Join(v.root, filepath.FromSlash(clean)), nil
}

// writeFileAtomic writes data to a temporary file in the target directory and
// renames it into place, so a reader — Obsidian, git, another agent — never
// observes a half-written note.
func writeFileAtomic(abs string, data []byte) error {
	dir, base := filepath.Dir(abs), filepath.Base(abs)

	tmp, err := os.CreateTemp(dir, "."+base+".*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", abs, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %q: %w", abs, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %q: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("set permissions on %q: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, abs); err != nil {
		return fmt.Errorf("rename into %q: %w", abs, err)
	}
	return nil
}
