// Package errs holds every public sentinel gopartman exposes. The root
// package re-exports each one; internal packages import this package
// directly. Keep this package free of any other symbols.
package errs

import "errors"

var (
	ErrParentNotFound          = errors.New("partman: parent not found")
	ErrTenantNotFound          = errors.New("partman: tenant not found")
	ErrParentAlreadyExists     = errors.New("partman: parent already exists")
	ErrTenantAlreadyExists     = errors.New("partman: tenant already exists")
	ErrTargetNotPartitioned    = errors.New("partman: target table is not partitioned by range")
	ErrColumnMissing           = errors.New("partman: required column missing on target")
	ErrParentNotTenanted       = errors.New("partman: parent has no tenant column")
	ErrArchiveSchemaMissing    = errors.New("partman: retention schema does not exist")
	ErrIntervalMismatch        = errors.New("partman: partition interval mismatch")
	ErrLockContention          = errors.New("partman: advisory lock held by another process")
	ErrHookVetoed              = errors.New("partman: pre-drop hook skipped the partition")
	ErrDefaultPartitionMissing = errors.New("partman: default partition missing")
	ErrInvalidIdentifier       = errors.New("partman: identifier contains invalid characters")
)
