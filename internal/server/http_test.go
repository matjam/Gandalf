package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

const testToken = "correct-horse-battery-staple"

// httpHarness is one vault served over the real HTTP transport, with clients
// connecting as separate sessions the way agents on different machines do.
type httpHarness struct {
	t      *testing.T
	server *Server
	url    string
	ctx    context.Context
}

func newHTTPHarness(t *testing.T) *httpHarness {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := instructions.Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	s := New(v, "test")
	ts := httptest.NewServer(s.handler(testToken))
	t.Cleanup(ts.Close)

	return &httpHarness{t: t, server: s, url: ts.URL, ctx: context.Background()}
}

// connect opens one MCP session over HTTP, carrying the given token.
func (h *httpHarness) connect(token string) *sdk.ClientSession {
	h.t.Helper()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(h.ctx, &sdk.StreamableClientTransport{
		Endpoint:   h.url,
		HTTPClient: &http.Client{Transport: bearer{token: token}},
	}, nil)
	if err != nil {
		h.t.Fatalf("Connect: %v", err)
	}
	h.t.Cleanup(func() { session.Close() })
	return session
}

// call invokes a tool on one session, failing the test if it errors.
func (h *httpHarness) call(s *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	h.t.Helper()

	res, err := s.CallTool(h.ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		h.t.Fatalf("%s reported an error: %s", name, text(res))
	}
	return res
}

// callErr invokes a tool expecting a refusal, and returns the message.
func (h *httpHarness) callErr(s *sdk.ClientSession, name string, args map[string]any) string {
	h.t.Helper()

	res, err := s.CallTool(h.ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		h.t.Fatalf("%s succeeded, want an error", name)
	}
	return text(res)
}

// bearer adds an Authorization header to every request, standing in for the
// header an MCP client is configured with.
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func TestHTTPRefusesARequestWithoutTheRightToken(t *testing.T) {
	h := newHTTPHarness(t)

	cases := []struct {
		name       string
		authHeader string
	}{
		{"no header at all", ""},
		{"the wrong token", "Bearer wrong"},
		{"the token with no scheme", testToken},
		{"the wrong scheme", "Basic " + testToken},
		{"a lowercase scheme", "bearer " + testToken},
		{"a prefix of the token", "Bearer " + testToken[:10]},
		{"the token with something appended", "Bearer " + testToken + "x"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, h.url, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			req.Header.Set("Content-Type", "application/json")

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
			}
			if got := res.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
			}
		})
	}
}

func TestHTTPServesTheToolsToAnAuthorisedClient(t *testing.T) {
	h := newHTTPHarness(t)
	session := h.connect(testToken)

	res := h.call(session, "boot", map[string]any{})
	if !strings.Contains(text(res), "Operating Contract") {
		t.Errorf("boot over HTTP did not return the contract: %s", text(res))
	}

	tools, err := session.ListTools(h.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools offered over HTTP")
	}
}

// TestEachHTTPSessionHasItsOwnReadRecord is the property the per-session split
// exists for: over one shared vault, one agent having read a note must not let
// another agent replace part of it.
func TestEachHTTPSessionHasItsOwnReadRecord(t *testing.T) {
	h := newHTTPHarness(t)

	reader := h.connect(testToken)
	other := h.connect(testToken)

	h.call(reader, "note_new", map[string]any{
		"kind":    "project",
		"scope":   "example",
		"facet":   "design",
		"title":   "Example — Design",
		"content": "## Shape\n\nAs first written.\n",
		"reason":  "set up the test",
	})

	const ref = "project:example:design"
	h.call(reader, "note_read", map[string]any{"ref": ref})

	msg := h.callErr(other, "note_replace", map[string]any{
		"ref":     ref,
		"section": "Shape",
		"content": "Rewritten by a session that never read it.\n",
		"reason":  "should be refused",
	})
	if !strings.Contains(msg, "note_read") {
		t.Errorf("second session was not asked to read first: %s", msg)
	}

	// The session that did read it is unaffected.
	h.call(reader, "note_replace", map[string]any{
		"ref":     ref,
		"section": "Shape",
		"content": "Rewritten by the session that read it.\n",
		"reason":  "the reader may replace",
	})
}

// TestConcurrentWritesFromSeparateSessionsAllLand covers the write lock: each
// append is a read-modify-write of the same note, so without serialisation
// some of them are lost.
func TestConcurrentWritesFromSeparateSessionsAllLand(t *testing.T) {
	h := newHTTPHarness(t)

	setup := h.connect(testToken)
	h.call(setup, "note_new", map[string]any{
		"kind":    "project",
		"scope":   "busy",
		"facet":   "todo",
		"title":   "Busy — Todo",
		"content": "## Backlog\n",
		"reason":  "set up the test",
	})

	const writers = 8

	sessions := make([]*sdk.ClientSession, writers)
	for i := range sessions {
		sessions[i] = h.connect(testToken)
	}

	var wg sync.WaitGroup
	for i, session := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()

			line := fmt.Sprintf("- item from writer %d", i)
			if _, err := session.CallTool(h.ctx, &sdk.CallToolParams{
				Name: "note_append",
				Arguments: map[string]any{
					"ref":     "project:busy:todo",
					"content": line,
					"reason":  "concurrent append",
				},
			}); err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	res := h.call(setup, "note_read", map[string]any{"ref": "project:busy:todo"})
	body := text(res)

	for i := range writers {
		want := fmt.Sprintf("item from writer %d", i)
		if !strings.Contains(body, want) {
			t.Errorf("append from writer %d was lost", i)
		}
	}
}

func TestRunHTTPRefusesAnIncompleteConfiguration(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cases := []struct {
		name string
		cfg  HTTPConfig
		want string
	}{
		{"no token", HTTPConfig{Addr: "127.0.0.1:0"}, "bearer token"},
		{"a blank token", HTTPConfig{Addr: "127.0.0.1:0", Token: "   "}, "bearer token"},
		{"no address", HTTPConfig{Token: testToken}, "address"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := New(v, "test").RunHTTP(t.Context(), c.cfg)
			if err == nil {
				t.Fatal("RunHTTP started, want a refusal")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}
