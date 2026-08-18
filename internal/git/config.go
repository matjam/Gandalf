// Package git maintains a vault as a git repository so a model never has to.
//
// Notes stay plain files. Gandalf owns the commits: init creates the repo,
// every mutation through the tools ends in a commit, and a background loop
// syncs with a remote when one is configured. Conflict resolution is
// remote-wins on pull — multi-machine eventual consistency over perfect merge.
package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConfigPath is where a vault's git settings live, relative to its root.
const ConfigPath = ".gandalf/git.json"

// DefaultRemote is the remote name used when none is set.
const DefaultRemote = "origin"

// DefaultSyncInterval is how often serve pulls and pushes when a remote is set.
const DefaultSyncInterval = 5 * time.Minute

// Config is what a vault declares about its git remote and sync behaviour.
type Config struct {
	// Enabled turns automatic commits and sync on. Default true when the file
	// is absent but a repo exists; an explicit false stops everything.
	Enabled *bool `json:"enabled,omitempty"`

	// Remote is the remote name, usually "origin".
	Remote string `json:"remote,omitempty"`

	// URL is the remote's URL. Empty means commit locally only.
	URL string `json:"url,omitempty"`

	// SyncInterval is how often to pull and push, as a Go duration string.
	SyncInterval string `json:"sync_interval,omitempty"`

	// Conflict is the pull strategy. Only "remote-wins" is supported.
	Conflict string `json:"conflict,omitempty"`
}

// IsEnabled reports whether automatic git maintenance is on.
func (c Config) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// RemoteName returns the configured remote, or origin.
func (c Config) RemoteName() string {
	if c.Remote == "" {
		return DefaultRemote
	}
	return c.Remote
}

// Interval returns the sync cadence, falling back to the default.
func (c Config) Interval() time.Duration {
	if c.SyncInterval == "" {
		return DefaultSyncInterval
	}
	d, err := time.ParseDuration(c.SyncInterval)
	if err != nil || d <= 0 {
		return DefaultSyncInterval
	}
	return d
}

// LoadConfig reads a vault's git settings. A missing file is an empty config,
// not an error: that is the state before anyone has configured a remote.
func LoadConfig(root string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ConfigPath)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Config{}, nil
	case err != nil:
		return Config{}, fmt.Errorf("read git config: %w", err)
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse git config at %s: %w", ConfigPath, err)
	}
	return c, nil
}

// SaveConfig writes a vault's git settings.
func SaveConfig(root string, c Config) error {
	if c.Conflict == "" {
		c.Conflict = "remote-wins"
	}
	if c.Remote == "" {
		c.Remote = DefaultRemote
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode git config: %w", err)
	}
	data = append(data, '\n')

	abs := filepath.Join(root, filepath.FromSlash(ConfigPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create git config directory: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("write git config: %w", err)
	}
	return nil
}

// rootIgnore keeps Obsidian and editor debris out of the vault's history.
const rootIgnore = `# Editor and Obsidian state — not notes.
.obsidian/
.DS_Store
*.swp
*~
`

// ensureRootIgnore writes a root .gitignore when the vault has none.
func ensureRootIgnore(root string) error {
	abs := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(abs); err == nil {
		return nil
	}
	return os.WriteFile(abs, []byte(rootIgnore), 0o644)
}

// normalizeURL trims whitespace from a remote URL.
func normalizeURL(url string) string {
	return strings.TrimSpace(url)
}
