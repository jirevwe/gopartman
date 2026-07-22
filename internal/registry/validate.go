package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/gopartman/internal/errs"
)

// identRegex mirrors the naming.alphaNumericRegex used by TableName.Build.
// Registry rejects anything else before it reaches PostgreSQL.
var identRegex = regexp.MustCompile(`^\w+$`)

func validateIdentifier(kind, value string) error {
	if value == "" {
		return fmt.Errorf("partman: %s is required", kind)
	}
	if !identRegex.MatchString(value) {
		return fmt.Errorf("%w: %s %q (allowed: %s)", errs.ErrInvalidIdentifier, kind, value, identRegex.String())
	}
	return nil
}

func validateParentIdentifiers(cfg ParentConfig) error {
	if err := validateIdentifier("schema_name", cfg.SchemaName); err != nil {
		return err
	}
	if err := validateIdentifier("table_name", cfg.TableName); err != nil {
		return err
	}
	if err := validateIdentifier("partition_by", cfg.PartitionBy); err != nil {
		return err
	}
	if cfg.TenantColumn != "" {
		if err := validateIdentifier("tenant_column", cfg.TenantColumn); err != nil {
			return err
		}
	}
	if cfg.RetentionSchema != "" {
		if err := validateIdentifier("retention_schema", cfg.RetentionSchema); err != nil {
			return err
		}
	}
	return nil
}

func assertTableExists(ctx context.Context, pool *pgxpool.Pool, schema, table string) error {
	var one int
	err := pool.QueryRow(ctx, `
		SELECT 1
		FROM pg_class c
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = $1 AND c.relname = $2
	`, schema, table).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("partman: target %s.%s does not exist", schema, table)
	}
	if err != nil {
		return fmt.Errorf("partman: assertTableExists %s.%s: %w", schema, table, err)
	}
	return nil
}

func assertPartitionedByRange(ctx context.Context, pool *pgxpool.Pool, schema, table string) error {
	var strat string
	err := pool.QueryRow(ctx, `
		SELECT p.partstrat::text
		FROM pg_partitioned_table p
		JOIN pg_class c ON c.oid = p.partrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2
	`, schema, table).Scan(&strat)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s.%s", errs.ErrTargetNotPartitioned, schema, table)
	}
	if err != nil {
		return fmt.Errorf("partman: assertPartitionedByRange %s.%s: %w", schema, table, err)
	}
	if strat != "r" {
		return fmt.Errorf("%w: %s.%s uses partstrat=%q, want 'r'", errs.ErrTargetNotPartitioned, schema, table, strat)
	}
	return nil
}

func assertColumnExists(ctx context.Context, pool *pgxpool.Pool, schema, table, column string) error {
	var one int
	err := pool.QueryRow(ctx, `
		SELECT 1
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
	`, schema, table, column).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s.%s.%s", errs.ErrColumnMissing, schema, table, column)
	}
	if err != nil {
		return fmt.Errorf("partman: assertColumnExists %s.%s.%s: %w", schema, table, column, err)
	}
	return nil
}

func assertSchemaExists(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	var one int
	err := pool.QueryRow(ctx, `
		SELECT 1 FROM pg_namespace WHERE nspname = $1
	`, schema).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", errs.ErrArchiveSchemaMissing, schema)
	}
	if err != nil {
		return fmt.Errorf("partman: assertSchemaExists %s: %w", schema, err)
	}
	return nil
}
