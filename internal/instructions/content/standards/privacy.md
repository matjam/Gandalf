# Privacy

## Default Posture

Do not collect, store, or transmit personal or sensitive data. This is the starting
assumption, not a preference to be balanced against convenience.

The question is never "how do we handle this data safely" until "do we need this data
at all" has been answered yes, with a reason.

## Classification

Classify every field and store that holds user-related data, visibly in the code —
schema comments, type annotations, or a data catalogue. Never leave it to be inferred.

| Class | Definition | Examples |
|---|---|---|
| Personal | Identifies a person directly | Name, email, phone, address, government ID, IP address, device ID |
| Sensitive — financial | Financial facts about a person | Balances, income, transactions, credit data |
| Sensitive — health | Health or medical data | Diagnoses, prescriptions, insurance records |
| Sensitive — other | Reveals private characteristics or creates real risk if exposed | Location history, behavioural profiles, demographic data |
| Internal | Not personal, not for publication | Internal identifiers, system metadata |
| Public | Safe to expose | Aggregates, public reference data |

Financial and health data carry the highest regulatory exposure and need explicit
callouts at design time. When classification is unclear, use the more sensitive one
until someone establishes otherwise.

## Stop and Ask

Raise a privacy concern before writing code when a design:

- Collects or stores personal identifiers
- Stores behavioural, financial, health, or demographic data
- Logs anything traceable to a specific person
- Sends user data to a third party
- Adds a field holding user-supplied data
- Adds analytics, tracking, or telemetry about people
- Copies user data between services or environments

Say what data is involved, why avoiding it is preferable, and what handling it
properly would require. Do not proceed until necessity is confirmed.

## When It Is Justified

**Minimise.** Collect the least that satisfies the requirement. If an anonymised or
aggregated value works, use it. Future usefulness does not justify present collection.

**Encrypt.** In transit and at rest, without exception.

**Scope access.** No service or role reads personal data it does not need. Grant
explicitly; never inherit from a broad role.

**Define retention.** A concrete duration and a mechanism that enforces it. Indefinite
retention is a decision someone should have to make on purpose.

**Never log it.** Audit every log statement near personal data, including error paths.
Watch for object serialisation in log calls, exception messages carrying user input,
request and response logging that includes headers or bodies, and query logging that
includes parameters.

**Synthetic test data.** Never copy production user data into a non-production
environment, even briefly.

**Support erasure.** Build the deletion or anonymisation path before shipping, not
after someone asks for it. Prefer anonymising over hard deletion where referential
integrity or audit trails matter. Include a dry-run mode; you will want to test the
path without destroying anything.

Specific obligations — lawful basis, consent categories, response deadlines, breach
notification — depend on jurisdiction and are not something to infer. When a design
touches them, find out what actually applies.

## Review Checklist

1. Classification is explicit on every field and store.
2. Necessity is justified and the data is the minimum required.
3. No code path can log personal data, including error paths.
4. Responses and event payloads expose no field that should not leave the service.
5. Third-party data flows were flagged and approved.
6. Retention is defined and enforced, not just documented.
7. Access is scoped to function.
8. An erasure path exists and has been exercised.
