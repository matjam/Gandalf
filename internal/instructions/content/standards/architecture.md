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

The payoff is testability without mocking frameworks: the domain runs against
in-memory adapters, and the real adapters are thin enough to be worth integration
testing on their own.

## Systems

- Enforce clear ownership boundaries between services and modules.
- Services communicate through explicit contracts — HTTP or RPC interfaces, event
  schemas. Implementation details do not cross a service boundary.
- Treat event schemas as first-class contracts. Version them, document them.
- Design for failure. Assume every downstream call can fail, and decide what happens
  when it does before it does.
- Tightly coupled services that share data or call each other synchronously for every
  operation are a distributed monolith wearing a service diagram. Notice it early.

## Choosing Complexity

Match the structure to the actual problem. A single well-organised binary is a
legitimate architecture and usually the right one to start from. Splitting it is a
response to a real constraint — independent scaling, independent deployment,
separate ownership — not a milestone to reach.
