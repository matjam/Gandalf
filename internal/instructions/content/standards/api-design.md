# API Design

## HTTP

- Version in the URL path (`/v1/`). Not in headers, not in query parameters.
- Plural nouns for collections (`/users`, `/orders`). Kebab-case for multi-word
  resources.
- Use methods for what they mean: GET is safe and idempotent, POST creates, PUT
  replaces, PATCH updates part, DELETE removes. Never GET with side effects.
- Status codes: 200 success, 201 created, 204 no content, 400 malformed, 401
  unauthenticated, 403 unauthorised, 404 not found, 409 conflict, 422 validation
  failure, 500 unexpected.
- One error shape across the whole API, carrying a stable machine-readable code and a
  human-readable message:

  ```json
  { "error": { "code": "VALIDATION_FAILED", "message": "..." } }
  ```

- Paginate unbounded collections with cursors, and always return the next cursor.
  Offset pagination silently skips and repeats rows while the underlying data changes.

## RPC

- Use RPC for internal service-to-service calls where performance or streaming
  matters; use HTTP for external clients and browsers.
- Schema files are contracts. Version them; never break one without a migration path.
- Lint schemas and check for breaking changes mechanically, in CI.

## Compatibility

Adding an optional field is compatible. Removing a field, renaming one, narrowing a
type, or making an optional field required is not — regardless of what the code does
when you try it locally.

Publish a deprecation before removing anything, and give consumers a version that
warns before the version that breaks.
