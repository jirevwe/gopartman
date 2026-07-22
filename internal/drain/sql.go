package drain

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// readBatchQuery holds the SQL and positional args for one batch read.
type readBatchQuery struct {
	SQL  string
	Args []any
}

// readParams describes one read-batch invocation.
type readParams struct {
	Schema       string
	DefaultTable string
	ControlCol   string
	TenantCol    string  // empty when the parent has no tenant column
	Tenant       *string // WithTenant filter, if set
	AnomalyKeys  []groupKey
	BatchSize    int
}

// buildReadBatch renders the SELECT that reads one batch of rows from
// the default partition. It excludes rows whose bounds already appear
// in AnomalyKeys so the drain loop terminates instead of re-reading
// them. Names go through pgx.Identifier for safe quoting.
func buildReadBatch(p readParams) readBatchQuery {
	schemaQ := pgx.Identifier{p.Schema}.Sanitize()
	tableQ := pgx.Identifier{p.DefaultTable}.Sanitize()
	ctrlQ := pgx.Identifier{p.ControlCol}.Sanitize()

	var sb strings.Builder
	args := make([]any, 0, 1+len(p.AnomalyKeys)*3)

	sb.WriteString("SELECT ctid, ")
	sb.WriteString(ctrlQ)
	if p.TenantCol != "" {
		sb.WriteString(", ")
		sb.WriteString(pgx.Identifier{p.TenantCol}.Sanitize())
	}
	sb.WriteString(" FROM ")
	sb.WriteString(schemaQ)
	sb.WriteString(".")
	sb.WriteString(tableQ)
	sb.WriteString(" WHERE ")
	sb.WriteString(ctrlQ)
	sb.WriteString(" IS NOT NULL")

	if p.TenantCol != "" && p.Tenant != nil {
		args = append(args, *p.Tenant)
		fmt.Fprintf(&sb, " AND %s = $%d", pgx.Identifier{p.TenantCol}.Sanitize(), len(args))
	}

	for _, k := range p.AnomalyKeys {
		args = append(args, k.Bounds.From, k.Bounds.To)
		sb.WriteString(" AND NOT (")
		fmt.Fprintf(&sb, "%s >= $%d AND %s < $%d", ctrlQ, len(args)-1, ctrlQ, len(args))
		if p.TenantCol != "" {
			args = append(args, sqlTenantArg(k))
			fmt.Fprintf(&sb, " AND %s IS NOT DISTINCT FROM $%d", pgx.Identifier{p.TenantCol}.Sanitize(), len(args))
		}
		sb.WriteString(")")
	}

	sb.WriteString(" ORDER BY ")
	sb.WriteString(ctrlQ)
	args = append(args, p.BatchSize)
	fmt.Fprintf(&sb, " LIMIT $%d FOR UPDATE SKIP LOCKED", len(args))
	return readBatchQuery{SQL: sb.String(), Args: args}
}

// sqlTenantArg converts a groupKey's tenant into the value bound to a
// SQL parameter. An unset tenant becomes SQL NULL via a typed nil.
func sqlTenantArg(k groupKey) any {
	if !k.TenantOK || k.Tenant == "" {
		return nil
	}
	return k.Tenant
}

// buildMoveCTE renders the DELETE ... RETURNING + INSERT CTE that
// moves a batch of ctids from the default partition into a target
// child. Column list is passed in explicitly so callers can filter out
// generated columns.
func buildMoveCTE(schema, defaultTable, targetFQ string, cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = pgx.Identifier{c}.Sanitize()
	}
	colList := strings.Join(quoted, ", ")
	defaultQ := pgx.Identifier{schema}.Sanitize() + "." + pgx.Identifier{defaultTable}.Sanitize()
	targetQ := quoteFQ(targetFQ)
	return fmt.Sprintf(
		`WITH moved AS (DELETE FROM %s WHERE ctid = ANY($1::tid[]) RETURNING %s) INSERT INTO %s (%s) SELECT %s FROM moved`,
		defaultQ, colList, targetQ, colList, colList,
	)
}

// buildNullSummary renders the SELECT that counts NULL-control-column
// rows per tenant so the drain can record them as anomalies at the end.
// When the parent has no tenant column, the query returns one row with
// no tenant grouping.
func buildNullSummary(schema, defaultTable, controlCol, tenantCol string, tenant *string) (string, []any) {
	schemaQ := pgx.Identifier{schema}.Sanitize()
	tableQ := pgx.Identifier{defaultTable}.Sanitize()
	ctrlQ := pgx.Identifier{controlCol}.Sanitize()

	var sb strings.Builder
	var args []any

	if tenantCol == "" {
		fmt.Fprintf(&sb, "SELECT count(*) FROM %s.%s WHERE %s IS NULL", schemaQ, tableQ, ctrlQ)
		return sb.String(), args
	}

	tenantQ := pgx.Identifier{tenantCol}.Sanitize()
	fmt.Fprintf(&sb, "SELECT %s, count(*) FROM %s.%s WHERE %s IS NULL", tenantQ, schemaQ, tableQ, ctrlQ)
	if tenant != nil {
		args = append(args, *tenant)
		fmt.Fprintf(&sb, " AND %s = $%d", tenantQ, len(args))
	}
	fmt.Fprintf(&sb, " GROUP BY %s", tenantQ)
	return sb.String(), args
}

// quoteFQ splits "schema.name" and quotes each half with pgx.Identifier.
// Falls back to returning the input as-is if there is no dot; the caller
// should have already validated the shape.
func quoteFQ(fq string) string {
	idx := strings.Index(fq, ".")
	if idx < 0 {
		return pgx.Identifier{fq}.Sanitize()
	}
	return pgx.Identifier{fq[:idx]}.Sanitize() + "." + pgx.Identifier{fq[idx+1:]}.Sanitize()
}

// insertColumnsSQL returns the SQL and args to fetch the columns of a
// partitioned parent that are NOT generated. The list preserves ordinal
// position so INSERT ... SELECT column orders match.
func insertColumnsSQL() string {
	return `SELECT column_name FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2 AND is_generated = 'NEVER'
ORDER BY ordinal_position`
}
