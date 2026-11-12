package retention

import (
	"fmt"

	"github.com/jackc/pgx/v5"
)

// buildDropDDL returns a DROP TABLE ... CASCADE statement for a
// child partition. Postgres DDL does not support $1 parameter
// binding, so the identifier is sanitized via pgx.Identifier.Sanitize().
func buildDropDDL(childSchema, childTable string) string {
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf("DROP TABLE %s CASCADE", childFQ)
}

// buildDropIfExistsDDL returns a DROP TABLE IF EXISTS ... CASCADE
// statement. Used by DropAll for idempotency: a second RemoveParent
// call must not fail because the table is already gone.
func buildDropIfExistsDDL(childSchema, childTable string) string {
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", childFQ)
}

// buildDetachDDL returns an ALTER TABLE <parent> DETACH PARTITION
// <child> statement. Both identifiers are sanitized.
func buildDetachDDL(parentSchema, parentTable, childSchema, childTable string) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf("ALTER TABLE %s DETACH PARTITION %s", parentFQ, childFQ)
}

// buildDetachConcurrentlyDDL returns DETACH PARTITION CONCURRENTLY.
// Must not run inside a transaction (ADR-0013).
func buildDetachConcurrentlyDDL(parentSchema, parentTable, childSchema, childTable string) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf("ALTER TABLE %s DETACH PARTITION %s CONCURRENTLY", parentFQ, childFQ)
}

// buildAttachDefaultDDL returns ATTACH PARTITION … DEFAULT for the
// parent's default child. Used to restore the DEFAULT after a brief
// plain DETACH so DETACH CONCURRENTLY can run on dated children.
func buildAttachDefaultDDL(parentSchema, parentTable, childSchema, childTable string) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf("ALTER TABLE %s ATTACH PARTITION %s DEFAULT", parentFQ, childFQ)
}

// buildSetSchemaDDL returns an ALTER TABLE <fq> SET SCHEMA <target>
// statement. The target schema is sanitized.
func buildSetSchemaDDL(childSchema, childTable, targetSchema string) string {
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	targetIdent := pgx.Identifier{targetSchema}.Sanitize()
	return fmt.Sprintf("ALTER TABLE %s SET SCHEMA %s", childFQ, targetIdent)
}
