# TypeScript

- Follow the conventions of the framework in use. Do not fight it.
- `strict: true`. No `any` without a comment explaining why it is unavoidable.
- Prefer `unknown` over `any` when a type is genuinely unknown, and narrow it before
  use.
- Validate at runtime on every system boundary with a schema library. Types are erased
  at runtime; incoming data is unvalidated until something validates it.
- Handle errors in every async path. An unhandled rejection is an outage with a delay
  on it.
- Use structured logging in services. Keep `console.log` out of production paths.
- Prefer discriminated unions over optional-field soup, and let the compiler check the
  cases are covered.
- Model absence explicitly. `null`, `undefined`, and "the key is missing" are three
  different things, and picking one deliberately saves an afternoon later.
