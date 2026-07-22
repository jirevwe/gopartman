package go_partman

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/go_partman/internal/maintainer"
	"github.com/jirevwe/go_partman/internal/provisioner"
	"github.com/jirevwe/go_partman/internal/registry"
	"github.com/jirevwe/go_partman/internal/retention"
)

// Manager is the facade for go_partman. It composes the four internal
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
// for a parent that already exists. ADR-0008 wires the implementation.
func (*Manager) ImportExisting(_ context.Context, _ ParentRef) (ReconcileReport, error) {
	return ReconcileReport{}, errors.ErrUnsupported
}

// PartitionData drains rows from the default partition into the
// correct bounded child partitions in batches. ADR-0009 wires the
// implementation.
func (*Manager) PartitionData(_ context.Context, _ ParentRef, _ ...DrainOption) (DrainReport, error) {
	return DrainReport{}, errors.ErrUnsupported
}

// initInternals constructs the Provisioner, Retention, and Registry
// after the options have been applied. Maintainer (ADR-0007) remains
// nil until its epic lands.
func (m *Manager) initInternals() error {
	prov, err := provisioner.New(provisioner.Config{
		Pool:   m.db,
		Clock:  m.clock,
		Logger: m.logger,
	})
	if err != nil {
		return fmt.Errorf("go_partman: init provisioner: %w", err)
	}
	m.provisioner = prov

	ret, err := retention.New(retention.Config{
		Pool:   m.db,
		Clock:  m.clock,
		Hook:   m.hook,
		Logger: m.logger,
	})
	if err != nil {
		return fmt.Errorf("go_partman: init retention: %w", err)
	}
	m.retention = ret

	reg, err := registry.New(registry.Config{
		Pool:        m.db,
		Provisioner: prov,
		Dropper:     retentionDropperAdapter{r: ret},
		Logger:      m.logger,
	})
	if err != nil {
		return fmt.Errorf("go_partman: init registry: %w", err)
	}
	m.registry = reg

	maint, err := maintainer.New(maintainer.Config{
		Pool:        m.db,
		Registry:    reg,
		Provisioner: prov,
		Retention:   ret,
		Clock:       m.clock,
		Logger:      m.logger,
		Schedule:    m.schedule,
	})
	if err != nil {
		return fmt.Errorf("go_partman: init maintainer: %w", err)
	}
	m.maintainer = maint
	return nil
}
