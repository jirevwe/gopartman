// Package registry manages the lifecycle of parents and tenants in the
// partman metadata schema. It validates the target parent table in
// PostgreSQL, writes metadata rows, and delegates partition DDL to the
// Provisioner (ADR-0004). ADR-0005 defines the semantics implemented
// here.
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/jirevwe/gopartman/internal/errs"
	"github.com/jirevwe/gopartman/internal/naming"
	parentsrepo "github.com/jirevwe/gopartman/internal/parents/repo"
	"github.com/jirevwe/gopartman/internal/provisioner"
	tenantsrepo "github.com/jirevwe/gopartman/internal/tenants/repo"
)

const defaultPremake = 4

// Registry is the seam through which Manager creates and removes
// parents and tenants.
type Registry interface {
	RegisterParent(ctx context.Context, cfg ParentConfig) error
	RegisterTenant(ctx context.Context, cfg TenantConfig) error
	RemoveParent(ctx context.Context, ref ParentRef, opts ...RemoveOption) error
	RemoveTenant(ctx context.Context, ref TenantRef) error
	ListParents(ctx context.Context) ([]ParentInfo, error)
	ListTenants(ctx context.Context, ref ParentRef) ([]TenantInfo, error)
}

// PartitionDropper is the narrow slice of Retention (ADR-0006) that
// RemoveParent needs when WithCascadeDrop is set. Retention will
// satisfy this structurally; nothing in this package imports Retention.
type PartitionDropper interface {
	DropAll(ctx context.Context, parent ParentRef) error
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool        *pgxpool.Pool
	Provisioner provisioner.Provisioner
	Dropper     PartitionDropper // optional; nil disables WithCascadeDrop
	Logger      *slog.Logger
}

// Impl is the concrete Registry.
type Impl struct {
	pool        *pgxpool.Pool
	provisioner provisioner.Provisioner
	dropper     PartitionDropper
	logger      *slog.Logger
}

// New constructs an Impl. Pool and Provisioner are required. Dropper is
// optional. Logger defaults to slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Pool == nil {
		return nil, errors.New("registry: Config.Pool is required")
	}
	if cfg.Provisioner == nil {
		return nil, errors.New("registry: Config.Provisioner is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Impl{
		pool:        cfg.Pool,
		provisioner: cfg.Provisioner,
		dropper:     cfg.Dropper,
		logger:      logger,
	}, nil
}

// RegisterParent validates the target table in PostgreSQL, inserts the
// metadata row, and (for non-tenanted parents) provisions the default
// plus initial bounded partitions.
func (r *Impl) RegisterParent(ctx context.Context, cfg ParentConfig) error {
	if err := validateParentIdentifiers(cfg); err != nil {
		return err
	}
	if err := assertTableExists(ctx, r.pool, cfg.SchemaName, cfg.TableName); err != nil {
		return err
	}
	if err := assertPartitionedByRange(ctx, r.pool, cfg.SchemaName, cfg.TableName); err != nil {
		return err
	}
	if err := assertColumnExists(ctx, r.pool, cfg.SchemaName, cfg.TableName, cfg.PartitionBy); err != nil {
		return err
	}
	if cfg.TenantColumn != "" {
		if err := assertColumnExists(ctx, r.pool, cfg.SchemaName, cfg.TableName, cfg.TenantColumn); err != nil {
			return err
		}
	}
	if cfg.RetentionSchema != "" {
		if err := assertSchemaExists(ctx, r.pool, cfg.RetentionSchema); err != nil {
			return err
		}
	}

	intervalLabel, err := naming.PartitionIntervalLabel(cfg.PartitionInterval)
	if err != nil {
		return err
	}

	partitionType := cfg.PartitionType
	if partitionType == "" {
		partitionType = "range"
	}
	premake := int32(cfg.Premake) //nolint:gosec // premake is small; overflow not a concern
	if cfg.Premake == 0 {
		premake = defaultPremake
	}

	params := parentsrepo.UpsertParentTableParams{
		ID:                   ulid.Make().String(),
		TableName:            cfg.TableName,
		SchemaName:           cfg.SchemaName,
		TenantColumn:         optionalText(cfg.TenantColumn),
		PartitionBy:          cfg.PartitionBy,
		PartitionType:        partitionType,
		PartitionInterval:    intervalLabel,
		RetentionPeriod:      intervalFromDuration(cfg.RetentionPeriod),
		RetentionKeepTable:   cfg.RetentionKeepTable,
		RetentionSchema:      optionalText(cfg.RetentionSchema),
		AutomaticMaintenance: !cfg.DisableAutomaticMaintenance,
		Premake:              premake,
	}

	parents := parentsrepo.New(r.pool)
	if _, err := parents.UpsertParentTable(ctx, params); err != nil {
		// ON CONFLICT DO NOTHING with RETURNING yields ErrNoRows when
		// the row already exists.
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s.%s", errs.ErrParentAlreadyExists, cfg.SchemaName, cfg.TableName)
		}
		return fmt.Errorf("partman: insert parent row: %w", err)
	}

	if cfg.TenantColumn != "" {
		return nil
	}

	if _, err := r.provisioner.EnsurePartitions(ctx, provisioner.ParentRef{
		SchemaName: cfg.SchemaName,
		TableName:  cfg.TableName,
	}, nil); err != nil {
		return fmt.Errorf("partman: provision partitions for %s.%s: %w", cfg.SchemaName, cfg.TableName, err)
	}
	return nil
}

