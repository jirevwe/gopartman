# Architecture Decision Records

This directory holds one ADR per major decision in `go_partman`. Each
ADR is small enough that one fresh Claude session can turn it into an
actionable implementation plan.

## Format

Every ADR follows MADR-lite:

- **Context** — what problem this ADR solves.
- **Decision** — the stance we take.
- **Consequences** — what changes downstream.
- **Deliverables** — files touched, functions added.
- **Acceptance** — testable criteria.
- **Open questions** — items to resolve before code lands.

All prose uses ASD-STE100 (Simplified Technical English): short
sentences, active voice, one idea per sentence.

## File naming

`NNNN-{kebab-title}.md` where `NNNN` is a four-digit index. Never renumber
an ADR. To supersede an ADR, add a new one and cross-link.

## Index

| ID | Title | Epic | Status |
|---|---|---|---|
| 0000 | [Domain language and ADR conventions](0000-domain-language-and-adr-conventions.md) | E0 | Proposed |
| 0001 | [Public API skeleton and package layout](0001-public-api-skeleton.md) | E1a | Proposed |
| 0002 | [Metadata schema extension](0002-metadata-schema-extension.md) | E1b | Proposed |
| 0003 | [Integration test harness](0003-integration-test-harness.md) | E1c | Proposed |
| 0004 | [Provisioner — create partitions and default](0004-provisioner-create-partitions.md) | E2 | Proposed |
| 0005 | [Registry lifecycle](0005-registry-lifecycle.md) | E3 | Proposed |
| 0006 | [Retention drop-or-detach with Hook](0006-retention-drop-or-detach.md) | E4 | Proposed |
| 0007 | [Maintainer + scheduler + advisory lock](0007-maintainer-scheduler-advisory-lock.md) | E5 | Proposed |
| 0008 | [Import and reconcile existing partitions](0008-import-and-reconcile.md) | E6 | Proposed |
| 0009 | [`partition_data` helper — drain the default](0009-partition-data-drain.md) | E7 | Proposed |
| 0010 | [Observability, typed errors, retry policy](0010-observability-errors-retry.md) | E8 | Proposed |
| 0011 | [Docs, examples, and README refresh](0011-docs-examples-readme.md) | E9 | Proposed |

## Related files

- `/CONTEXT.md` — the glossary. Every ADR cites it.
- `/CLAUDE.md` — repo-wide guidance for Claude Code.
- `/docs/agents/domain.md` — agent instructions on how to consume this
  directory.

## Status values

- **Proposed** — written, awaiting user approval.
- **Accepted** — user has approved. Implementation may begin.
- **Implemented** — code lands. Cross-link the merge commit.
- **Superseded** — replaced by another ADR. Cross-link the successor.
