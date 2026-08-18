package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/matjam/gandalf/internal/instructions"
	"github.com/matjam/gandalf/internal/schema"
	"github.com/matjam/gandalf/internal/vault"
)

// harness is a client connected to a server over an in-memory transport, so
// tests exercise the real protocol path: schema inference, argument
// validation, and result marshalling all run.
type harness struct {
	t       *testing.T
	client  *sdk.ClientSession
	vault   *vault.Vault
	context context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := instructions.Seed(v, schema.Today(), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	srv := New(v, "test").MCP()
	go func() {
		if err := srv.Run(ctx, serverTransport); err != nil {
			// The transport closes when the test finishes; that is not a failure.
			return
		}
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return &harness{t: t, client: session, vault: v, context: ctx}
}

// call invokes a tool and decodes its structured result into out.
func (h *harness) call(name string, args any, out any) {
	h.t.Helper()

	res, err := h.client.CallTool(h.context, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		h.t.Fatalf("%s reported an error: %s", name, text(res))
	}
	if out == nil {
		return
	}

	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		h.t.Fatalf("%s: marshal result: %v", name, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		h.t.Fatalf("%s: decode result: %v", name, err)
	}
}

// callErr invokes a tool expecting it to fail, and returns the message.
func (h *harness) callErr(name string, args any) string {
	h.t.Helper()

	res, err := h.client.CallTool(h.context, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		h.t.Fatalf("%s succeeded, want an error", name)
	}
	return text(res)
}

// callParams builds the parameters for a tool call.
func callParams(name string, args any) *sdk.CallToolParams {
	return &sdk.CallToolParams{Name: name, Arguments: args}
}

// writeUnmanaged plants a valid note at a path following none of the vault's
// filing conventions, which Gandalf must tolerate without adopting.
func writeUnmanaged(t *testing.T, h *harness, rel string) {
	t.Helper()

	const note = "---\ntype: standard\ncreated: 2026-01-01\nupdated: 2026-01-01\ntags: [personal]\nauthor: user\n---\n\n# Unmanaged\n"

	abs := filepath.Join(h.vault.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(note), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

// text renders a result's content as a string.
func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsAreRegistered(t *testing.T) {
	h := newHarness(t)

	res, err := h.client.ListTools(h.context, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}

	for _, want := range []string{
		"gandalf_boot", "gandalf_topic", "gandalf_note_read", "gandalf_session_start",
		"gandalf_note_new", "gandalf_note_append", "gandalf_note_update",
		"gandalf_lint", "gandalf_correct",
	} {
		if !got[want] {
			t.Errorf("%s is not registered", want)
		}
	}
}

func TestBoot(t *testing.T) {
	h := newHarness(t)

	var out BootOutput
	h.call("gandalf_boot", BootInput{}, &out)

	if out.Vault != h.vault.Root() {
		t.Errorf("vault = %q, want %q", out.Vault, h.vault.Root())
	}
	if out.Version != instructions.Version {
		t.Errorf("version = %d, want %d", out.Version, instructions.Version)
	}
	if len(out.Contract) == 0 {
		t.Fatal("no contract returned")
	}
	if len(out.Topics) == 0 {
		t.Error("no topics returned")
	}
	if len(out.Notices) != 0 {
		t.Errorf("a seeded vault should boot without notices: %v", out.Notices)
	}
	if !strings.Contains(out.Addressing, "ref") {
		t.Error("boot does not explain how notes are addressed")
	}

	// The contract must come from the vault, since that is where corrections
	// live.
	for _, doc := range out.Contract {
		if doc.Source != "vault" {
			t.Errorf("%s came from %q, want vault", doc.ID, doc.Source)
		}
		if !strings.HasPrefix(strings.TrimSpace(doc.Content), "# ") {
			t.Errorf("%s content does not start with a heading", doc.ID)
		}
	}

	// Every advertised topic must be fetchable by the ref boot handed out.
	for _, topic := range out.Topics {
		var got TopicOutput
		h.call("gandalf_topic", TopicInput{Ref: topic.Ref}, &got)
		if got.Content == "" {
			t.Errorf("%s returned no content", topic.Ref)
		}
	}
}

// TestBootPrefersTheVaultOverTheBinary is the property that makes corrections
// durable: an edited contract must be what the model receives.
func TestBootPrefersTheVaultOverTheBinary(t *testing.T) {
	h := newHarness(t)

	const marker = "Local rule: never rebase shared branches."
	note, err := h.vault.Read("Gandalf/Operating.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	note.Append("", marker)
	if err := h.vault.Write(note); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var out BootOutput
	h.call("gandalf_boot", BootInput{}, &out)

	for _, doc := range out.Contract {
		if doc.ID == "operating" {
			if !strings.Contains(doc.Content, marker) {
				t.Error("boot returned the shipped contract instead of the vault's")
			}
			return
		}
	}
	t.Fatal("no operating document in the contract")
}

func TestBootFallsBackWhenUnseeded(t *testing.T) {
	v, err := vault.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := New(v, "test")
	_, out, err := s.boot(context.Background(), nil, BootInput{})
	if err != nil {
		t.Fatalf("boot: %v", err)
	}

	if len(out.Contract) == 0 {
		t.Fatal("an unseeded vault returned no contract")
	}
	for _, doc := range out.Contract {
		if doc.Source != "shipped" {
			t.Errorf("%s came from %q, want shipped", doc.ID, doc.Source)
		}
	}
	if len(out.Notices) == 0 {
		t.Error("no notice that the vault is unseeded")
	}
}

func TestTopicAcceptsBareID(t *testing.T) {
	h := newHarness(t)

	var withPrefix, bare TopicOutput
	h.call("gandalf_topic", TopicInput{Ref: "topic:shipping"}, &withPrefix)
	h.call("gandalf_topic", TopicInput{Ref: "shipping"}, &bare)

	if withPrefix.Content != bare.Content {
		t.Error("a bare topic id returned different content from its ref")
	}
	if msg := h.callErr("gandalf_topic", TopicInput{Ref: "topic:nonexistent"}); !strings.Contains(msg, "no topic") {
		t.Errorf("error = %q, want it to name the missing topic", msg)
	}
}
