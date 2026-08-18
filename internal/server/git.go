package server

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GitRemoteInput configures the vault's git remote.
type GitRemoteInput struct {
	// URL is the remote repository URL. Pass an empty string to clear it and
	// stop push/pull while keeping local commits.
	URL string `json:"url"`
}

// GitRemoteOutput reports the resulting configuration.
type GitRemoteOutput struct {
	Remote       string `json:"remote"`
	URL          string `json:"url,omitempty"`
	SyncInterval string `json:"sync_interval"`
	Conflict     string `json:"conflict"`
	Note         string `json:"note,omitempty"`
}

// gitRemote sets or clears the vault's git remote. Gandalf owns the commits;
// this is the one git decision a model is allowed to make.
func (s *Server) gitRemote(ctx context.Context, _ *sdk.CallToolRequest, in GitRemoteInput) (*sdk.CallToolResult, GitRemoteOutput, error) {
	if s.git == nil {
		return nil, GitRemoteOutput{}, fmt.Errorf(
			"this vault is not under git for this process; restart without -no-git, or run `gandalf init`")
	}

	cfg, err := s.git.SetRemote(in.URL)
	if err != nil {
		return nil, GitRemoteOutput{}, err
	}

	out := GitRemoteOutput{
		Remote:       cfg.RemoteName(),
		URL:          cfg.URL,
		SyncInterval: cfg.Interval().String(),
		Conflict:     "remote-wins",
	}
	if cfg.URL == "" {
		out.Note = "remote cleared; local commits continue, push and pull are idle until a URL is set again"
	} else {
		out.Note = "remote configured; the server will pull (remote-wins on conflict) and push on its sync interval"
		// Try an immediate sync so a bad URL surfaces now rather than later.
		if err := s.git.Sync(); err != nil {
			out.Note = fmt.Sprintf("remote saved, but the first sync failed: %v", err)
		}
	}
	return nil, out, nil
}
