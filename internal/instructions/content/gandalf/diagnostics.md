# Diagnostics

Bugs, failures, incidents, and anything behaving in a way nobody expected.

## Evidence First

1. Pull the actual logs, telemetry, failing output, or resource state before forming
   competing hypotheses.
2. Run the cheapest check that distinguishes the leading explanations.
3. After two inconclusive probes, name the single observation that would settle the
   question, and go get it.
4. Do not reason in circles while direct evidence is sitting there unread.

Validate assumptions against code, current documentation, or a focused probe before
asking the user. When the evidence stays inconclusive, say what you assumed, what you
checked, and what is still unknown.

When a bug concerns stored data, query the data. Do not start from code-path
inference, generic process inspection, or a proposed rule while the records that
exhibit the failure are directly available.

## Diagnosis Before Mitigation

A mitigation built on an unconfirmed diagnosis is worse than no mitigation: it hides
the symptom that would have led to the cause, and leaves the bug in the tree behind a
workaround. Confirm what is actually happening first. Then fix it.

If a mitigation genuinely has to ship before the diagnosis is complete, say so
explicitly, and say what evidence is still missing.

## Verification Levels

Compilation, type checking, linting, and unit tests prove code assembles. They do not
verify credentials, network paths, access rules, health checks, deployment wiring, or
integration behaviour.

For infrastructure, deployment, and integration changes:

- Run the real path when authorised to.
- Verify with a health check, a live request, or direct observation of the resource.
- Say "compiles, not yet run" until runtime behaviour is observed.

Run diagnostic commands untruncated and preserve their exit status. A failure is
signal, not noise to be filtered out.

## Single Failures Are Not Patterns

One timeout, one refused connection, one name-resolution failure is evidence about one
request. Before diagnosing a systemic problem, check whether the condition currently
reproduces and whether it has happened more than once.

## Tests

When a test fails after a change:

- If the implementation regressed, fix the implementation.
- If behaviour intentionally changed, update the test and explain the contract change.
- If which of those is true is unclear, stop before touching the test and settle the
  intended behaviour first.
