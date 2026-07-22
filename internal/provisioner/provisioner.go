// Package provisioner creates partitions and the default partition
// under a registered parent. ADR-0004 fills in the interface; this
// file implements it.
//
// The Provisioner is idempotent: a second call with the same clock
// produces zero DDL. All DDL and metadata mutations for one call run
// in one transaction. The Provisioner does NOT take an advisory lock —
// the caller (the Maintainer, ADR-0007) is responsible for that.
package provisioner

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
	"github.com/oklog/ulid/v2"

	"github.com/jirevwe/go_partman/internal/naming"
	parentsrepo "github.com/jirevwe/go_partman/internal/parents/repo"
	partitionsrepo "github.com/jirevwe/go_partman/internal/partitions/repo"
)

// ParentRef identifies a registered parent by (schema, table). It
// mirrors partman.ParentRef; the Provisioner keeps a local copy to
// avoid a circular import.
type ParentRef struct {
	SchemaName string
	TableName  string
}

// TenantRef identifies a tenant under a parent. Nil is valid when the
// parent has no TenantColumn.
type TenantRef struct {
	TenantId string
}

// EnsureReport summarizes what one EnsurePartitions call did.
type EnsureReport struct {
	BoundedCreated int
	DefaultCreated bool
}

// Clock is the interface provisioner needs from a clock. Any type
// satisfying partman.Clock (Now() time.Time) satisfies this.
type Clock interface {
	Now() time.Time
}

// Provisioner is the seam through which Registry and Maintainer create
// partitions.
type Provisioner interface {
	EnsurePartitions(ctx context.Context, parent ParentRef, tenant *TenantRef) (EnsureReport, error)
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool   *pgxpool.Pool
	Clock  Clock
	Logger *slog.Logger
}

// Impl is the concrete Provisioner. Exported so the Manager facade can
// hold a typed field (interface would also work but reads noisier at
// the call site).
type Impl struct {
	pool   *pgxpool.Pool
	clock  Clock
	logger *slog.Logger
}

// New constructs an Impl. Pool and Clock are required; Logger defaults
// to slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Pool == nil {
		return nil, errors.New("provisioner: Config.Pool is required")
	}
	if cfg.Clock == nil {
		return nil, errors.New("provisioner: Config.Clock is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Impl{
		pool:   cfg.Pool,
		clock:  cfg.Clock,
		logger: logger,
	}, nil
}

