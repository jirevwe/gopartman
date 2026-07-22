# Architecture

`go_partman` follows a facade + internal-package layout. The root
package `go_partman` exports one facade type (`Manager`) and a set of
small helpers. All behavior lives under `internal/`, one interface per
concern. `Manager` composes those concerns; callers never import
`internal/` directly.

The public API is documented in ADR-0001 and the composition is defined
in `manager.go`.

## Composition

```
                    +----------------------------------+
                    |            Manager               |
                    |  (root package: go_partman)      |
                    +----------------------------------+
                         |         |         |
        +----------------+----+----+----+----+----------------+
        |             |       |       |     |                 |
        v             v       v       v     v                 v
   +----------+  +--------+  +---+  +---+  +----------+  +--------+
   | Registry |  | Provi- |  | R |  | M |  | Importer |  | Drain  |
   |          |  | sioner |  | e |  | a |  |          |  |        |
   | parents  |  |        |  | t |  | i |  | reconcile|  | move   |
   | tenants  |  | create |  | e |  | n |  | PG->meta |  | rows   |
   | lifecycle|  | children|  | n |  | t |  |          |  | out of |
   |          |  |        |  | t |  | a |  |          |  | default|
   |          |  |        |  | i |  | i |  |          |  |        |
   |          |  |        |  | o |  | n |  |          |  |        |
   |          |  |        |  | n |  | e |  |          |  |        |
   |          |  |        |  |   |  | r |  |          |  |        |
   +----------+  +--------+  +---+  +---+  +----------+  +--------+
        \____________|________|______|__________|_____________/
                              |
                              v
              +------------------------------+
              | Shared dependencies          |
              |------------------------------|
              |  *pgxpool.Pool  (DB access)  |
              |  Clock          (time)       |
              |  Meter          (metrics)    |
              |  *slog.Logger   (logs)       |
              |  Hook           (drop policy)|
              +------------------------------+
```

The field list on `Manager` (see `manager.go:25-39`) is the source of
truth for this diagram.

## Responsibilities

- **Registry** (`internal/registry`, ADR-0005) — writes and reads
  rows in `partman.parent_tables` and `partman.tenants`. Delegates
  physical partition creation to Provisioner, and physical drops to
  Retention when `WithCascadeDrop` is requested.
- **Provisioner** (`internal/provisioner`, ADR-0004) — creates the
  current partition, `Premake` future partitions, and the default
  partition. Idempotent under repeat calls.
- **Retention** (`internal/retention`, ADR-0006) — sweeps expired
  partitions and applies the `Hook` decision (drop / detach /
  archive / skip). Marks metadata rows `status='dropped'` after
  physical DDL succeeds. Default partitions are never dropped.
- **Maintainer** (`internal/maintainer`, ADR-0007) — the scheduler.
  On each tick (or on demand via `Manage.Maintain`), iterates
  registered parents, holds a per-parent PostgreSQL advisory lock,
  calls Provisioner then Retention, releases the lock, moves on.
- **Importer** (`internal/importer`, ADR-0008) — inspects
  `pg_inherits` and `pg_get_expr(relpartbound)` for a parent,
  inserts missing metadata rows, and reports drifted / orphaned /
  skipped children. Never rewrites PG state.
- **Drain** (`internal/drain`, ADR-0009) — batches
  `INSERT INTO ... SELECT ... FROM {default} WHERE ... RETURNING`
  loops that move rows out of the default partition into their
  bounded targets. Records `DrainAnomaly` for rows whose target
  partition is missing.

## Cross-cutting concerns

- **`*pgxpool.Pool`** — every internal package accepts the pool in
  its `Config`. Nothing else opens connections.
- **`Clock`** — every read of "now" flows through `Clock.Now()`.
  Tests inject `SimulatedClock`.
- **`Meter`** — every internal package accepts `Meter` and calls
  `Counter` / `Histogram` per ADR-0010. Never includes `tenant_id`
  in tags.
- **`*slog.Logger`** — structured logs; every package accepts a
  logger and never calls `log.*` directly.
- **`Hook`** — global (not per-parent). Retention passes each
  candidate; the hook filters by inspecting `PartitionRef`.

## Related ADRs

Read the ADRs in order for the design rationale behind each piece.

- [ADR-0000](adr/0000-domain-language-and-adr-conventions.md) — ADR
  conventions and ASD-STE100 prose rules.
- [ADR-0001](adr/0001-public-api-skeleton.md) — public API skeleton
  and package layout.
- [ADR-0002](adr/0002-metadata-schema-extension.md) — metadata
  schema shape.
- [ADR-0003](adr/0003-integration-test-harness.md) — testcontainers
  harness.
- [ADR-0004](adr/0004-provisioner-create-partitions.md) —
  provisioner semantics.
- [ADR-0005](adr/0005-registry-lifecycle.md) — registry lifecycle.
- [ADR-0006](adr/0006-retention-drop-or-detach.md) — retention with
  pre-drop hook.
- [ADR-0007](adr/0007-maintainer-scheduler-advisory-lock.md) —
  maintainer scheduler and advisory lock.
- [ADR-0008](adr/0008-import-and-reconcile.md) — import and
  reconcile.
- [ADR-0009](adr/0009-partition-data-drain.md) — `partition_data`
  drain.
- [ADR-0010](adr/0010-observability-errors-retry.md) —
  observability, typed errors, retry policy.
- [ADR-0011](adr/0011-docs-examples-readme.md) — this docs epic.

See also [`/CONTEXT.md`](../CONTEXT.md) for the glossary that every
ADR cites.