// RegisterTenant validates the tenant against the parent, inserts the
// tenant row, and provisions the tenant's partitions.
func (r *Impl) RegisterTenant(ctx context.Context, cfg TenantConfig) error {
	if err := validateIdentifier("parent_schema", cfg.ParentSchema); err != nil {
		return err
	}
	if err := validateIdentifier("parent_name", cfg.ParentName); err != nil {
		return err
	}
	if err := validateIdentifier("tenant_id", cfg.TenantId); err != nil {
		return err
	}

	parents := parentsrepo.New(r.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: cfg.ParentSchema,
		TableName:  cfg.ParentName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s.%s", errs.ErrParentNotFound, cfg.ParentSchema, cfg.ParentName)
		}
		return fmt.Errorf("partman: load parent for tenant: %w", err)
	}
	if !prow.TenantColumn.Valid || prow.TenantColumn.String == "" {
		return fmt.Errorf("%w: %s.%s", errs.ErrParentNotTenanted, cfg.ParentSchema, cfg.ParentName)
	}

	tenants := tenantsrepo.New(r.pool)
	rows, err := tenants.UpsertTenant(ctx, tenantsrepo.UpsertTenantParams{
		ID:            cfg.TenantId,
		ParentTableID: prow.ID,
	})
	if err != nil {
		return fmt.Errorf("partman: insert tenant row: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s under %s.%s", errs.ErrTenantAlreadyExists, cfg.TenantId, cfg.ParentSchema, cfg.ParentName)
	}

	if _, err := r.provisioner.EnsurePartitions(ctx, provisioner.ParentRef{
		SchemaName: cfg.ParentSchema,
		TableName:  cfg.ParentName,
	}, &provisioner.TenantRef{TenantId: cfg.TenantId}); err != nil {
		return fmt.Errorf("partman: provision partitions for %s.%s tenant=%s: %w", cfg.ParentSchema, cfg.ParentName, cfg.TenantId, err)
	}
	return nil
}

// RemoveParent deletes the parent metadata row. FK cascade removes
// dependent tenants and partition metadata. With WithCascadeDrop, the
// PG child tables are also dropped via the injected PartitionDropper.
func (r *Impl) RemoveParent(ctx context.Context, ref ParentRef, opts ...RemoveOption) error {
	if err := validateIdentifier("schema_name", ref.SchemaName); err != nil {
		return err
	}
	if err := validateIdentifier("table_name", ref.TableName); err != nil {
		return err
	}

	o := evalRemoveOptions(opts)
	if o.cascadeDrop {
		if r.dropper == nil {
			return errors.New("partman: WithCascadeDrop requires Retention (ADR-0006) not yet wired")
		}
		if err := r.dropper.DropAll(ctx, ref); err != nil {
			return fmt.Errorf("partman: cascade drop parent %s.%s: %w", ref.SchemaName, ref.TableName, err)
		}
	}

	parents := parentsrepo.New(r.pool)
	if _, err := parents.DeleteParentTable(ctx, parentsrepo.DeleteParentTableParams{
		SchemaName: ref.SchemaName,
		TableName:  ref.TableName,
	}); err != nil {
		return fmt.Errorf("partman: delete parent row: %w", err)
	}
	return nil
}

