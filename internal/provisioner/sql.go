package provisioner

import (
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jirevwe/gopartman/internal/naming"
)

// buildBoundedPartitionDDL returns a CREATE TABLE ... PARTITION OF ...
// FOR VALUES FROM (...) TO (...) statement for a bounded child
// partition. If tenant is non-empty, the FOR VALUES clause uses the
// composite (tenant, timestamp) form. Postgres DDL does not support
// $1 parameter binding, so identifiers go through pgx.Identifier.
// Sanitize() and literals go through pqQuoteLiteral / a fixed
// TIMESTAMPTZ literal.
func buildBoundedPartitionDDL(
	parentSchema, parentTable string,
	childSchema, childTable string,
	tenant string,
	b naming.Bounds,
) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()

	fromLit := tsLiteral(b.From)
	toLit := tsLiteral(b.To)

	var forValues string
	if tenant == "" {
		forValues = fmt.Sprintf("FROM (%s) TO (%s)", fromLit, toLit)
	} else {
		t := pqQuoteLiteral(tenant)
		forValues = fmt.Sprintf("FROM (%s, %s) TO (%s, %s)", t, fromLit, t, toLit)
	}

	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES %s",
		childFQ, parentFQ, forValues,
	)
}

// buildDefaultPartitionDDL returns a CREATE TABLE ... PARTITION OF ...
// DEFAULT statement for the default partition.
func buildDefaultPartitionDDL(
	parentSchema, parentTable string,
	childSchema, childTable string,
) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s DEFAULT",
		childFQ, parentFQ,
	)
}

// tsLiteral renders a Postgres TIMESTAMPTZ literal in UTC RFC3339 form.
// Not user input; safe from injection.
func tsLiteral(t time.Time) string {
	return "TIMESTAMPTZ '" + t.UTC().Format(time.RFC3339) + "'"
}

// pqQuoteLiteral quotes a string as a Postgres text literal, doubling
// any embedded single quotes. Tenant IDs are already validated to
// `^\w+$` by naming.TableName.Build, so this call escapes nothing in
// practice; the escape is defense in depth.
func pqQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
