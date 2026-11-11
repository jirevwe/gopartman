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

// buildStandaloneLikeDDL creates an empty table matching the parent's
// column defaults without attaching it (ADR-0013 online create).
func buildStandaloneLikeDDL(parentSchema, parentTable, childSchema, childTable string) string {
	parentFQ := pgx.Identifier{parentSchema, parentTable}.Sanitize()
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (LIKE %s INCLUDING DEFAULTS)",
		childFQ, parentFQ,
	)
}

// buildBoundsCheckDDL adds a CHECK that matches the RANGE bounds so
// ATTACH can skip a full validation scan.
func buildBoundsCheckDDL(childSchema, childTable, timeColumn, tenantColumn, tenant string, b naming.Bounds) string {
	childFQ := pgx.Identifier{childSchema, childTable}.Sanitize()
	timeIdent := pgx.Identifier{timeColumn}.Sanitize()
	fromLit := tsLiteral(b.From)
	toLit := tsLiteral(b.To)
	var checkExpr string
	if tenantColumn == "" || tenant == "" {
		checkExpr = fmt.Sprintf(
			"%s IS NOT NULL AND %s >= %s AND %s < %s",
			timeIdent, timeIdent, fromLit, timeIdent, toLit,
		)
	} else {
		tenantIdent := pgx.Identifier{tenantColumn}.Sanitize()
		checkExpr = fmt.Sprintf(
			"%s = %s AND %s IS NOT NULL AND %s >= %s AND %s < %s",
			tenantIdent, pqQuoteLiteral(tenant),
			timeIdent, timeIdent, fromLit, timeIdent, toLit,
		)
	}
	constraint := pgx.Identifier{childTable + "_partman_bounds"}.Sanitize()
	return fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)",
		childFQ, constraint, checkExpr,
	)
}

// buildAttachPartitionDDL attaches a prepared standalone child.
func buildAttachPartitionDDL(
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
		"ALTER TABLE %s ATTACH PARTITION %s FOR VALUES %s",
		parentFQ, childFQ, forValues,
	)
}

// timeColumnFromPartitionBy returns the time column from a parent
// partition_by string such as "created_at" or "project_id, created_at".
func timeColumnFromPartitionBy(partitionBy string) string {
	parts := strings.Split(partitionBy, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return "created_at"
	}
	return last
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
