// Package go_partman manages range partitions in PostgreSQL. It
// provisions upcoming partitions, drops expired ones, imports
// pre-existing children, and drains rows out of the default partition.
// A parent table may be partitioned by date, or by (tenant, date) when
// a tenant column is declared. Metadata lives in a schema named
// "partman".
//
// Manager is the facade. Construct it with New(WithDB(pool),
// WithClock(clock), ...). Register parents with RegisterParent, then
// call Maintain in a loop (or Start to run the built-in scheduler).
//
// See the README, CONTEXT.md, docs/architecture.md, and the runnable
// programs under examples/ for a full tour.
package go_partman
