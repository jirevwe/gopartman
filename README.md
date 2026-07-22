# go_partman

A Go library that manages range partitions in PostgreSQL. The library
provisions upcoming partitions, drops expired ones, imports pre-existing
ones, and drains rows out of the default partition. A parent table can
be partitioned by date, or by `(tenant, date)` when a tenant column is
declared. The library holds its metadata in a schema called `partman`.

## When to use it

- Your PostgreSQL table has (or will have) a `PARTITION BY RANGE` clause
  on a time column.
- You need daily, weekly, monthly, or hourly partitions.
- You want to keep rows for a fixed retention window and drop or detach
  older partitions on a schedule.
- You partition one dataset per tenant and want each tenant's rows in
  its own child table.

## When not to use it

The following features are **non-goals for v1** (see `CONTEXT.md`):

- List partitioning or hash partitioning.
- Sub-partitioning or template tables.
- Epoch (integer-time) partition columns.
- Constraint columns / constraint exclusion tuning.
- `undo_partition` (merging child tables back into one).
- `pg_jobmon` integration.

If you need any of these, `pg_partman` remains the more feature-rich
option.

## Install

```bash
go get github.com/jirevwe/go_partman
```

Prerequisites:

- Go 1.25.4 or later.
- PostgreSQL 14 or later (native declarative partitioning + `DEFAULT`
  partition support).

Contributors:

```bash
mise install   # installs Go, golangci-lint, sqlc, gci
```

## Quickstart

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    partman "github.com/jirevwe/go_partman"
)

func main() {
    ctx := context.Background()
    pool, err := pgxpool.New(ctx, "postgres://user:pass@localhost/db")
    if err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    // Apply the partman metadata migrations once at startup.
    for _, m := range partman.Migrations() {
        if _, err := pool.Exec(ctx, m.SQL); err != nil {
            log.Fatal(err)
        }
    }

    mgr, err := partman.New(
        partman.WithDB(pool),
        partman.WithClock(partman.NewRealClock()),
    )
    if err != nil {
        log.Fatal(err)
    }

    // The user creates the parent table itself:
    //   CREATE TABLE public.events (id BIGSERIAL, created_at TIMESTAMPTZ NOT NULL)
    //       PARTITION BY RANGE (created_at);
    if err := mgr.RegisterParent(ctx, partman.ParentConfig{
        SchemaName:        "public",
        TableName:         "events",
        PartitionBy:       "created_at",
        PartitionInterval: partman.PartitionDayInterval,
        Premake:           7,
        RetentionPeriod:   30 * 24 * time.Hour,
    }); err != nil {
        log.Fatal(err)
    }

    if err := mgr.Maintain(ctx); err != nil {
        log.Fatal(err)
    }
}
```

Two runnable programs live under `examples/`:

- `examples/basic` — one date-partitioned parent, driven by a simulated
  clock so you can watch partitions get created and dropped.
- `examples/multitenant` — a parent with a `tenant_column`, two
  tenants, each with its own partition set.

Both examples read `DATABASE_URL` from the environment.

## Concepts

- **Parent** — an ordinary PostgreSQL partitioned table. You create it;
  the library manages its children.
- **Partition** — one child table of a parent. Bounded partitions have
  `[From, To)` ranges. The default partition holds anything that no
  bounded partition accepts.
- **Tenant** — an opaque string that groups rows within one parent.
  Only used when the parent has a `TenantColumn`.
- **Premake** — the number of future partitions to keep ahead of the
  current period.
- **Retention Window** — the interval before which a partition is a
  drop candidate.
- **Pre-Drop Hook** — a Go function that decides whether a candidate
  partition is dropped, detached, archived, or skipped.
- **Maintenance Run** — one iteration of the maintenance loop. It
  provisions upcoming partitions and sweeps expired ones under an
  advisory lock, per parent.

See `CONTEXT.md` for the full glossary.

## Comparison to `pg_partman`

| We have | We do not have (see non-goals) |
| --- | --- |
| Range partitioning on a time column | List partitioning |
| Optional tenant axis via `TenantColumn` | Hash partitioning |
| Default partition auto-creation | Sub-partitioning / template tables |
| Premake and retention-window sweep | Epoch (integer-time) columns |
| Pre-drop hook for drop / detach / archive | Constraint-cols / optimize-constraint |
| Import & reconcile of pre-existing partitions | `undo_partition` |
| `partition_data` drain out of the default | `pg_jobmon` integration |
| Advisory-locked maintenance loop | Background worker / bgw scheduling |
| Observability via a `Meter` interface | Built-in dashboards |

## Non-goals for v1

Copied from `CONTEXT.md`:

- List partitioning, hash partitioning.
- Sub-partitioning, template tables.
- Epoch (integer-time) columns.
- Constraint cols, optimize constraint.
- `undo_partition` (merge child tables back into one).
- `jobmon` (`pg_jobmon` integration).

If a future PR needs any of these, open an ADR first.

## Reading order

New contributors should read in this order:

1. This `README.md` — what the library does.
2. `CONTEXT.md` — the glossary; single source of truth for terms.
3. `docs/architecture.md` — how `Manager` composes the internal
   packages.
4. `docs/adr/` — one ADR per major decision, in numerical order.
