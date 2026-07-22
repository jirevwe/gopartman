# examples/basic

Register one date-partitioned parent, drive `Maintain` against a
simulated clock, and watch partitions come and go as the clock crosses
the retention window.

## How to run

Set `DATABASE_URL` to a PostgreSQL 14+ instance where you have
`CREATE SCHEMA` rights. The example wipes and recreates a schema
named `partman_basic_demo` on every run.

```bash
DATABASE_URL=postgres://user:pass@localhost/pg_part go run ./examples/basic
```

## What it does

1. Applies the `partman` metadata migrations.
2. Drops and recreates `partman_basic_demo.events`, a table with
   `PARTITION BY RANGE (created_at)`.
3. Registers the parent with `PartitionInterval=day`, `Premake=2`,
   and `RetentionPeriod=3d`.
4. Advances a `SimulatedClock` by one day, three times. Each step
   calls `Maintain` and prints the partitions.
5. Jumps the clock 10 days forward and calls `Maintain` once more.
   Old partitions cross the retention window and are marked
   `status='dropped'`. New partitions are provisioned ahead.

## Expected output (elided)

```
== Registered parent
  clock=2026-03-15  partitions=4
    - partman_basic_demo.events_default [active] (default)
    - partman_basic_demo.events_20260315 [active]
    - partman_basic_demo.events_20260316 [active]
    - partman_basic_demo.events_20260317 [active]
== After +1d Maintain
  clock=2026-03-16  partitions=5
    ...
== After +10d jump
  clock=2026-03-28  partitions=10
    - partman_basic_demo.events_default [active] (default)
    - partman_basic_demo.events_20260315 [dropped]
    - partman_basic_demo.events_20260316 [dropped]
    - ...
    - partman_basic_demo.events_20260328 [active]
    - partman_basic_demo.events_20260329 [active]
    - partman_basic_demo.events_20260330 [active]
```

`Retention` marks the physical child table gone but keeps the
metadata row with `status='dropped'` for auditability.
