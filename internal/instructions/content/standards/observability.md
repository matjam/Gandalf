# Observability

## Logging

- Structured logs only. No freeform string-only lines in production.
- Every warning and error carries a correlation or trace identifier. A log line you
  cannot join to a request is a log line you cannot use.
- Log at boundaries, not deep inside domain logic.
- Use consistent field names across services: service, environment, trace identifier,
  span identifier, error, duration.
- Put the one or two identifiers needed to follow a live tail in the message text as
  well as in structured fields. Some viewers show only the message. Keep personal data,
  secrets, and high-cardinality values out of it.

## Tracing

- Instrument at the entry point, before anything else initialises.
- Propagate trace context through every call — outbound HTTP, database queries,
  external services all produce spans.
- A trace that stops at the service boundary answers the easy questions only.

## Metrics

Every service exposes, at minimum: request rate, error rate, latency distribution
(median and tail), and whatever indicator actually reflects whether the service is
doing its job. The last one is the one that gets skipped and the one that matters.

## Choosing Tools

Pick one tracing and metrics stack and use it everywhere. The specific vendor matters
far less than every service reporting the same field names into the same place —
consistency is what makes a dashboard possible.
