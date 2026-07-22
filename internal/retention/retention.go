// Package retention drops, detaches, or archives expired partitions.
// ADR-0006 defines the semantics implemented here. Retention does NOT
// hold an advisory lock; the Maintainer (ADR-0007) is responsible.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/go_partman/internal/hooks"
	"github.com/jirevwe/go_partman/internal/naming"
	parentsrepo "github.com/jirevwe/go_partman/internal/parents/repo"
	partitionsrepo "github.com/jirevwe/go_partman/internal/partitions/repo"
	"github.com/jirevwe/go_partman/internal/registry"
)

// ParentRef identifies a registered parent by (schema, table). It
// mirrors partman.ParentRef; the retention package keeps a local copy
// to avoid a circular import.
type ParentRef struct {
	SchemaName string
	TableName  string
}

// Clock is the interface Retention needs from a clock. Any type
// satisfying partman.Clock (Now() time.Time) satisfies this.
type Clock interface {
	Now() time.Time
}

// SweepReport summarizes one Sweep call. Considered counts the
// expired candidates the sweep looked at. The four slices list the
// PartitionRefs the sweep placed under each fate. DryRun mirrors the
// option so callers do not have to plumb it separately.
type SweepReport struct {
	Considered int
	Dropped    []hooks.PartitionRef
	Detached   []hooks.PartitionRef
	Archived   []hooks.PartitionRef
	Skipped    []hooks.PartitionRef
	DryRun     bool
}

// Retention is the interface Manager and Maintainer consume.
type Retention interface {
	Sweep(ctx context.Context, parent ParentRef, opts ...SweepOption) (SweepReport, error)
	DropAll(ctx context.Context, schemaName, tableName string) error
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool   *pgxpool.Pool
	Clock  Clock
	Hook   hooks.Hook
	Logger *slog.Logger
}

// Impl is the concrete Retention. Exported so the Manager facade can
// hold a typed field.
type Impl struct {
	pool   *pgxpool.Pool
	clock  Clock
	hook   hooks.Hook
	logger *slog.Logger
}

// New constructs an Impl. Pool and Clock are required. Hook is
// optional; nil is equivalent to always returning HookDrop. Logger
// defaults to slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Pool == nil {
		return nil, errors.New("retention: Config.Pool is required")
	}
	if cfg.Clock == nil {
		return nil, errors.New("retention: Config.Clock is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Impl{
		pool:   cfg.Pool,
		clock:  cfg.Clock,
		hook:   cfg.Hook,
		logger: logger,
	}, nil
}

