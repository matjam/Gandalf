# Vault

Agent memory for this workspace. Plain markdown, readable in Obsidian or any editor,
kept in version control.

| Folder | Contents |
|---|---|
| `Gandalf/` | The operating contract and its topics — how the agent works |
| `Standards/` | Engineering standards the agent applies to code |
| `Projects/` | One folder per project: design, decisions, todo |
| `Sessions/` | A note per unit of work, filed by date |
| `Meetings/` | Notes from conversations with other people |

Those folders come from the categories this vault declares, in
`.gandalf/categories.json`. Add your own, rename them, or retire the ones you do not
use: what kinds of note the vault keeps is your decision, not Gandalf's.

Each note ends with a `## Backlinks` section listing what points at it. Gandalf
maintains that block, so edits inside it are overwritten — everything above it is
yours.

## How It Works

The agent calls `gandalf_boot` at the start of a session, which returns the operating
contract and a list of topics it can fetch as the work demands. Notes are created and
updated through tool calls that own the metadata, so frontmatter stays valid without
anyone maintaining it.

## It Is Yours Now

These files were seeded from the version shipped with Gandalf, and from here on the
vault wins. Edit anything. When you correct the agent, the correction is written into
these files and it applies from then on.

`gandalf doctor` reports where your vault has diverged from the shipped defaults and
what new topics later versions added. It never overwrites your edits — divergence is
the expected state, not a problem to be repaired.
