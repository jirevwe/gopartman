# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This repository is a Go library. The name of the library is `gopartman`.
The library controls partitioned tables in PostgreSQL.
The library can partition a table by date.
The library can also partition a table by tenant ID.
Each parent table can have a `_default` partition.
The design is similar to pg_partman.
The library uses `pgx/v5` as the database driver.
The library uses `sqlc` to make Go code from SQL queries.
The library keeps its data in a schema with the name `partman`.

## Toolchain

The file `mise.toml` sets the versions of all tools.
Use `mise install` to install the tools.

The tools are:
- Go, version 1.25.4.
- golangci-lint, version 2.4.0.
- sqlc, latest version.
- gci, latest version.

The file `mise.toml` also sets the variable `DATABASE_URL`.
The default value is `postgres://rtukpe:postgres@localhost:5432/pg_part?sslmode=disable`.
The tool `sqlc` reads this variable at code generation time.
The file `sqlc.yaml` has the option `database.managed: true`.
This option tells `sqlc` to make a temporary database from `schema.sql`.

## Commands

Use these commands during development:

- To build the code, run: `go build ./...`
- To run all tests, run: `go test ./...`
- To run one test, run: `go test -run TestTableNameGen .`
- To run one sub-test, run: `go test -run 'TestTableNameGen/table_with_date' .`
- To lint the code, run: `golangci-lint run`
- To make the sqlc code again, run: `sqlc generate`

## Architecture

The root package is `gopartman`.
The root package has these files:

- `clock.go` gives a `Clock` interface. Use `NewRealClock()` in production code. Use `NewSimulatedClock(t)` in test code. The simulated clock has the methods `SetTime` and `AdvanceTime`. New code that reads time must accept a `Clock`. New code must not call `time.Now()` directly.

- `table.go` gives the type `TableName`. The method `TableName.Build()` makes the partition name. The format of the name is `{schema}.{parent}[_{TENANT}]_{YYYYMMDD|default}`. The method makes the tenant ID upper case. The method rejects hyphens and spaces. The method accepts only the characters that match the pattern `^\w+$`.

- `types.go` gives constants and helper types. The constants for the partition interval are `PartitionMonthInterval`, `PartitionWeekInterval`, `PartitionDayInterval`, and `PartitionHourInterval`. The constant for the date format is `DateNoHyphens = "20060102"`. The type `Bounds` has the fields `From` and `To`. The type `Tenant` has the fields `ParentName`, `ParentSchema`, and `TenantId`. Note: the functions `generatePartitionName` and `buildTableName` do the same work as `TableName.Build`. Use `TableName.Build` in new code.

- `table_test.go` has the unit tests for the partition name.

## Metadata schema

The file `schema.sql` has the definitions for three tables in the `partman` schema:

1. `partman.parent_tables` has one row for each parent table. The pair `(schema_name, table_name)` is unique.
2. `partman.tenants` has one row for each tenant of a parent table.
3. `partman.partitions` has one row for each partition. The row has the bounds, the tenant ID, and the parent table.

The foreign keys cascade from `parent_tables` to `tenants` to `partitions`.

## Writing Plans
- All plans must be written in ASD-STE100 format.
- Always use these skills: "I have ADHD" and "Grilling"
- Review the ADR before planning and approve it/lock it in before the plan is generated
- If any existing already implemented ADRs exist lock them in/accpect them retroactively  

## Generated code

The directory `internal/` has three sub-directories: `parents/`, `tenants/`, and `partitions/`.
Each sub-directory has these items:

- `queries.sql` has the SQL queries. Edit this file to change a query.
- `repo/` has the Go code from `sqlc`. Do not edit these files. To make the code again, run `sqlc generate`.

Each of the three sub-directories has its own `sqlc` package.
All three packages read the same file `schema.sql`.

## Important notes

- The tenant ID in a partition name is always upper case. This is true even when the input is lower case. The tests in `table_test.go` show this behavior.
- When you change a file in `internal/*/queries.sql`, run `sqlc generate`. Commit both the SQL file and the new Go file.
- The value of `DATABASE_URL` must be correct when you run `sqlc generate`.

## Agent skills

### Issue tracker

GitHub issues in `jirevwe/gopartman` (via `gh` CLI). External PRs are not a triage surface. See `docs/agents/issue-tracker.md`.

### Triage labels

Default names: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
