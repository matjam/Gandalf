# Database

## Access

- Use the repository pattern: one package per domain entity, owning all database
  interaction for it, exposing a clear API.
- Write SQL by hand. A lightweight helper for parameter binding is fine; an ORM that
  generates queries you cannot see is not.
- Define the repository interface where it is consumed and put the implementation in
  an adapter package.

The reasoning is not that ORMs are bad in general. It is that the query is the part
that will eventually need reading, tuning, and explaining to a database, and hiding it
behind a fluent API means you find out what it does in production.

## Migrations

- Every application with a database has a migration system. No exceptions.
- Migrations run before the application serves traffic.
- If a migration cannot be applied, the application refuses to start with a clear
  error. A half-migrated schema serving requests is worse than an outage.
- Migrations are versioned, sequential, and never edited after being applied. Fix a
  bad migration by writing another one.
- A schema change that cannot be applied without downtime is a multi-phase migration:
  add, backfill, switch, remove. Plan all four before starting the first.

## Queries

- Index for the queries you actually run, and check the plan rather than assuming.
- Bound every query that can grow: no unpaginated reads of a table that only gets
  bigger.
- Use transactions where invariants span statements, and keep them short.
