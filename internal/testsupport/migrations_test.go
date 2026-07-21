//go:build integration

package testsupport

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// columnsQuery orders by column_name (not ordinal_position). Migrations
// grow a table with ALTER TABLE ADD COLUMN and DROP COLUMN, which leaves
// ordinal-position holes and reorders columns. Those holes have no
// runtime meaning. Logical equivalence is what the smoke test asserts.
const columnsQuery = `
	SELECT table_name, column_name, data_type, is_nullable, column_default
	FROM information_schema.columns
	WHERE table_schema = $1
	ORDER BY table_name, column_name
`

const checksQuery = `
	SELECT tc.table_name, cc.check_clause
	FROM information_schema.check_constraints cc
	JOIN information_schema.table_constraints tc
	  ON cc.constraint_name = tc.constraint_name
	 AND cc.constraint_schema = tc.constraint_schema
	WHERE tc.table_schema = $1
	  AND tc.constraint_type = 'CHECK'
	  AND cc.check_clause NOT LIKE '%IS NOT NULL%'
	ORDER BY tc.table_name, cc.check_clause
`

// TestMigrationsMatchSchema asserts that migrations/*.sql applied in
// order produces the same partman schema as schema.sql. This satisfies
// the acceptance criterion in ADR-0002 lines 121-127.
//
// Strategy: NewPG already applied migrations to schema "partman".
// Apply schema.sql to a parallel schema "partman_check" and diff the
// two using information_schema.columns and check_constraints.
func TestMigrationsMatchSchema(t *testing.T) {
	pool, _ := NewPG(t)
	ctx := t.Context()

	schemaBytes, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatalf("read ../../schema.sql: %v", err)
	}
	parallelSQL := regexp.MustCompile(`\bpartman\b`).ReplaceAllString(
		string(schemaBytes), "partman_check",
	)
	if _, err := pool.Exec(ctx, parallelSQL); err != nil {
		t.Fatalf("apply schema.sql to partman_check: %v", err)
	}

	migrCols := readColumns(t, ctx, pool, "partman")
	schemaCols := readColumns(t, ctx, pool, "partman_check")
	if !reflect.DeepEqual(migrCols, schemaCols) {
		t.Errorf("columns diverge\nmigrations:\n%s\n\nschema.sql:\n%s",
			strings.Join(migrCols, "\n"),
			strings.Join(schemaCols, "\n"),
		)
	}

	migrChecks := readChecks(t, ctx, pool, "partman")
	schemaChecks := readChecks(t, ctx, pool, "partman_check")
	if !reflect.DeepEqual(migrChecks, schemaChecks) {
		t.Errorf("check constraints diverge\nmigrations:\n%s\n\nschema.sql:\n%s",
			strings.Join(migrChecks, "\n"),
			strings.Join(schemaChecks, "\n"),
		)
	}
}

func readColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, columnsQuery, schema)
	if err != nil {
		t.Fatalf("query columns for %s: %v", schema, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var tbl, col, dt, nul string
		var def *string
		if err := rows.Scan(&tbl, &col, &dt, &nul, &def); err != nil {
			t.Fatalf("scan columns for %s: %v", schema, err)
		}
		defStr := "<nil>"
		if def != nil {
			defStr = *def
		}
		out = append(out, fmt.Sprintf("%s.%s type=%s null=%s default=%s",
			tbl, col, dt, nul, defStr))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns rows for %s: %v", schema, err)
	}
	return out
}

func readChecks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, checksQuery, schema)
	if err != nil {
		t.Fatalf("query checks for %s: %v", schema, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var tbl, clause string
		if err := rows.Scan(&tbl, &clause); err != nil {
			t.Fatalf("scan checks for %s: %v", schema, err)
		}
		out = append(out, fmt.Sprintf("%s: %s", tbl, clause))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("checks rows for %s: %v", schema, err)
	}
	return out
}
