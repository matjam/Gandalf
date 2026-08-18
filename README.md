# Gandalf

Gandalf is an MCP server that gives a coding agent a durable, structured memory:
an Obsidian-compatible markdown vault where the *tool* owns note metadata, so the
model spends its attention on the work instead of on frontmatter bookkeeping.

It ships an operating instruction set — GandalfOS — that is seeded into the vault
on first use and served back to the model through `boot` at the start of a
session.

**Status: working, early.** Everything below runs. It has not yet been used in anger
across a long-lived vault, so expect rough edges rather than data loss: notes are
plain files, and everything Gandalf derives from them can be rebuilt.

## Why

Markdown vaults work well as agent memory right up until the model has to
maintain them. Frontmatter drifts, tags fragment into six spellings of the same
idea, wikilinks rot, and every session spends context re-reading conventions it
then half-applies.

Gandalf's position is that this is administrative work, and administrative work
belongs in a tool:

- **Markdown stays canonical.** Every note is a plain file you can read, edit in
  Obsidian, and keep in git. Gandalf is not a database with a markdown export.
- **The tool owns metadata.** Notes are created and updated through tool calls
  that generate valid frontmatter. The model writes prose.
- **Notes are addressed by ref, never by path.** `session:2026-08-17-cache-work`,
  not `Sessions/2026/08/...`. A model given a path parameter will use it, and will
  then invent paths of its own.
- **Structure is enforced, not documented.** A schema violation is a lint
  finding, not a paragraph the model may or may not follow.
- **Recall is semantic.** Finding the note that matters should not depend on
  guessing the words it was written with.

## Install

Requires Go 1.26 or newer.

```
git clone https://github.com/matjam/gandalf
cd gandalf
brew install onnxruntime   # macOS: enables the fast embedding backend
make build                 # -> bin/gandalf
install -m755 bin/gandalf ~/.local/bin/gandalf
```

`make build` says which embedding backend it built with. `make doctor-deps` reports
what the fast path is missing without building anything. See
[Which engine does the embedding](#which-engine-does-the-embedding) for why it
matters.

Then create a vault:

```
gandalf init -vault ~/Documents/Vaults/Gandalf
```

That writes the GandalfOS documents, a category declaration, and a seed ledger. It
never overwrites a file that already exists. It also initialises a git repository
for the vault so every later change can be committed automatically. Pass
`-git-remote URL` to configure a remote up front, or `-no-git` to skip.

## Version control

The model does not run git. Gandalf does:

- `init` and `serve` create a repository when one is missing.
- Every MCP mutation (and the end of `import` / `update`) becomes a commit.
- When a remote is configured, `serve` periodically pulls and pushes. Pull
  conflicts resolve as **remote-wins**.
- The model configures the remote with `git_remote` (or you pass
  `-git-remote` to `init`). Settings live in `.gandalf/git.json`.

Derived search indexes stay out of git via `.gandalf/.gitignore`.

## Connect an agent

Gandalf speaks MCP over stdio. Point your harness at `gandalf serve` with a vault.

### Claude Code

```
claude mcp add gandalf -- gandalf serve -vault ~/Documents/Vaults/Gandalf
```

Add `--scope user` to make it available in every project rather than the current
one. Check it connected with `claude mcp list`.

### Cursor

Add a stdio server to `~/.cursor/mcp.json` (user-global) or `.cursor/mcp.json`
(project-local):

```json
{
  "mcpServers": {
    "gandalf": {
      "type": "stdio",
      "command": "/Users/you/go/bin/gandalf",
      "args": ["serve", "-vault", "/Users/you/Documents/Vaults/Gandalf"]
    }
  }
}
```

Prefer an absolute path for `command` so Cursor does not depend on your shell
`PATH`. Then add a user rule (or `.cursor/rules/*.mdc` with `alwaysApply: true`):

```markdown
Call `boot` before doing anything else, and follow what it returns.
```

Reload MCP from **Settings → MCP**, or restart Cursor, and confirm `gandalf`
shows as connected.

### opencode

Add it to `~/.config/opencode/opencode.json` (or a project's `opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "gandalf": {
      "type": "local",
      "command": ["gandalf", "serve", "-vault", "/home/you/Documents/Vaults/Gandalf"],
      "enabled": true
    }
  }
}
```

### Anything else

Any MCP client that can launch a stdio server will do:

```
gandalf serve -vault /path/to/vault
```

The server seeds the vault on startup unless `-no-seed` is given, so pointing a
fresh client at an empty directory is a complete install step.

### Make the agent call boot

Gandalf works best when the model calls `boot` before anything else — that
is how it receives the operating contract. Most harnesses read a rules file, so add
a line to `CLAUDE.md`, `AGENTS.md`, or the equivalent:

```markdown
Call `boot` before doing anything else, and follow what it returns.
```

## Search

Search runs an embedding model in-process by default, downloading it on first use
and caching it outside the vault. Nothing else needs to be running.

```
gandalf serve -vault DIR                       # in-process model (default)
gandalf serve -vault DIR -embed http           # an OpenAI-compatible endpoint
gandalf serve -vault DIR -embed none           # no search; everything else works
```

The HTTP backend covers Ollama, llama.cpp's server, LM Studio, and anything else
speaking the same shape:

```
gandalf serve -vault DIR -embed http \
  -embed-url http://localhost:11434/v1 \
  -embed-model nomic-embed-text -embed-dims 768 -embed-window 8192