// Sweep drops, detaches, or archives every partition whose upper
// bound is at or before the retention cutoff. The default, current,
// and future partitions are never candidates — the SQL filter enforces
// this. Per-partition failures are captured in Skipped; only
// sweep-level failures (parent load, list query) return an error.
func (p *Impl) Sweep(ctx context.Context, parent ParentRef, opts ...SweepOption) (SweepReport, error) {
	o := evalSweepOptions(opts)

	parents := parentsrepo.New(p.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: parent.SchemaName,
		TableName:  parent.TableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SweepReport{DryRun: o.dryRun}, fmt.Errorf("%w: %s.%s", registry.ErrParentNotFound, parent.SchemaName, parent.TableName)
		}
		return SweepReport{DryRun: o.dryRun}, fmt.Errorf("retention: load parent: %w", err)
	}

	cutoff := computeCutoff(p.clock.Now().UTC(), prow.RetentionPeriod)

	partitions := partitionsrepo.New(p.pool)
	candidates, err := partitions.ListExpiredPartitions(ctx, partitionsrepo.ListExpiredPartitionsParams{
		ParentTableID: prow.ID,
		Cutoff:        pgtype.Timestamptz{Time: cutoff, Valid: true},
	})
	if err != nil {
		return SweepReport{DryRun: o.dryRun}, fmt.Errorf("retention: list expired: %w", err)
	}

	report := SweepReport{Considered: len(candidates), DryRun: o.dryRun}

	for i := range candidates {
		row := candidates[i]
		ref, err := buildPartitionRef(prow.TableName, row)
		if err != nil {
			p.logger.Warn("retention: bad partition metadata; skipping",
				"parent", parent.SchemaName+"."+parent.TableName,
				"partition_id", row.ID,
				"name", row.Name,
				"err", err,
			)
			// TODO(ADR-0010): emit partman.retention_skipped_total{reason=bad_metadata}.
			report.Skipped = append(report.Skipped, ref)
			continue
		}

		decision := hooks.HookDrop
		if p.hook != nil {
			decision = p.hook(ctx, ref)
		}

		if decision == hooks.HookArchive && !hasRetentionSchema(prow.RetentionSchema) {
			p.logger.Warn("retention: HookArchive requested but retention_schema is empty",
				"parent", parent.SchemaName+"."+parent.TableName,
				"partition", row.Name,
			)
			// TODO(ADR-0010): emit partman.retention_skipped_total{reason=missing_archive_schema}.
			report.Skipped = append(report.Skipped, ref)
			continue
		}

		if o.dryRun {
			switch decision {
			case hooks.HookDrop:
				report.Dropped = append(report.Dropped, ref)
			case hooks.HookDetach:
				report.Detached = append(report.Detached, ref)
			case hooks.HookArchive:
				report.Archived = append(report.Archived, ref)
			case hooks.HookSkip:
				report.Skipped = append(report.Skipped, ref)
			default:
				report.Skipped = append(report.Skipped, ref)
			}
			continue
		}

		if decision == hooks.HookSkip {
			p.logger.Debug("retention: HookSkip; leaving partition",
				"parent", parent.SchemaName+"."+parent.TableName,
				"partition", row.Name,
			)
			// TODO(ADR-0010): emit partman.retention_skipped_total{reason=hook_skip}.
			report.Skipped = append(report.Skipped, ref)
			continue
		}

		if err := p.applyDecision(ctx, prow, row, decision); err != nil {
			p.logger.Warn("retention: partition action failed; skipping",
				"parent", parent.SchemaName+"."+parent.TableName,
				"partition", row.Name,
				"decision", decisionLabel(decision),
				"err", err,
			)
			// TODO(ADR-0010): emit partman.retention_skipped_total{reason=ddl_error}.
			report.Skipped = append(report.Skipped, ref)
			continue
		}

		switch decision {
		case hooks.HookDrop:
			// TODO(ADR-0010): emit partman.partitions_dropped_total.
			report.Dropped = append(report.Dropped, ref)
		case hooks.HookDetach:
			// TODO(ADR-0010): emit partman.partitions_detached_total.
			report.Detached = append(report.Detached, ref)
		case hooks.HookArchive:
			// TODO(ADR-0010): emit partman.partitions_archived_total.
			report.Archived = append(report.Archived, ref)
		}
	}

	// TODO(ADR-0010): emit partman.retention_duration_seconds.
	return report, nil
}

// DropAll drops every child partition of the parent, including the
// default and any already-detached partitions, and marks every
// metadata row status='dropped'. Called by Registry.RemoveParent with
// WithCascadeDrop. The Hook is NOT invoked. Idempotent: absent
// parent returns nil.
func (p *Impl) DropAll(ctx context.Context, schemaName, tableName string) error {
	parents := parentsrepo.New(p.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: schemaName,
		TableName:  tableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("retention: load parent: %w", err)
	}

	partitions := partitionsrepo.New(p.pool)
	rows, err := partitions.ListPartitionsForParent(ctx, prow.ID)
	if err != nil {
		return fmt.Errorf("retention: list partitions: %w", err)
	}

	for i := range rows {
		row := rows[i]
		childSchema, childTable, ok := splitFQ(row.Name)
		if !ok {
			return fmt.Errorf("retention: unexpected FQ shape %q", row.Name)
		}
		if err := p.dropOne(ctx, childSchema, childTable, row.ID); err != nil {
			return fmt.Errorf("retention: drop %s: %w", row.Name, err)
		}
	}
	return nil
}

