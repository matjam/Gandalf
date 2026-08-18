# Security

## Input Validation

- Validate at the edge — HTTP handlers, CLI arguments, queue consumers. Domain logic
  is entitled to assume its inputs are already valid.
- Use the ecosystem's standard validation library at that boundary rather than
  hand-rolled checks scattered through handlers.
- Never log raw user input at warning or error level. It may carry credentials or
  personal data, and error paths are exactly where nobody is looking.

## Secrets

- Secrets are never committed. No exceptions, including "temporarily".
- Fetch secrets at runtime from the platform's secret store.
- Local development may use an ignored environment file, with a checked-in example
  containing placeholders.
- Secrets never appear in logs, error messages, or stack traces.
- A secret that has been committed is compromised. Rotate it; removing the commit is
  not a fix.

## Client Bundles

In any ecosystem that ships code to a browser, the module graph can pull a
secret-reading module into a client bundle without anyone intending it.

- Guard server-only modules with the framework's mechanism for that.
- Use type-only imports for types that cross the server/client boundary.
- Keep client-safe constants and pure functions in modules that read no environment.
- After building, search the client output for environment variable names and known
  secret literals. This is a mechanical check and it belongs in CI.

This does not apply to server binaries that ship no client code.

## Dependencies

- Keep dependencies current. A stale dependency is a liability with a CVE attached.
- Prefer writing it yourself over adding a dependency, unless the dependency is
  well-maintained and widely used. Prefer the dependency over writing your own
  cryptography, always.
- Run vulnerability audits in CI, not occasionally by hand.
- Check the current version before pinning. Do not pin to a version you remember.

## Static Analysis

When a finding is intentional and justified, suppress it on the exact line, naming the
rule and the reason. Refactor to satisfy a scanner only when the result is better code
on its own merits.

## Authentication and Authorisation

- Authenticate at the edge; authorise at the point of access to the resource.
- Deny by default. A missing permission check should fail closed.
- Scope credentials to the least they need, and make them expire.
- Never roll your own session or token scheme when the platform provides one.
