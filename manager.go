package go_partman

import (
	"context"
	"errors"
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

	// Seams populated by downstream ADRs (0004–0007).
	registry    registry.Registry       //nolint:unused
	provisioner provisioner.Provisioner //nolint:unused
	retention   retention.Retention     //nolint:unused
	maintainer  maintainer.Maintainer   //nolint:unused
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
// creates its default partition. ADR-0005 wires the implementation.
func (*Manager) RegisterParent(_ context.Context, _ ParentConfig) error {
	return errors.ErrUnsupported
}

// RegisterTenant records a tenant under a registered parent. ADR-0005
// wires the implementation.
func (*Manager) RegisterTenant(_ context.Context, _ TenantConfig) error {
	return errors.ErrUnsupported
}

// RemoveParent deletes a parent from the metadata schema. ADR-0005
// wires the implementation.
func (*Manager) RemoveParent(_ context.Context, _ ParentRef, _ ...RemoveOption) error {
	return errors.ErrUnsupported
}

// RemoveTenant deletes a tenant from the metadata schema. ADR-0005
// wires the implementation.
func (*Manager) RemoveTenant(_ context.Context, _ TenantRef) error {
	return errors.ErrUnsupported
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