// applyDecision runs the DDL and metadata write for one partition in
// a fresh transaction. On any error the transaction rolls back and
// no metadata write is committed for this partition.
func (p *Impl) applyDecision(
	ctx context.Context,
	prow parentsrepo.PartmanParentTable,
	row partitionsrepo.PartmanPartition,
	decision hooks.HookDecision,
) error {
	childSchema, childTable, ok := splitFQ(row.Name)
	if !ok {
		return fmt.Errorf("bad partition name %q", row.Name)
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		// pgx.ErrTxClosed is expected after a successful commit.
		_ = tx.Rollback(ctx)
	}()

	txPartitions := partitionsrepo.New(tx)

	switch decision {
	case hooks.HookDrop:
		ddl := buildDropDDL(childSchema, childTable)
		if _, err := tx.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
		if err := txPartitions.MarkPartitionDropped(ctx, row.ID); err != nil {
			return fmt.Errorf("mark dropped: %w", err)
		}
	case hooks.HookDetach:
		ddl := buildDetachDDL(prow.SchemaName, prow.TableName, childSchema, childTable)
		if _, err := tx.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("detach: %w", err)
		}
		if err := txPartitions.MarkPartitionDetached(ctx, row.ID); err != nil {
			return fmt.Errorf("mark detached: %w", err)
		}
	case hooks.HookArchive:
		detachDDL := buildDetachDDL(prow.SchemaName, prow.TableName, childSchema, childTable)
		if _, err := tx.Exec(ctx, detachDDL); err != nil {
			return fmt.Errorf("archive detach: %w", err)
		}
		setSchemaDDL := buildSetSchemaDDL(childSchema, childTable, prow.RetentionSchema.String)
		if _, err := tx.Exec(ctx, setSchemaDDL); err != nil {
			return fmt.Errorf("archive set schema: %w", err)
		}
		if err := txPartitions.MarkPartitionDetached(ctx, row.ID); err != nil {
			return fmt.Errorf("mark archived: %w", err)
		}
	default:
		return fmt.Errorf("unknown HookDecision %d", decision)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// dropOne drops one child unconditionally (DROP TABLE IF EXISTS ...
// CASCADE) and marks the metadata row status='dropped'. Used by
// DropAll.
func (p *Impl) dropOne(ctx context.Context, childSchema, childTable, partitionID string) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	ddl := buildDropIfExistsDDL(childSchema, childTable)
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	txPartitions := partitionsrepo.New(tx)
	if err := txPartitions.MarkPartitionDropped(ctx, partitionID); err != nil {
		return fmt.Errorf("mark dropped: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// buildPartitionRef converts a metadata row to the PartitionRef shape
// the Hook sees. Bounds are read straight from the row's timestamptz
// columns and normalized to UTC.
func buildPartitionRef(parentTable string, row partitionsrepo.PartmanPartition) (hooks.PartitionRef, error) {
	childSchema, _, ok := splitFQ(row.Name)
	if !ok {
		return hooks.PartitionRef{}, fmt.Errorf("bad partition name %q", row.Name)
	}
	tenantID := ""
	if row.TenantID.Valid {
		tenantID = row.TenantID.String
	}
	return hooks.PartitionRef{
		Schema:    childSchema,
		Parent:    parentTable,
		TenantId:  tenantID,
		Bounds:    naming.Bounds{From: row.PartitionBoundsFrom.Time.UTC(), To: row.PartitionBoundsTo.Time.UTC()},
		IsDefault: row.IsDefault,
	}, nil
}

// splitFQ splits "schema.name" into ("schema", "name", true). Returns
// ok=false if the input has no dot. Mirrors provisioner.splitFQ.
func splitFQ(fq string) (schema, name string, ok bool) {
	idx := strings.Index(fq, ".")
	if idx < 0 {
		return "", "", false
	}
	return fq[:idx], fq[idx+1:], true
}

func hasRetentionSchema(t pgtype.Text) bool {
	return t.Valid && t.String != ""
}

func decisionLabel(d hooks.HookDecision) string {
	switch d {
	case hooks.HookDrop:
		return "drop"
	case hooks.HookDetach:
		return "detach"
	case hooks.HookArchive:
		return "archive"
	case hooks.HookSkip:
		return "skip"
	default:
		return "unknown"
	}
}
