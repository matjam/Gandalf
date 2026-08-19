package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPConfig says where to listen and what credential to require.
type HTTPConfig struct {
	// Addr is the listen address, such as 127.0.0.1:8760 or a LAN or
	// Tailscale address. It is passed through as given: which interface the
	// vault is reachable on is the operator's decision, not a default.
	Addr string

	// Token is the bearer token every request must carry. There is no
	// unauthenticated mode: the thing being served is the user's notes, with
	// tools that rewrite and delete them.
	Token string
}

// httpTimeouts bound a request that never finishes.
//
// Only the header read is bounded. A streamable MCP session holds its response
// open for as long as the client is connected, so a write or whole-request
// deadline would cut working sessions off mid-stream.
const (
	httpReadHeaderTimeout = 10 * time.Second
	httpIdleTimeout       = 2 * time.Minute
)

// sessionTimeout closes an MCP session that has gone quiet.
//
// Each session holds a record of what it has read, so sessions that are never
// closed accumulate. Losing that record is a recoverable failure: a client
// coming back after the timeout is told to read the note again before
// replacing part of it, which is what it should do anyway after that long.
const sessionTimeout = 30 * time.Minute

// shutdownGrace is how long a stopping server waits for connected sessions.
// Streamable sessions hold their response open, so this bounds a shutdown that
// would otherwise wait for every client to disconnect on its own.
const shutdownGrace = 5 * time.Second

// RunHTTP serves MCP over streamable HTTP until ctx is cancelled.
//
// Every connection gets its own session over the same vault, so what one agent
// has read does not satisfy another agent's read-before-write gate, while the
// index, the repository, and the write lock are shared.
func (s *Server) RunHTTP(ctx context.Context, cfg HTTPConfig) error {
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("serving over HTTP needs a bearer token; there is no unauthenticated mode")
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return errors.New("serving over HTTP needs an address to listen on")
	}

	// Indexing starts as the server comes up rather than on the first search,
	// exactly as it does over stdio.
	s.startIndexing(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.handler(cfg.Token),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	slog.Info("gandalf: serving MCP over HTTP", "addr", cfg.Addr, "vault", s.vault.Root())

	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()

	if err := srv.Shutdown(stopping); err != nil {
		// Sessions still streaming will not drain on their own. Closing is the
		// intended end of a shutdown that has waited its grace period, so it is
		// not reported as a failure.
		slog.Info("gandalf: closing connected sessions", "reason", err)
		return srv.Close()
	}
	return nil
}

// handler is what the HTTP transport serves: the MCP tools, one session per
// connection, behind the bearer token.
func (s *Server) handler(token string) http.Handler {
	mcp := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return s.Session().MCP() },
		&sdk.StreamableHTTPOptions{
			SessionTimeout: sessionTimeout,
			Logger:         slog.Default(),
		},
	)
	return requireToken(token, mcp)
}

// requireToken refuses any request that does not carry the bearer token.
//
// Authentication is at the edge and fails closed: a malformed header, a
// missing one, and a wrong token are all the same 401, because saying which
// would help someone guessing. The comparison is constant-time, and the token
// is never logged or echoed.
func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gandalf"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
