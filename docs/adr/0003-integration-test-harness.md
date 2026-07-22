# ADR 0003 — Integration test harness

- **Epic**: E1c
- **Status**: Implemented (commit `fb16ec1`)
- **Depends on**: 0002
- **Blocks**: 0004, 0005, 0006, 0007, 0008, 0009

## Context

`sqlc.yaml` runs in `database.managed: true` mode. That handles
codegen only. It does not handle test-time PostgreSQL. E2 is the first
epic that emits DDL against a real database. The harness must land
before E2.

The old library at `/Users/rtukpe/Documents/dev/gopartman/manager_test.go`
uses a real Postgres reached through `DATABASE_URL`. That works on the
author's laptop and breaks everywhere else. The new library needs a
harness that any contributor can run.

Without a shared harness, every downstream epic invents its own
"spin up PG" code. That wastes time and creates inconsistent test
patterns.

## Decision

- Use `testcontainers-go` with PostgreSQL 16. This matches the
  production target.
- One helper for the database:
  ```go
  func NewPG(t *testing.T) (*pgxpool.Pool, func())
  ```
  It starts a container (or reuses a package-level one), applies all
  migrations from `partman.Migrations()`, and hands back a pool. The
  cleanup function truncates the `partman` schema.
- One helper for the clock:
  ```go
  func NewSimulatedClock(t *testing.T) *SimulatedClock
  ```
  It wraps the root package's `SimulatedClock` with a `t.Cleanup` that
  logs the final time.
- Add build tag `//go:build integration` to every integration test
  file. `go test ./...` stays fast. `go test -tags=integration ./...`
  runs the full suite.
- Share one container per package via `TestMain`. Truncate the
  `partman` schema (and drop user schemas the test created) between
  tests. Do NOT restart the container per test.
- Live under `internal/testsupport/`. Not exported.
- Reuse patterns:
  - Every test uses `t.Context()` (Go 1.24+) for cancellation.
  - Every test uses `t.Cleanup` for teardown.
  - No test calls `time.Now()`. Every test uses `NewSimulatedClock`.

## Consequences

- Every downstream epic includes integration tests in its acceptance
  list. The harness makes that cheap.
- CI grows a second job that runs with `-tags=integration`.
- Local dev needs Docker. Contributors without Docker can still run
  unit tests.
- The truncate-between-tests pattern is fast (~50 ms) and reliable.

## Deliverables

- `internal/testsupport/pg.go` — `NewPG(t)`.
- `internal/testsupport/clock.go` — `NewSimulatedClock(t)`.
- `internal/testsupport/migrations_test.go` — the smoke test from
  ADR-0002 acceptance.
- `.github/workflows/test.yml` (or `.github/workflows/ci.yml`) — two
  jobs:
  - `test-unit`: `go test ./...`
  - `test-integration`: `go test -tags=integration ./...`
- `go.mod` — add `github.com/testcontainers/testcontainers-go` and its
  postgres module.

## Acceptance

- `go test -tags=integration ./internal/testsupport/...` passes with
  Docker.
- Time from `go test` invocation to the first test running is under 5
  seconds (container reuse enabled).
- Between tests, `partman.parent_tables`, `partman.tenants`, and
  `partman.partitions` are empty.
- CI job `test-integration` runs on every push to `main` and on every
  PR.
- No test file calls `time.Now()` (verified by lint or grep).

## Open questions

- Testcontainers or `pg_tmp`? **Decision: testcontainers.** More
  portable across dev machines.
- Container image — `postgres:16-alpine` or plain `postgres:16`?
  **Decision: `postgres:16-alpine`.** Faster pulls.
- Run integration tests on every push or merge queue only?
  **Decision: every push.** Failures block PRs.
- Do we cache the container across test binaries? **Decision: no for
  v1.** Ryuk handles cleanup.
- Testcontainers version — pin or floating? **Decision: pin in
  `go.mod`.** Prevents surprise breaks.

## References

- `CONTEXT.md` — glossary.
- ADR-0002 — migrations must be embedded via `partman.Migrations()`.
- pg_partman test approach — full Docker-based CI with `pgTAP`. We
  keep this idea but use Go tests, not `pgTAP`.
