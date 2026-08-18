# Architecture

## Ports and Adapters

The default shape for any non-trivial service. The domain is the centre; everything
else is an adapter.

- Define boundaries as interfaces. Components implement them.
- Keep domain logic free of infrastructure: no database calls, no HTTP clients, no
  logging framework in domain code.
- Separate packages for each adapter and for the domain.
- Dependencies point inward. Adapters depend on the domain; the domain depends on
  nothing external.

Test the domain against in-memory adapters, and the real adapters with integration
tests. Neither needs a mocking framework.

## Systems

- Enforce clear ownership boundaries between services and modules.
- Services communicate through explicit contracts — HTTP or RPC interfaces, event
  schemas. Implementation details do not cross a service boundary.
- Treat event schemas as first-class contracts. Version them, document them.
- Design for failure. Assume every downstream call can fail, and decide what happens
  when it does before it does.
- Treat services that share a database or call each other synchronously for every
  operation as one service that has been split by mistake.

## Choosing Complexity

Start with a single well-organised binary. Split it in response to a stated constraint
— independent scaling, independent deployment, separate ownership — not as a milestone.