// RemoveTenant deletes the tenant metadata row. FK cascade removes the
// tenant's partition metadata. PG partition tables stay.
func (r *Impl) RemoveTenant(ctx context.Context, ref TenantRef) error {
	if err := validateIdentifier("parent_schema", ref.ParentSchema); err != nil {
		return err
	}
	if err := validateIdentifier("parent_name", ref.ParentName); err != nil {
		return err
	}
	if err := validateIdentifier("tenant_id", ref.TenantId); err != nil {
		return err
	}

	parents := parentsrepo.New(r.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: ref.ParentSchema,
		TableName:  ref.ParentName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Idempotent: parent already gone means the tenant is gone too.
			return nil
		}
		return fmt.Errorf("partman: load parent for tenant remove: %w", err)
	}

	tenants := tenantsrepo.New(r.pool)
	if _, err := tenants.DeleteTenant(ctx, tenantsrepo.DeleteTenantParams{
		ParentTableID: prow.ID,
		ID:            ref.TenantId,
	}); err != nil {
		return fmt.Errorf("partman: delete tenant row: %w", err)
	}
	return nil
}

// ListParents returns every registered parent.
func (r *Impl) ListParents(ctx context.Context) ([]ParentInfo, error) {
	parents := parentsrepo.New(r.pool)
	rows, err := parents.ListParentTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("partman: list parents: %w", err)
	}
	out := make([]ParentInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, parentInfoFromRow(row))
	}
	return out, nil
}

// ListTenants returns every tenant under the given parent.
func (r *Impl) ListTenants(ctx context.Context, ref ParentRef) ([]TenantInfo, error) {
	if err := validateIdentifier("schema_name", ref.SchemaName); err != nil {
		return nil, err
	}
	if err := validateIdentifier("table_name", ref.TableName); err != nil {
		return nil, err
	}
	parents := parentsrepo.New(r.pool)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: ref.SchemaName,
		TableName:  ref.TableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s.%s", errs.ErrParentNotFound, ref.SchemaName, ref.TableName)
		}
		return nil, fmt.Errorf("partman: load parent for list tenants: %w", err)
	}
	tenants := tenantsrepo.New(r.pool)
	trows, err := tenants.ListTenantsForParent(ctx, prow.ID)
	if err != nil {
		return nil, fmt.Errorf("partman: list tenants: %w", err)
	}
	out := make([]TenantInfo, 0, len(trows))
	for _, row := range trows {
		out = append(out, TenantInfo{
			ParentSchema: ref.SchemaName,
			ParentName:   ref.TableName,
			TenantId:     row.ID,
		})
	}
	return out, nil
}

func parentInfoFromRow(row parentsrepo.PartmanParentTable) ParentInfo {
	return ParentInfo{
		SchemaName:           row.SchemaName,
		TableName:            row.TableName,
		TenantColumn:         optionalTextValue(row.TenantColumn),
		PartitionBy:          row.PartitionBy,
		PartitionType:        row.PartitionType,
		PartitionInterval:    row.PartitionInterval,
		Premake:              int(row.Premake),
		RetentionPeriod:      durationFromInterval(row.RetentionPeriod),
		RetentionSchema:      optionalTextValue(row.RetentionSchema),
		RetentionKeepTable:   row.RetentionKeepTable,
		AutomaticMaintenance: row.AutomaticMaintenance,
	}
}

func optionalText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func optionalTextValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func intervalFromDuration(d time.Duration) pgtype.Interval {
	return pgtype.Interval{
		Microseconds: d.Microseconds(),
		Valid:        true,
	}
}

func durationFromInterval(iv pgtype.Interval) time.Duration {
	if !iv.Valid {
		return 0
	}
	total := time.Duration(iv.Microseconds) * time.Microsecond
	total += time.Duration(iv.Days) * 24 * time.Hour
	total += time.Duration(iv.Months) * 30 * 24 * time.Hour
	return total
}
