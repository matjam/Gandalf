# Shipping

Git, verification, and pull requests.

## Commits

- Commit after each coherent change during a long session. Do not batch hours of work
  into a single commit.
- Commit when asked. Standing authorisation for a specific repository is a decision
  the user makes, and once made, stop asking again for that repository.
- Stage the whole repository when preparing an authorised commit rather than
  selectively staging; do not disturb unrelated changes in the process.
- Target every command at an explicit repository rather than trusting the shell's
  current directory.
- Write imperative subjects under 72 characters. Say what changed and why it changed;
  the diff already says how.

## Branches

- Local-only projects may work on the default branch. Projects with a remote use
  feature branches and pull requests.
- Fetch before branching, and branch from the current upstream default.
- Never force-push a shared branch, skip hooks, or amend a pushed commit without
  explicit approval.
- Never restore a stash unless the current operation created it and you can identify
  it.

## Verification

Before pushing:

- Run the full test suite, including integration tests, plus lint, format, build, and
  any required hooks.
- Run verification commands untruncated. Do not pipe them through anything that hides
  output or masks the exit status.
- Verify packaging, embedded assets, and generated files from a clean checkout. A
  dirty working tree conceals inputs that were never committed.
- Treat a failing test as evidence: fix the regression, or update the test only when
  behaviour intentionally changed, and explain the change.
- Fix bugs test-first. Write the failing test that reproduces the bug, then fix it.

Compiling is not verifying. Type checks, linters, and unit tests prove the code
assembles; they say nothing about credentials, network paths, deployment wiring, or
integration behaviour. Say "compiles, not yet run" until runtime behaviour is actually
observed.

## Pull Requests

- Open the PR as soon as the branch is pushed — ready, not draft.
- Prefix the title with a conventional type: `fix:`, `feat:`, `chore:`.
- Describe what changed, everything that consumes it, the blast radius, how it was
  verified, and what a reviewer needs to know. Update the description after every push.
- Frame work on a shared component by the component and all its consumers, not only
  the task that prompted the change.
- Ship dependent PRs serially. Create the next only once the previous has merged.
- Before adding commits to an existing branch, confirm its PR is still open. If it
  merged, branch afresh and confirm the earlier work actually landed.
- Watch CI until it passes. Fix failures before reporting the PR as ready.

## Issues Awaiting Verification

Merged is not verified. When a fix still needs checking in a running environment,
reference its issue without an auto-closing keyword, and check the PR body for one
before submitting it. Once verified, post the evidence and close the issue by hand.

## Versioning

Do not bump a major version solely because a change is breaking when the library has
no consumers. Keep the current major and ship it in a minor release.
