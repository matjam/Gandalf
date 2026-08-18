# Gandalf

Gandalf is an MCP server that gives a coding agent a durable, structured memory:
an Obsidian-compatible markdown vault where the *tool* owns note metadata, so the
model spends its attention on the work instead of on frontmatter bookkeeping.

It ships an operating instruction set — GandalfOS — that is seeded into the vault
on first use and served back to the model through a `gandalf_boot` tool call at
the start of a session.

**Status: early. Phase 1 of 5.** The note model, schema, and linter are built and
tested; the instruction set, MCP server, and semantic search are not. The `lint`
subcommand is the only thing you can run today.

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
- **Structure is enforced, not documented.** A schema violation is a lint
  finding, not a paragraph of instructions the model may or may not follow.
- **Recall is semantic.** Finding the note that matters should not depend on
  guessing the words it was written with.

## Design

| Decision | Choice |
|---|---|
| Distribution | Single static Go binary, stdio MCP server |
| Storage | Obsidian-compatible markdown; frontmatter as YAML |
| Index | SQLite, machine-local, rebuildable from the vault |
| Embeddings | Pluggable: in-process by default, any OpenAI-compatible endpoint otherwise |
| Instruction set | Embedded in the binary, seeded into the vault, then vault-authoritative |

Seeded instructions are a starting point, not a lock: once a file is in your
vault it wins, so corrections you make during a session persist. Gandalf reports
drift from the shipped defaults but never overwrites your edits.

## Layout

```
cmd/gandalf/       command line entry point
internal/schema/   frontmatter contract: fields, enums, validation
internal/vault/    note parsing and rendering, filing conventions, linting
```

## Building

Requires Go 1.26 or newer.

```
make build      # -> bin/gandalf
make check      # vet + tests
```

## Usage

```
gandalf lint [-vault DIR] [-strict] [NOTE...]
```

Validates note metadata and links. Errors are contract violations — an unknown
note type, a missing required field, a `related` entry pointing nowhere.
Warnings are things that are legal but probably unintended, such as an untagged
note or a dead link in prose. Exits non-zero on errors, or on warnings with
`-strict`.

## Licence

MIT. See [LICENSE](LICENSE).
