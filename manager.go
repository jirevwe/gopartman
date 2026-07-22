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
	retention   retention.Retention   //nolint:unused
	maintainer  maintainer.Maintainer //nolint:unused
}

// Start begins the maintenance loop. ADR-0007 wires the implementation.
func (*Manager) Start(_ context.Context) error {
	return errors.ErrUnsupported
}

// Stop halts the maintenance loop, honoring the context deadline.
// ADR-0007 wires the implementation.
func (*Manager) Stop(_ context.Context) error {
	return errors.ErrUnsupported
}

// Maintain triggers one maintenance run. ADR-0007 wires the
// implementation.
func (*Manager) Maintain(_ context.Context) error {
	return errors.ErrUnsupported
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

// initInternals constructs the Provisioner and Registry after the
// options have been applied. Retention (ADR-0006) and Maintainer
// (ADR-0007) remain nil until their epics land; RegisterParent works
// without them, and RemoveParent with WithCascadeDrop returns a typed
// error until Retention is wired.
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

	reg, err := registry.New(registry.Config{
		Pool:        m.db,
		Provisioner: prov,
		Logger:      m.logger,
	})
	if err != nil {
		return fmt.Errorf("go_partman: init registry: %w", err)
	}
	m.registry = reg
	return nil
}
