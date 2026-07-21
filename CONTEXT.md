# CONTEXT — `go_partman` domain glossary

> This file is the single source of truth for terms used across the
> `go_partman` codebase and its ADRs. If a new term is invented in a PR,
> add it here in the same PR. All prose uses ASD-STE100 (Simplified
> Technical English): short sentences, active voice, one idea per
> sentence.

## Scope

`go_partman` is a Go library. It manages PostgreSQL partitioned tables.
It has one bounded context. All terms below belong to that context.

## Glossary

### Parent

An ordinary PostgreSQL partitioned table. A parent has `PARTITION BY
RANGE (...)` on either a time column or a `(tenant, time)` composite.
The user creates the parent. `go_partman` does not create it.

- Registered with: `Registry.RegisterParent`.
- Not to be confused with: **Partition** (a child of the parent).

### Tenant

A value that groups rows of a parent. The library treats a tenant as an
opaque string. A tenant is required only when the parent has a
`TenantColumn`.

- Registered with: `Registry.RegisterTenant`.
- Not to be confused with: **Parent** (the tenant belongs to one
  parent).

### Partition

One child table of a parent. A partition has bounds. A partition can
also be the default partition.

- Not to be confused with: **Parent** (the parent has many partitions).

### Default Partition

The child table with `DEFAULT` bounds. It catches rows that no bounded
partition accepts. Every parent has exactly one default partition. The
library creates the default partition when the user registers the
parent.

- Not to be confused with: **Partition** (a bounded partition holds
  rows in a known range; the default holds the rest).

### Bounds

A half-open time range `[From, To)`. `From` is included. `To` is
excluded. Bounds match PostgreSQL range semantics. A day partition
runs `[00:00 UTC, next 00:00 UTC)`.

- Not to be confused with: closed `[From, To]` intervals. The library
  never uses closed intervals.

### Premake

The number of future partitions to keep ahead of the current period.
The default is 4. The value is stored on the parent row in
`partman.parent_tables.premake`.

- Not to be confused with: **Retention Window** (premake is ahead;
  retention is behind).

### Retention Window

The interval before which a partition is a drop candidate. The value
is stored on the parent row in `partman.parent_tables.retention_period`
as a PostgreSQL `INTERVAL`.

- Not to be confused with: **Premake** (premake is ahead; retention is
  behind).

### Detach

`ALTER TABLE ... DETACH PARTITION`. The child table becomes an
ordinary table. The rows stay. The table stays in the same schema
unless retention moves it.

- Not to be confused with: **Drop** (drop removes the table; detach
  keeps it).

### Drop

`DROP TABLE ... CASCADE`. The child table and its rows are gone. The
metadata row moves to `status = 'dropped'`.

- Not to be confused with: **Detach** (drop removes the table; detach
  keeps it).

### Archive Schema

A PostgreSQL schema where detached tables move for cold storage. The
schema is set per parent in
`partman.parent_tables.retention_schema`. The library does not create
the schema. The user must create it.

- Not to be confused with: **`partman` schema** (which holds metadata,
  not user data).

### Pre-Drop Hook

A Go function that runs before Retention drops, detaches, or archives
a partition. The hook returns `HookDrop`, `HookDetach`, `HookArchive`,
or `HookSkip`. The hook is global, not per-parent. The hook filters by
inspecting the `PartitionRef` it receives.

- Not to be confused with: **partition_data drain** (drain moves rows;
  the hook decides fate).

### Maintenance Run

One iteration of the maintenance loop. In one run, the Maintainer
iterates parents, holds the advisory lock per parent, calls Provisioner
and Retention, and releases the lock.

- Not to be confused with: **tick** (a tick is the ticker event; a
  maintenance run is the work triggered by the tick).

### Advisory Lock Key

The two-int32 pair used with `pg_try_advisory_lock`. The pair is
`(hashtext(schema), hashtext(table))`. The lock scope is per parent.

- Not to be confused with: **PostgreSQL row locks** (advisory locks are
  application-level; the library does not use row locks for
  maintenance).

### Reconcile

Bring metadata and PostgreSQL state in line. One operation reconciles
in one direction only.

- Provisioner: metadata drives PG. It creates missing tables.
- Importer: PG drives metadata. It inserts missing rows.
- Not to be confused with: **drift** (drift is the discrepancy;
  reconcile is the action).

### Anomaly

A condition that the library detects but cannot fix. Examples: a row
in the default partition that no bounded partition accepts; a metadata
row without a matching PG table (orphan).

- Not to be confused with: **error** (an error stops the operation; an
  anomaly is recorded and the operation continues).

### `partman` schema

The PostgreSQL schema that holds `go_partman` metadata: `parent_tables`,
`tenants`, `partitions`. The library never puts user data here.

- Not to be confused with: **user schema** (where user tables live).

### `partition_data` drain

A batch operation that moves rows out of the default partition and
into the correct bounded child partitions. It runs one transaction per
batch. It is safe to interrupt.

- Not to be confused with: **Retention Sweep** (drain moves rows;
  retention removes tables).

## Non-goals for v1

These terms appear in pg_partman but NOT in `go_partman` v1:

- **List partitioning**, **Hash partitioning**
- **Sub-partitioning**, **Template Table**
- **Epoch** (integer-time columns)
- **Constraint Cols**, **Optimize Constraint**
- **undo_partition** (merge child tables back into one)
- **jobmon** (pg_jobmon integration)

If a future PR needs any of these, open an ADR first.
