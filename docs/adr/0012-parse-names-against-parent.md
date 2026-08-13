# ADR 0012 — Parse partition names against their parent

- **Epic**: E6 (follow-up)
- **Status**: Proposed
- **Depends on**: 0001, 0008
- **Blocks**: none
- **Amends**: 0001, 0008

## Context

ADR-0008 step 3 calls `TableName.Parse` with the child name alone. Parse
must find the tenant segment inside that name. It has no parent name, so
it guesses. It reads the word before the suffix as a tenant if that word
matches `^[A-Z0-9]+$`.

The guess fails on two inputs. Both failures are silent. Parse returns no
error:

```
input                             parent                tenant   err
test.user_logs_tenant1_20240101   "user_logs_tenant1"   ""       nil
test.user_logs_TENANT_2_20240101  "user_logs_TENANT"    "2"      nil
test.user_logs_20240101           "user_logs"           ""       nil
```

Row 1 breaks adoption. PostgreSQL folds unquoted identifiers to lower
case. A partition created by external DDL carries a lower-case tenant.
Parse returns no tenant. `compareNameAndBound` then reports `tenant
mismatch: name="", bound="TENANT1"`. The importer records the child as
`Drifted`. It writes no metadata row. Retention manages nothing for that
child. The table grows without a limit. Retention still runs to the end
and reports success.

The comparison is not at fault. It already folds both sides to upper
case. Parse gives it no tenant to compare. This is why the report shows a
mismatch instead of a case difference.

Row 2 breaks the round trip. `Build` validates the tenant with `^\w+$`,
so an underscore is a legal tenant. Parse splits such a name at the wrong
underscore. It returns the wrong tenant and the wrong parent. ADR-0001
requires `Parse(Build(x)) == x`. That requirement fails today.

Row 3 shows why a wider character test is not the fix. Here `user_logs`
is a parent with two words, and the child has no tenant. A test that
accepts lower case reads `logs` as a tenant. `user_logs` is the fixture
in `table_test.go`, so the regression is immediate.

No property of the name separates a tenant segment from the last word of
a parent. In `public.order_items_20060102`, `items` can be either one.

## Decision

- Change the signature to `Parse(fqName, parentName string)`.
- Callers pass the parent that they reconcile. Every caller knows it.
- Compare the parent segment without case. PostgreSQL may have folded it.
- Return the tenant as written. Parse does not fold the tenant.
- Validate the tenant with `^\w+$`. This matches `Build`.
- Return an error if the name does not belong to the given parent.
- Return an error if the tenant segment is empty.
- The importer passes `ParentRef.TableName`.
- `refFromMetadata` no longer parses the stored name. It takes the schema
  and the parent from the caller. The row is already scoped to that
  parent, and the row holds its own `tenant_id`.
- Remove `isUpperAlphanumeric`. Nothing uses it after this change.

## Consequences

- Parse is deterministic. It does not guess.
- `ImportExisting` adopts children created by external DDL. Retention
  then manages them.
- Tenant identity still comes from the bound. `parseBoundExpr` folds the
  bound tenant to upper case, so metadata keeps one form for one tenant.
- The change breaks direct callers of `Parse`. `Build` does not change.
  The only callers in this repository are the importer and the tests.
- A tenant may hold an underscore. Parse and `Build` now agree on that.
- A child that belongs to another parent returns an error. The importer
  records it as `Skipped`, which ADR-0008 already defines.
- ADR-0001 and ADR-0008 keep their numbers. Each one gains a pointer to
  this ADR.

## Deliverables

- `internal/naming/naming.go` — the new signature. Deletes
  `isUpperAlphanumeric`.
- `internal/importer/importer.go` — passes `ref.TableName` to Parse.
  Simplifies `refFromMetadata`.
- `table_test.go` — Parse cases for a folded tenant, a folded parent, a
  foreign parent, and an empty tenant. One round-trip case for a tenant
  with an underscore.
- `internal/importer/integration_test.go` — one real-PG test for a folded
  child name.
- `CHANGELOG.md` — an `Unreleased` section for the breaking change and
  the fix.

## Acceptance

- Parse reads a lower-case tenant.
- Parse reads a tenant that holds an underscore.
- Parse ignores the case of the parent segment.
- Parse returns an error for a name that belongs to another parent.
- Parse returns an error for an empty tenant segment.
- `Parse(Build(x)) == x` for every case in `TestTableNameRoundTrip`,
  including a tenant with an underscore.
- A child of a two-word parent with no tenant still parses with no
  tenant.
- `ImportExisting` adopts a child named `<parent>_<tenant>_<YYYYMMDD>`
  when the tenant is lower case. The report shows `Imported = 1` and
  `Drifted = 0`.
- The metadata row for that child holds the upper-case tenant.
- The same test fails before this change with `Imported = 0`.

## Open questions

- Keep a one-argument form as a wrapper? **Decision: no.** Two forms
  invite the old guess back.
- Fold the tenant to upper case inside Parse? **Decision: no.** Parse
  reports what PostgreSQL stores. The importer folds where it compares.
- Rename children that already drifted? **Decision: no.** The library
  reports. The operator renames. A rename needs a lock on live tables,
  so the library must not do it during import.
- Amend ADR-0001 and ADR-0008 in place? **Decision: no.** ADR-0000
  forbids a rewrite. Add a pointer to this ADR in each one.

## References

- ADR-0000 — supersede or amend through a new ADR. Never renumber.
- ADR-0001 — `Parse` as the inverse of `Build`. This ADR changes the
  signature.
- ADR-0008 — import and reconcile. Step 3 now passes the parent name.
- `CONTEXT.md` — glossary (Parent, Tenant, Reconcile).
- PostgreSQL lexical structure — unquoted identifiers fold to lower
  case: https://www.postgresql.org/docs/current/sql-syntax-lexical.html
