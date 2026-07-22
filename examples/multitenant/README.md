# examples/multitenant

Register a parent with a tenant column, then register two tenants
(`ACME` and `GLOBEX`). Each tenant gets its own set of bounded
partitions. `Maintain` slides every tenant's premake window forward
together.

## How to run

Set `DATABASE_URL` to a PostgreSQL 14+ instance where you have
`CREATE SCHEMA` rights. The example wipes and recreates a schema
named `partman_mt_demo` on every run.

```bash
DATABASE_URL=postgres://user:pass@localhost/pg_part go run ./examples/multitenant
```

## What it does

1. Applies the `partman` metadata migrations.
2. Drops and recreates `partman_mt_demo.orders` with
   `PARTITION BY RANGE (tenant_id, created_at)`.
3. Registers the parent with `TenantColumn=tenant_id`,
   `PartitionInterval=day`, `Premake=2`, and `RetentionPeriod=7d`.
   No child partitions exist yet — a tenanted parent waits for its
   first `RegisterTenant` call.
4. Registers tenants `ACME` and `GLOBEX`. Each triggers provisioning
   of that tenant's current + premake partitions.
5. Advances a `SimulatedClock` by one day, twice. Each step calls
   `Maintain` and prints the partitions grouped by tenant.

## Expected output (elided)

```
== Registered parent (no tenants yet)
  clock=2026-03-15  buckets=0 (tenants + default)
== Registered 2 tenants:
    - ACME
    - GLOBEX
  clock=2026-03-15  buckets=3 (tenants + default)
  tenant=<none>
    - partman_mt_demo.orders_default [active] (default)
  tenant=ACME
    - partman_mt_demo.orders_ACME_20260315 [active]
    - partman_mt_demo.orders_ACME_20260316 [active]
    - partman_mt_demo.orders_ACME_20260317 [active]
  tenant=GLOBEX
    - partman_mt_demo.orders_GLOBEX_20260315 [active]
    - partman_mt_demo.orders_GLOBEX_20260316 [active]
    - partman_mt_demo.orders_GLOBEX_20260317 [active]
== After +1d Maintain
  ...
```

Partition names upper-case the tenant id. `TableName.Build` enforces
this — see `table.go` and `internal/naming/naming.go`.
