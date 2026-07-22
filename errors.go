// Sentinels callers branch on with errors.Is. Aliased from
// internal/registry so root and internal packages agree on identity.
package go_partman

import "github.com/jirevwe/go_partman/internal/registry"

var (
	ErrParentNotFound       = registry.ErrParentNotFound
	ErrTenantNotFound       = registry.ErrTenantNotFound
	ErrParentAlreadyExists  = registry.ErrParentAlreadyExists
	ErrTenantAlreadyExists  = registry.ErrTenantAlreadyExists
	ErrTargetNotPartitioned = registry.ErrTargetNotPartitioned
	ErrColumnMissing        = registry.ErrColumnMissing
	ErrParentNotTenanted    = registry.ErrParentNotTenanted
	ErrArchiveSchemaMissing = registry.ErrArchiveSchemaMissing
)
