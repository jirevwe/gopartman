package registry

import "errors"

// Sentinels callers branch on with errors.Is. Registry (ADR-0005) is
// the first producer; downstream epics add more values under ADR-0010.
// The root package re-exports every sentinel here.
var (
	ErrParentNotFound       = errors.New("partman: parent not found")
	ErrTenantNotFound       = errors.New("partman: tenant not found")
	ErrParentAlreadyExists  = errors.New("partman: parent already exists")
	ErrTenantAlreadyExists  = errors.New("partman: tenant already exists")
	ErrTargetNotPartitioned = errors.New("partman: target table is not partitioned by range")
	ErrColumnMissing        = errors.New("partman: required column missing on target table")
	ErrParentNotTenanted    = errors.New("partman: parent has no tenant column")
	ErrArchiveSchemaMissing = errors.New("partman: retention schema does not exist")
	ErrIntervalMismatch     = errors.New("partman: partition interval mismatch")
)
