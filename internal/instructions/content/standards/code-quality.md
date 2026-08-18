# Code Quality

## Size and Shape

- Files under roughly 300 lines. Past 500 is a smell; past 1000, split it.
- One concern per file. Functions do one thing.
- Prefer early returns to nested conditionals.

## Formatting and Linting

- Code passes the project's linter on every change, not at the end.
- Use the language's standard formatter and do not argue with it.
- Delete commented-out code. Version control is the safety net.

## Dead Code

Delete it. Unused functions, types, variables, imports, and flags all go. Code that
exists "for later" is code nobody tests and everybody has to read.

## Documentation

Documentation lives with the code, because documentation that lives elsewhere drifts.

- Document assumptions, algorithms, contracts, and anything non-obvious at the entry
  point.
- Use the language's documentation convention on exported symbols.
- Do not restate what the code plainly says.
- No TODO comments without something that tracks them.

Comments explain why. The code already says what.

## Errors

Errors travel up with context and are logged once, at the boundary.

- Never log an error and return it as well. Choose one.
- Wrap errors at each layer with enough context to locate the failure.
- Never swallow an error silently. If ignoring one is deliberate, say why in a comment.
- Distinguish bad input from infrastructure failure — callers need to tell them apart.
- Avoid panicking in library or domain code.

## Testing

- Treat 80% statement coverage as a floor, not a target. An untested critical path
  matters more than the number.
- Co-locate tests with the code they test.
- Tests document behaviour, not implementation. A test that breaks on every refactor
  is testing the wrong thing.
- Mock at adapter boundaries only. Never mock domain logic.
- Use table-driven tests for anything with multiple cases.
- Use golden files for large structured output, where a diff is more readable than a
  set of assertions.

## Build Artifacts

Generated output stays out of version control — bundles, compiled binaries, dist
directories. Wire generation into the build instead.

Where an asset must exist at compile time, commit a placeholder so a bare checkout
still builds and degrades with an honest "not built" surface rather than a stale
committed artifact. Committed code generation is the deliberate exception when it is
reviewable source and CI checks it is current.

## Leaving It Better

Fix what you touch. Flag significant debt rather than silently working around it, and
say what it would take to fix.
