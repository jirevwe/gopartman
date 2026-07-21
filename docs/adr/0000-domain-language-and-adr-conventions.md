# ADR 0000 — Domain language and ADR conventions

- **Epic**: E0
- **Status**: Accepted
- **Depends on**: none
- **Blocks**: 0001, 0002

## Context

`docs/agents/domain.md` tells agents to read `CONTEXT.md` and
`docs/adr/`. Neither file exists in the repo today. Without a fixed
glossary, each downstream ADR invents its own vocabulary. Drift is
certain. Terms such as **detach vs drop**, **parent vs template**,
and **tenant vs partition key** each have several plausible readings.

The library is a fresh rewrite. This is the moment to fix the words
before code hardens around ambiguous ones. The user cited pg_partman as
the reference implementation. Pg_partman has its own vocabulary. Some
of it fits; some of it does not.

## Decision

- Adopt MADR-lite for every ADR: Context, Decision, Consequences,
  Deliverables, Acceptance, Open questions.
- Author `CONTEXT.md` at repo root with a fixed glossary. The glossary
  is the single source of truth.
- Author `docs/adr/0000-...` (this file) with the format rules.
- Author `docs/adr/README.md` as an index. Add one placeholder per
  future ADR.
- Freeze these terms with one-line definitions in `CONTEXT.md`:
  Parent, Tenant, Partition, Default Partition, Bounds, Premake,
  Retention Window, Detach, Drop, Archive Schema, Pre-Drop Hook,
  Maintenance Run, Advisory Lock Key, Reconcile, Anomaly, `partman`
  schema, `partition_data` drain.
- Freeze `Bounds` as half-open `[From, To)`. All downstream SQL uses
  `>= From` and `< To`.
- Freeze the ADR filename convention: `NNNN-{kebab-title}.md`. Never
  renumber. Supersede via a new ADR.
- Use ASD-STE100 in every ADR and in `CONTEXT.md`. Short sentences.
  Active voice. One idea per sentence.

## Consequences

- Every downstream ADR cites the glossary in its Context section.
- If an ADR needs a new term, the author adds the term to `CONTEXT.md`
  in the same PR.
- Half-open bounds match PostgreSQL range semantics. There is no
  off-by-one negotiation in downstream epics.
- The ADR index is the single onboarding surface for new contributors.

## Deliverables

- `CONTEXT.md` (repo root).
- `docs/adr/0000-domain-language-and-adr-conventions.md` (this file).
- `docs/adr/README.md` (index).

## Acceptance

- Every term in the Decision list appears in `CONTEXT.md` with a
  one-line definition and a "not to be confused with" note.
- `docs/adr/README.md` lists ADR-0000 and reserves slots for ADR-0001
  through ADR-0011.
- `grep -Ri "not to be confused with" CONTEXT.md` returns at least 15
  hits.
- No ADR file in `docs/adr/` fails ASD-STE100 rules (short sentences,
  active voice) on manual review.

## Open questions

- Full MADR or MADR-lite? **Decision: MADR-lite.** MADR's "Considered
  options" section is over-heavy for a small-team library.
- One `CONTEXT.md` or split contexts? **Decision: one file.** The
  library has a single bounded context.
- Do we vendor a linter for ASD-STE100? **Decision: no.** Manual review
  during PR.

## References

- `docs/agents/domain.md` — instructs agents to consult `CONTEXT.md`.
- pg_partman glossary — different scope; used as reference only.
