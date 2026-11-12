# ADR 0013 — Online partition DDL mode

- **Epic**: E4 / E2 follow-up
- **Status**: Accepted
- **Depends on**: 0001, 0004, 0006, 0007
- **Blocks**: none
- **Supersedes (when mode=online)**: create path of ADR-0004; apply path of ADR-0006

## Context

Classic Maintain create uses `CREATE TABLE … PARTITION OF`. Classic
drop uses `DROP TABLE` on an attached child. Both take parent
`ACCESS EXCLUSIVE`. Under concurrent ingest that lock queues writers
for seconds to minutes.

Operators need a mode that keeps ingest moving during Maintain. The
mode must stay opt-in. Classic behavior remains the default.

Postgres rules that shape the design:

- `DETACH PARTITION CONCURRENTLY` cannot run inside a transaction.
- `ATTACH PARTITION` still takes a brief parent exclusive lock.
- A matching `CHECK` constraint lets attach skip a full validation scan.

See glossary terms **Online DDL mode** and **Concurrent detach** in
`CONTEXT.md`.

## Decision

- Add `PartitionDDLModeClassic` (default) and `PartitionDDLModeOnline`.
- Add `WithPartitionDDLMode(mode)` on `Manager`.
- Thread the mode into Provisioner and Retention at construction.

### Online provision

For each missing bounded child:

1. `CREATE TABLE … (LIKE parent INCLUDING DEFAULTS)` (no parent lock).
2. Add a bounds `CHECK` that matches the partition range (and tenant
   when present).
3. `SET lock_timeout` (3 seconds).
4. `ALTER TABLE … ATTACH PARTITION … FOR VALUES …`.
5. Upsert `partman.partitions` metadata after a successful attach.
6. Retry on lock timeout.

Do not wrap all creates in one transaction. Create one child at a
time. Default partition create may keep the classic
`CREATE … PARTITION OF … DEFAULT` path (rare).

### Online retention

Postgres rejects `DETACH PARTITION CONCURRENTLY` while a DEFAULT
partition is attached (`SQLSTATE 55000`). Every gopartman parent keeps
a DEFAULT, so online Sweep must:

1. Plain-`DETACH` the DEFAULT once per Sweep when it is attached (brief
   exclusive lock). If a prior Crash left the DEFAULT detached, skip
   this step and still plan to re-attach at the end.
2. For each expired candidate (`HookDrop` / `HookDetach` /
   `HookArchive`): `DETACH … CONCURRENTLY` when still attached, then
   drop or `SET SCHEMA` as today, then mark metadata.
3. Re-`ATTACH … DEFAULT` the default child.

Mid-failure must be safe on the next Sweep: already-detached dated
children resume drop/mark; a detached DEFAULT is re-attached at the
end of the next online Sweep.

### Classic mode

ADR-0004 and ADR-0006 paths stay unchanged when the mode is classic.

## Consequences

- Online drop breaks “DDL and metadata in one transaction”. Catalog
  changes and metadata updates are two-phase. Document that in code
  comments.
- Online requires PostgreSQL 14 or newer (`DETACH … CONCURRENTLY`).
- Online does not claim zero exclusive lock. Attach is still brief
  exclusive. Drop avoids long exclusive windows on the parent.
- Callers enable the mode explicitly. Default stays classic.

## Deliverables

- `docs/adr/0013-online-partition-ddl-mode.md` (this file).
- Glossary updates in `CONTEXT.md`.
- Index row in `docs/adr/README.md`.
- `WithPartitionDDLMode` in `options.go` / `types`.
- Online branches in `internal/provisioner` and `internal/retention`.
- Unit and integration tests for both modes.

## Acceptance

- Classic remains the default. Existing classic tests pass without a
  mode option.
- Online drop under concurrent inserts does not queue those inserts
  behind a multi-second parent `ACCESS EXCLUSIVE` held by `DROP` of an
  attached child.
- Online create under a parent `ACCESS SHARE` holder completes without
  the long wait seen for contended `CREATE … PARTITION OF`.
- A crash between concurrent detach and metadata mark leaves the next
  Sweep able to finish without erroring forever.
- Online drop with an attached DEFAULT succeeds (no `55000` skip):
  dated children drop, DEFAULT is re-attached. Covers tenant-dated
  children, `HookDetach` / `HookArchive`, and resume after DEFAULT was
  left detached.

## Open questions

- Should default-partition create also use prepare+attach?
  **Decision: no for this change.** Keep classic default create.
- Admin UI toggle in consumers?
  **Decision: out of scope.** Consumers pass the Manager option or an
  env flag.

## References

- ADR-0004 — classic provisioner create path.
- ADR-0006 — classic retention apply path.
- ADR-0000 — supersede via a new ADR; do not renumber.
- PostgreSQL docs — `DETACH PARTITION CONCURRENTLY`, `ATTACH PARTITION`.
