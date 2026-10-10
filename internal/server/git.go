package server

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GitRemoteInput configures a vault's git remote.
type GitRemoteInput struct {
	URL   string `json:"url" jsonschema:"the remote repository URL; pass an empty string to clear it, which stops push and pull while local commits continue"`
	Vault string `json:"vault,omitempty" jsonschema:"a team slug to configure that team's vault; omit for your personal vault"`
}

// GitRemoteOutput reports the resulting configuration.
type GitRemoteOutput struct {
	Remote       string `json:"remote"`
	URL          string `json:"url,omitempty"`
	SyncInterval string `json:"sync_interval"`
	Conflict     string `json:"conflict"`
	Note         string `json:"note,omitempty"`
}

// RemoteSetter changes one vault's git remote and reports the result.
//
// scope is the team slug the vault belongs to, or "" for the personal vault.
// It is a function type so that hosted mode can replace the mechanism, for
// example with per-vault deploy keys, while the tool keeps owning routing,
// access, policy and the shape of the reply. A replacement that touches the
// working tree takes the core's BeginWrite lock for exactly the phases the
// default does.
type RemoteSetter func(ctx context.Context, core *Core, scope, url string) (GitRemoteOutput, error)

// gitRemote sets or clears a vault's git remote. Gandalf owns the commits;
// this is the one git decision a model is allowed to make.
//
// Whether a session may make it is the policy's to say, and is asked after the
// vault is resolved so that the policy sees the vault it is deciding about.
func (s *Server) gitRemote(ctx context.Context, _ *sdk.CallToolRequest, in GitRemoteInput) (*sdk.CallToolResult, GitRemoteOutput, error) {
	sc, err := s.vaultFor(ctx, in.Vault)
	if err != nil {
		return nil, GitRemoteOutput{}, err
	}

	if s.GitRemotePolicy != nil {
		if err := s.GitRemotePolicy(ctx, sc.slug); err != nil {
			return nil, GitRemoteOutput{}, fmt.Errorf("git_remote is not permitted: %w", err)
		}
	}

	set := s.SetRemote
	if set == nil {
		set = DefaultSetRemote
	}

	out, err := set(ctx, sc.Core, sc.slug, in.URL)
	if err != nil {
		return nil, GitRemoteOutput{}, err
	}
	return nil, out, nil
}

// DefaultSetRemote configures the remote on the vault's own repository, and
// tries one sync so a bad URL surfaces now rather than at the next interval.
func DefaultSetRemote(ctx context.Context, c *Core, _ string, url string) (GitRemoteOutput, error) {
	unlock := c.beginWrite()
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	if c.git == nil {
		return GitRemoteOutput{}, fmt.Errorf(
			"this vault is not under git for this process; restart without -no-git, or run `gandalf init`")
	}

	cfg, err := c.git.SetRemote(url)
	if err != nil {
		return GitRemoteOutput{}, err
	}

	out := GitRemoteOutput{
		Remote:       cfg.RemoteName(),
		URL:          cfg.URL,
		SyncInterval: cfg.Interval().String(),
		Conflict:     "remote-wins",
	}
	if cfg.URL == "" {
		out.Note = "remote cleared; local commits continue, push and pull are idle until a URL is set again"
		return out, nil
	}

	out.Note = "remote configured; the server will pull (remote-wins on conflict) and push on its sync interval"

	// Try an immediate sync so a bad URL surfaces now rather than later. The
	// change itself is committed, so the lock is released first: the sync
	// takes it back for the phases that touch the tree, and talks to the
	// network without it, exactly as the periodic sync does.
	unlock()
	unlock = nil
	if err := c.git.Sync(ctx, &c.write); err != nil {
		out.Note = fmt.Sprintf("remote saved, but the first sync failed: %v", err)
	}
	return out, nil
}