// EnsurePartitions makes the parent's partitions match the target set
// {current period} ∪ {premake futures} ∪ {default}. Missing partitions
// are created; existing partitions are left alone. All DDL and
// metadata writes run in one transaction; on error the whole call
// rolls back and the next call retries the full set.
func (p *Impl) EnsurePartitions(ctx context.Context, parent ParentRef, tenant *TenantRef) (EnsureReport, error) {
	parents := parentsrepo.New(p.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: parent.SchemaName,
		TableName:  parent.TableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return EnsureReport{}, fmt.Errorf("provisioner: parent %q.%q not registered", parent.SchemaName, parent.TableName)
		}
		return EnsureReport{}, fmt.Errorf("provisioner: load parent: %w", err)
	}

	hasTenantCol := prow.TenantColumn.Valid && prow.TenantColumn.String != ""
	switch {
	case hasTenantCol && tenant == nil:
		return EnsureReport{}, fmt.Errorf("provisioner: parent %q.%q has TenantColumn %q; tenant is required", parent.SchemaName, parent.TableName, prow.TenantColumn.String)
	case !hasTenantCol && tenant != nil:
		return EnsureReport{}, fmt.Errorf("provisioner: parent %q.%q has no TenantColumn; tenant must be nil", parent.SchemaName, parent.TableName)
	}

	k, err := parseIntervalLabel(prow.PartitionInterval)
	if err != nil {
		return EnsureReport{}, fmt.Errorf("provisioner: parent %q.%q: %w", parent.SchemaName, parent.TableName, err)
	}

	required, err := NextBoundsUTC(p.clock.Now().UTC(), canonicalIntervalFor(k), int(prow.Premake)+1)
	if err != nil {
		return EnsureReport{}, err
	}

	partitions := partitionsrepo.New(p.pool)
	existing, err := partitions.ListPartitionsForParent(ctx, prow.ID)
	if err != nil {
		return EnsureReport{}, fmt.Errorf("provisioner: list partitions: %w", err)
	}
	existingSet := buildExistingSet(existing, tenant)

	var toCreate []naming.Bounds
	for _, b := range required {
		if !existingSet[boundsKey{From: b.From.UTC(), To: b.To.UTC()}] {
			toCreate = append(toCreate, b)
		}
	}

	_, err = partitions.GetDefaultPartition(ctx, partitionsrepo.GetDefaultPartitionParams{
		ParentTableID: prow.ID,
		TenantID:      pgtype.Text{}, // default is per-parent; tenant_id is always NULL
	})
	needDefault := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !needDefault {
		return EnsureReport{}, fmt.Errorf("provisioner: lookup default: %w", err)
	}

	if len(toCreate) == 0 && !needDefault {
		return EnsureReport{}, nil
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EnsureReport{}, fmt.Errorf("provisioner: begin tx: %w", err)
	}
	defer func() {
		// pgx.ErrTxClosed is expected after a successful commit; ignore.
		_ = tx.Rollback(ctx)
	}()

	txPartitions := partitionsrepo.New(tx)

	tenantIdStr := ""
	var tenantParam pgtype.Text
	if tenant != nil {
		tenantIdStr = tenant.TenantId
		tenantParam = pgtype.Text{String: tenant.TenantId, Valid: true}
	}

	for _, b := range toCreate {
		tn := naming.TableName{
			SchemaName: prow.SchemaName,
			ParentName: prow.TableName,
			TenantId:   tenantIdStr,
			Bounds:     b,
			IsDefault:  false,
			Interval:   canonicalIntervalFor(k),
		}
		fq, err := tn.Build()
		if err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: build partition name: %w", err)
		}
		childSchema, childTable, ok := splitFQ(fq)
		if !ok {
			return EnsureReport{}, fmt.Errorf("provisioner: unexpected FQ shape %q", fq)
		}
		ddl := buildBoundedPartitionDDL(prow.SchemaName, prow.TableName, childSchema, childTable, tenantIdStr, b)
		if _, err := tx.Exec(ctx, ddl); err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: create bounded partition %s: %w", fq, err)
		}
		if err := txPartitions.UpsertPartition(ctx, partitionsrepo.UpsertPartitionParams{
			ID:                  ulid.Make().String(),
			Name:                fq,
			ParentTableID:       prow.ID,
			TenantID:            tenantParam,
			PartitionBy:         prow.PartitionBy,
			PartitionType:       prow.PartitionType,
			PartitionBoundsFrom: pgtype.Timestamptz{Time: b.From, Valid: true},
			PartitionBoundsTo:   pgtype.Timestamptz{Time: b.To, Valid: true},
			IsDefault:           false,
		}); err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: upsert bounded metadata %s: %w", fq, err)
		}
	}

	if needDefault {
		tn := naming.TableName{
			SchemaName: prow.SchemaName,
			ParentName: prow.TableName,
			TenantId:   "", // default is per-parent, not per-tenant
			IsDefault:  true,
			Interval:   canonicalIntervalFor(k),
		}
		fq, err := tn.Build()
		if err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: build default name: %w", err)
		}
		childSchema, childTable, ok := splitFQ(fq)
		if !ok {
			return EnsureReport{}, fmt.Errorf("provisioner: unexpected FQ shape %q", fq)
		}
		ddl := buildDefaultPartitionDDL(prow.SchemaName, prow.TableName, childSchema, childTable)
		if _, err := tx.Exec(ctx, ddl); err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: create default partition %s: %w", fq, err)
		}
		// bounds_from/to are NOT NULL columns; use the epoch as a
		// visually obvious sentinel for the default.
		epoch := time.Time{}
		if err := txPartitions.UpsertPartition(ctx, partitionsrepo.UpsertPartitionParams{
			ID:                  ulid.Make().String(),
			Name:                fq,
			ParentTableID:       prow.ID,
			TenantID:            pgtype.Text{}, // NULL
			PartitionBy:         prow.PartitionBy,
			PartitionType:       prow.PartitionType,
			PartitionBoundsFrom: pgtype.Timestamptz{Time: epoch, Valid: true},
			PartitionBoundsTo:   pgtype.Timestamptz{Time: epoch, Valid: true},
			IsDefault:           true,
		}); err != nil {
			return EnsureReport{}, fmt.Errorf("provisioner: upsert default metadata %s: %w", fq, err)
		}
	}

	// TODO(ADR-0010): emit partman.partitions_created_total and
	// partman.default_partitions_created_total via the Meter.
	if err := tx.Commit(ctx); err != nil {
		return EnsureReport{}, fmt.Errorf("provisioner: commit: %w", err)
	}
	return EnsureReport{
		BoundedCreated: len(toCreate),
		DefaultCreated: needDefault,
	}, nil
}

type boundsKey struct {
	From time.Time
	To   time.Time
}

// buildExistingSet indexes the active bounded partitions that match
// the caller's tenant (or the no-tenant case). Detached, dropped, and
// default rows are excluded. A row's tenant matches when its stored
// tenant_id equals the caller's tenant (both nil ↔ both set to the
// same string).
func buildExistingSet(rows []partitionsrepo.PartmanPartition, tenant *TenantRef) map[boundsKey]bool {
	out := make(map[boundsKey]bool, len(rows))
	for _, ep := range rows {
		if ep.IsDefault || ep.Status != "active" {
			continue
		}
		rowTenant := ""
		if ep.TenantID.Valid {
			rowTenant = ep.TenantID.String
		}
		wanted := ""
		if tenant != nil {
			wanted = tenant.TenantId
		}
		if rowTenant != wanted {
			continue
		}
		out[boundsKey{
			From: ep.PartitionBoundsFrom.Time.UTC(),
			To:   ep.PartitionBoundsTo.Time.UTC(),
		}] = true
	}
	return out
}

// splitFQ splits "schema.name" into ("schema", "name", true). Returns
// ok=false if the input has no dot.
func splitFQ(fq string) (schema, name string, ok bool) {
	idx := strings.Index(fq, ".")
	if idx < 0 {
		return "", "", false
	}
	return fq[:idx], fq[idx+1:], true
}
