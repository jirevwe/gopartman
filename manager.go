package gopartman

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/gopartman/internal/drain"
	"github.com/jirevwe/gopartman/internal/importer"
	"github.com/jirevwe/gopartman/internal/maintainer"
	"github.com/jirevwe/gopartman/internal/provisioner"
	"github.com/jirevwe/gopartman/internal/registry"
	"github.com/jirevwe/gopartman/internal/retention"
)

// Manager is the facade for gopartman. It composes the four internal
// interfaces (registry, provisioner, retention, maintainer). Methods
// return errors.ErrUnsupported until downstream epics wire them.
//
// Construct with New. Manager is concrete on purpose; callers who need
// test doubles wrap it.
type Manager struct {
	db       *pgxpool.Pool
	clock    Clock
	logger   *slog.Logger
	hook     Hook
	meter    Meter
	schedule time.Duration

	provisioner provisioner.Provisioner
	registry    registry.Registry
	retention   *retention.Impl
	maintainer  maintainer.Maintainer
	importer    *importer.Impl
	drain       *drain.Impl
}

// retentionDropperAdapter satisfies registry.PartitionDropper by
// converting the registry's ParentRef into the (schema, table) pair
// that Retention.DropAll accepts. Keeps internal/retention free of
// an import on internal/registry.
type retentionDropperAdapter struct {
	r *retention.Impl
}

func (a retentionDropperAdapter) DropAll(ctx context.Context, ref registry.ParentRef) error {
	return a.r.DropAll(ctx, ref.SchemaName, ref.TableName)
}

// Start begins the maintenance loop. See ADR-0007.
func (m *Manager) Start(ctx context.Context) error {
	return m.maintainer.Start(ctx)
}

// Stop halts the maintenance loop, honoring the context deadline.
// See ADR-0007.
func (m *Manager) Stop(ctx context.Context) error {
	return m.maintainer.Stop(ctx)
}

// Maintain triggers one maintenance run. See ADR-0007.
func (m *Manager) Maintain(ctx context.Context) error {
	return m.maintainer.Maintain(ctx)
}

// RegisterParent records a parent table in the metadata schema and
// creates its default partition. See ADR-0005.
func (m *Manager) RegisterParent(ctx context.Context, p ParentConfig) error {
	return m.registry.RegisterParent(ctx, p)
}

// RegisterTenant records a tenant under a registered parent. See
// ADR-0005.
func (m *Manager) RegisterTenant(ctx context.Context, t TenantConfig) error {
	return m.registry.RegisterTenant(ctx, t)
}

// RemoveParent deletes a parent from the metadata schema. See ADR-0005.
func (m *Manager) RemoveParent(ctx context.Context, ref ParentRef, opts ...RemoveOption) error {
	return m.registry.RemoveParent(ctx, ref, opts...)
}

// RemoveTenant deletes a tenant from the metadata schema. See ADR-0005.
func (m *Manager) RemoveTenant(ctx context.Context, ref TenantRef) error {
	return m.registry.RemoveTenant(ctx, ref)
}

// ListParents returns every registered parent.
func (m *Manager) ListParents(ctx context.Context) ([]ParentInfo, error) {
	return m.registry.ListParents(ctx)
}

// ListTenants returns every tenant under the given parent.
func (m *Manager) ListTenants(ctx context.Context, ref ParentRef) ([]TenantInfo, error) {
	return m.registry.ListTenants(ctx, ref)
}

// ImportExisting reconciles PostgreSQL state into the metadata schema
// for a parent that already exists. See ADR-0008.
func (m *Manager) ImportExisting(ctx context.Context, ref ParentRef) (ReconcileReport, error) {
	rep, err := m.importer.Import(ctx, importer.ParentRef{
		SchemaName: ref.SchemaName,
		TableName:  ref.TableName,
	})
	if err != nil {
		return ReconcileReport{}, err
	}
	return ReconcileReport{
		Imported: rep.Imported,
		Drifted:  convertDrifted(rep.Drifted),
		Orphaned: rep.Orphaned,
		Skipped:  convertSkipped(rep.Skipped),
	}, nil
}

func convertDrifted(in []importer.DriftedPartition) []DriftedPartition {
	if len(in) == 0 {
		return nil
	}
	out := make([]DriftedPartition, len(in))
	for i, d := range in {
		out[i] = DriftedPartition{
			Name:        d.Name,
			NameBounds:  d.NameBounds,
			ActualBound: d.ActualBound,
			Reason:      d.Reason,
		}
	}
	return out
}

func convertSkipped(in []importer.SkippedPartition) []SkippedPartition {
	if len(in) == 0 {
		return nil
	}
	out := make([]SkippedPartition, len(in))
	for i, s := range in {
		out[i] = SkippedPartition{Name: s.Name, Reason: s.Reason}
	}
	return out
}

// PartitionData drains rows from the default partition into the
// correct bounded child partitions in batches. See ADR-0009.
func (m *Manager) PartitionData(ctx context.Context, ref ParentRef, opts ...DrainOption) (DrainReport, error) {
	o := evalDrainOptions(opts)
	rep, err := m.drain.PartitionData(ctx,
		drain.ParentRef{SchemaName: ref.SchemaName, TableName: ref.TableName},
		drain.Options{
			BatchSize:  o.batchSize,
			MaxBatches: o.maxBatches,
			Tenant:     o.tenant,
		},
	)
	if err != nil {
		return DrainReport{}, err
	}
	return convertDrainReport(rep), nil
}

func convertDrainReport(in drain.Report) DrainReport {
	out := DrainReport{
		RowsMoved:  in.RowsMoved,
		BatchesRun: in.BatchesRun,
	}
	if len(in.Anomalies) > 0 {
		out.Anomalies = make([]DrainAnomaly, len(in.Anomalies))
		for i, a := range in.Anomalies {
			out.Anomalies[i] = DrainAnomaly{
				MissingPartitionBounds: a.MissingPartitionBounds,
				TenantId:               a.TenantId,
				RowCount:               a.RowCount,
			}
		}
	}
	return out
}

// initInternals constructs the Provisioner, Retention, and Registry
// after the options have been applied. Maintainer (ADR-0007) remains
// nil until its epic lands.
func (m *Manager) initInternals() error {
	prov, err := provisioner.New(provisioner.Config{
		Pool:   m.db,
		Clock:  m.clock,
		Logger: m.logger,
		Meter:  m.meter,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init provisioner: %w", err)
	}
	m.provisioner = prov

	ret, err := retention.New(retention.Config{
		Pool:   m.db,
		Clock:  m.clock,
		Hook:   m.hook,
		Logger: m.logger,
		Meter:  m.meter,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init retention: %w", err)
	}
	m.retention = ret

	reg, err := registry.New(registry.Config{
		Pool:        m.db,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
		Logger:      m.logger,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init registry: %w", err)
	}
	m.registry = reg

	maint, err := maintainer.New(maintainer.Config{
		Pool:        m.db,
		Registry:    reg,
		Provisioner: prov,
		Retention:   ret,
		Clock:       m.clock,
		Logger:      m.logger,
		Meter:       m.meter,
		Schedule:    m.schedule,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init maintainer: %w", err)
	}
	m.maintainer = maint

	imp, err := importer.New(importer.Config{
		Pool:   m.db,
		Logger: m.logger,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init importer: %w", err)
	}
	m.importer = imp

	dr, err := drain.New(drain.Config{
		Pool:   m.db,
		Clock:  m.clock,
		Logger: m.logger,
		Meter:  m.meter,
	})
	if err != nil {
		return fmt.Errorf("gopartman: init drain: %w", err)
	}
	m.drain = dr
	return nil
}
