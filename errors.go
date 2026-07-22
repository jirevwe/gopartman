// Sentinels callers branch on with errors.Is. Aliased from internal/errs
// so root and internal packages agree on identity.
package go_partman

import "github.com/jirevwe/go_partman/internal/errs"

var (
	ErrParentNotFound          = errs.ErrParentNotFound
	ErrTenantNotFound          = errs.ErrTenantNotFound
	ErrParentAlreadyExists     = errs.ErrParentAlreadyExists
	ErrTenantAlreadyExists     = errs.ErrTenantAlreadyExists
	ErrTargetNotPartitioned    = errs.ErrTargetNotPartitioned
	ErrColumnMissing           = errs.ErrColumnMissing
	ErrParentNotTenanted       = errs.ErrParentNotTenanted
	ErrArchiveSchemaMissing    = errs.ErrArchiveSchemaMissing
	ErrIntervalMismatch        = errs.ErrIntervalMismatch
	ErrLockContention          = errs.ErrLockContention
	ErrHookVetoed              = errs.ErrHookVetoed
	ErrDefaultPartitionMissing = errs.ErrDefaultPartitionMissing
	ErrInvalidIdentifier       = errs.ErrInvalidIdentifier
)
