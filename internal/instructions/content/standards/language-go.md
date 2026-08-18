# Go

- Follow ports and adapters; see the architecture standard.
- `context.Context` is the first parameter of every function that does I/O or can be
  cancelled.
- Inspect errors with `errors.Is` and `errors.As`. Never match on message text.
- Wrap errors with `%w` and enough context to locate the failure.
- Avoid global state. Inject dependencies through constructors.
- Interfaces belong in the consuming package, not alongside their implementation.
  Define what you need where you need it; keep them small.
- Accept interfaces, return concrete types.
- Use `log/slog` for structured logging, passing the request context so trace
  correlation works. No `fmt.Println` or `log.Printf` in production paths.
- When touching a file that logs through something older, migrate that file.
- In tests, embed the interface in a stub and override only the methods under test
  rather than implementing every method.
- Use table-driven tests. Name the cases; a failure should say which case failed
  without counting brackets.
- Guard concurrency with the race detector in CI, and prefer channels or a mutex over
  clever lock-free schemes nobody can review.
- Keep `CGO_ENABLED=0` unless something genuinely requires cgo. Static binaries are
  most of Go's deployment story.

## Tooling

- Run `gofmt` and `goimports`; do not hand-edit import blocks after a bulk change.
- Run `go vet` on every change and `govulncheck` in CI.
- Prefer the standard library. Reach for a dependency when it is well-maintained and
  the problem is genuinely someone else's.
