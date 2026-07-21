# ADR 0011 — Docs, examples, and README refresh

- **Epic**: E9
- **Status**: Proposed
- **Depends on**: 0010
- **Blocks**: none

## Context

The current `README.md` is 437 bytes. It describes intent, not usage.
By the end of E8, the public API is stable and testable. Now docs can
describe reality.

Docs written before the API stabilizes are lies. That is why this epic
runs last, not first.

## Decision

- Rewrite `README.md` with this structure:
  1. **What it is** — one paragraph.
  2. **When to use / when not to use** — bullet lists.
  3. **Install** — `go get` line and prerequisite versions.
  4. **Quickstart** — a 20-line Go snippet plus a `mise install`
     command.
  5. **Concepts** — glossary summary, links to `CONTEXT.md`.
  6. **Comparison to pg_partman** — table of "we have / we don't".
  7. **Non-goals** — copy from `CONTEXT.md`.
  8. **Reading order** — pointer to `docs/adr/`.
- Author two runnable programs under `examples/`:
  - `examples/basic/main.go` — register one parent by date, run
    `Maintain` a few times against a simulated clock, drop old
    partitions.
  - `examples/multitenant/main.go` — register a parent with a
    `tenant_column`, register two tenants, run `Maintain` a few
    times.
  - Each example has its own `README.md` with a "how to run" section
    and expected output.
- Update `CONTEXT.md` with any new terms invented during E1a to E8.
- Author `CHANGELOG.md` for `v0.1.0`. Follow keep-a-changelog format
  loosely: Added / Changed / Deprecated / Removed / Fixed / Security.
- Author `docs/architecture.md` with a system diagram (ASCII) that
  shows `Manager` composing `Registry`, `Provisioner`, `Retention`,
  `Maintainer` over `*pgxpool.Pool` and `Clock`. Cross-link every
  ADR.
- Update the ADR index (`docs/adr/README.md`): flip every epic's
  status to `Implemented` and cross-link the merge commit.

## Consequences

- `v0.1.0` becomes a release-tag candidate.
- The public API is documented against real code.
- New contributors have a linear reading order: `README.md` ->
  `CONTEXT.md` -> `docs/architecture.md` -> `docs/adr/`.

## Deliverables

- `README.md` — rewritten.
- `examples/basic/main.go` and `examples/basic/README.md`.
- `examples/multitenant/main.go` and `examples/multitenant/README.md`.
- `CHANGELOG.md`.
- `docs/architecture.md`.
- Updated `CONTEXT.md`.
- Updated `docs/adr/README.md` (status flips).

## Acceptance

- `go run ./examples/basic` runs against a fresh PostgreSQL. It
  creates the parent, provisions partitions, then drops the oldest
  as the simulated clock advances.
- `go run ./examples/multitenant` runs against a fresh PostgreSQL.
  It creates two tenant-specific partition sets.
- `README.md` renders under `go doc`.
- `CONTEXT.md` glossary covers every exported name in the root
  package.
- Every ADR file in `docs/adr/` links back to `CONTEXT.md`.
- `docs/architecture.md` contains an ASCII diagram of the composed
  types.

## Open questions

- Do we tag `v0.1.0` at the end of this epic? **Decision: yes.**
- Do we publish to `pkg.go.dev`? **Decision: automatic on tag push.**
  No extra work.
- Do we set up `.github/DISCUSSIONS.md`? **Decision: no for v1.**
  Issues are enough for a young project.

## References

- `CONTEXT.md` — the glossary. Every doc references it.
- ADR-0000 — the ADR conventions this epic follows.
- ADR-0001 through ADR-0010 — the epics this epic documents.