```

The index lives in `.gandalf/`, is excluded from git by a seeded ignore file, and
is rebuilt from the notes whenever it disagrees with them. Changing model rebuilds
it rather than comparing incompatible vectors.

Indexing runs in the background from the moment the server starts. A search issued
before the first pass finishes answers from what has been indexed so far and says
so, rather than blocking; `boot` reports the same, so a session knows whether search
is `ready`, `building`, or `unavailable` before it relies on it.

Build the index up front — worth doing after an import — with:

```
gandalf reindex -vault DIR          # progress and timing per note
gandalf reindex -vault DIR -all     # include notes already up to date
gandalf reindex -vault DIR -quiet   # summary only
```

### Which engine does the embedding

The in-process model runs on one of two compute backends, and the difference is
large:

| Backend | Needs | Speed |
|---|---|---|
| `onnxruntime` | ONNX Runtime and a build with `-tags ORT` | ~37ms per chunk |
| `pure-go` | nothing | ~510ms per chunk |

That is roughly fourteen times, measured on the same vault on Apple silicon: 3.2s
against 44s for 86 chunks, or about ninety seconds against twenty minutes for a
430-note vault. `reindex` and `boot` both name the backend in use, so a slow index
is diagnosable rather than mysterious.

ONNX Runtime is the default on macOS, where `make build` fetches what it needs.
Elsewhere the pure-Go backend is the default, because a build that fails on a fresh
clone is worse than a slow index. If the native pieces are missing the binary still
runs — it falls back rather than refusing to start.

## Importing an existing vault

If you already keep markdown notes, `gandalf import` moves them in — preserving
each note's original dates and rewriting every link to its new home.

```
gandalf import -from ~/Documents/Vaults/Old -vault ~/Documents/Vaults/Gandalf
```

That prints a plan and writes nothing. Add `-apply` once it looks right.

Notes are mapped by rules, or by their own frontmatter type when no rule
matches. A rules file maps source paths onto categories, where `*` captures one
path segment and `**` any number:

```json
{
  "rules": [
    { "match": "Apps/*/Design.md", "category": "project", "scope": "$1", "facet": "design" },
    { "match": "Sessions/**", "category": "session" },
    { "match": "**/README.md", "skip": true }
  ]
}
```

See [examples/agentos-import.json](examples/agentos-import.json) for a complete
set. The importer never overwrites: anything already in the destination is
reported as a conflict and left alone, as are links whose targets were not part
of the import. Run `gandalf lint` afterwards to see what still needs attention.

## Commands

```
gandalf serve   -vault DIR [-no-seed] [-no-git] [embedding flags]
gandalf init    [-vault DIR] [-restore] [-git-remote URL] [-no-git]
gandalf import  -from DIR [-vault DIR] [-rules FILE] [-apply]
gandalf reindex [-vault DIR] [-quiet] [-all] [embedding flags]
gandalf update  [-vault DIR]
gandalf doctor  [-vault DIR]
gandalf lint    [-vault DIR] [-strict] [NOTE...]
```

`init` seeds what is missing and never overwrites. A document you deleted stays
deleted; `-restore` puts it back. It also creates a git repository unless
`-no-git` is given.

`doctor` reports how your copy of each shipped document compares with this build —
unchanged, edited by you, superseded upstream, both, absent, or removed. It only
reads. Divergence is what happens when the vault is working.

`lint` validates metadata, links, and backlinks. Exits non-zero on errors, or on
warnings with `-strict`.

`import` moves an existing vault in, preserving dates and rewriting links. It plans
first and writes nothing without `-apply`.

`reindex` builds the search index up front rather than leaving it to the background
pass a running server does. It reports progress and timing per note; `-all` includes
notes already up to date, `-quiet` prints only the summary.

`update` adopts this build's text for shipped documents you have not edited, leaving
edited ones alone, and rewrites retired tool names in the documents it leaves — so a
contract you have corrected keeps naming tools that exist.

## The vault

```
Gandalf/     the operating contract and its topics
Standards/   engineering standards, seeded with opinionated defaults
Projects/    one folder per project: design, decisions, todo
Sessions/    one note per unit of work, filed by date
Meetings/    notes from conversations with other people
.gandalf/    categories, seed ledger, search index
```

Those folders come from the categories the vault declares in
`.gandalf/categories.json`. Add your own, rename them, or retire the ones you do
not use — what kinds of note the vault keeps is your decision, and the tools
follow the declaration rather than a list baked into the binary.

Seeded documents are a starting point. Once a file is in your vault it wins, so
corrections you make during a session persist across releases.

## Layout

```
cmd/gandalf/            command line entry point
internal/category/      what kinds of note exist and how each is filed
internal/schema/        the frontmatter contract and its validation
internal/vault/         note parsing, refs, wikilinks, backlinks, linting
internal/index/         chunking, SQLite storage, hybrid search
internal/embed/         embedding models behind one interface
internal/instructions/  the GandalfOS documents, seeding, drift reporting
internal/server/        MCP tool handlers
```

## Building

```
make build            # -> bin/gandalf, fast backend where available
make build ORT=0      # force the pure-Go backend
make build ORT=1      # force ONNX Runtime, and fail if it is not available
make deps             # fetch the prebuilt tokenizer archive only
make doctor-deps      # report what the fast path is missing
make check            # vet + tests
```

The pure-Go build has no native dependencies and works with `CGO_ENABLED=0` on
Linux, macOS, and Windows.

The ONNX Runtime build needs two native pieces: the `onnxruntime` shared library
(`brew install onnxruntime`, or your distribution's package), and `libtokenizers.a`,
a Rust static archive linked at build time. `make build` fetches the prebuilt
archive for platforms that have one and points the linker at it; on anything else
you will need to build it from [daulet/tokenizers](https://github.com/daulet/tokenizers).

Set `GANDALF_ONNXRUNTIME` to the directory holding the shared library if it is
installed somewhere the build does not look.

Tests that download the embedding model are opt-in:

```
GANDALF_MODEL_TESTS=1 go test ./internal/embed/
```

## Licence

MIT. See [LICENSE](LICENSE).
