package errs

import (
	"errors"
	"fmt"
	"testing"
)

// Every sentinel must round-trip through fmt.Errorf("%w: ...").
// errors.Is on the wrapped value must return true.
func TestSentinelWrapping(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
	}{
		{"ErrParentNotFound", ErrParentNotFound},
		{"ErrTenantNotFound", ErrTenantNotFound},
		{"ErrParentAlreadyExists", ErrParentAlreadyExists},
		{"ErrTenantAlreadyExists", ErrTenantAlreadyExists},
		{"ErrTargetNotPartitioned", ErrTargetNotPartitioned},
		{"ErrColumnMissing", ErrColumnMissing},
		{"ErrParentNotTenanted", ErrParentNotTenanted},
		{"ErrArchiveSchemaMissing", ErrArchiveSchemaMissing},
		{"ErrIntervalMismatch", ErrIntervalMismatch},
		{"ErrLockContention", ErrLockContention},
		{"ErrHookVetoed", ErrHookVetoed},
		{"ErrDefaultPartitionMissing", ErrDefaultPartitionMissing},
		{"ErrInvalidIdentifier", ErrInvalidIdentifier},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("%w: extra context", tc.sentinel)
			if !errors.Is(wrapped, tc.sentinel) {
				t.Fatalf("errors.Is on wrapped %s failed", tc.name)
			}
		})
	}
}

// Distinct sentinels must not match each other.
func TestSentinelsDistinct(t *testing.T) {
	if errors.Is(ErrParentNotFound, ErrTenantNotFound) {
		t.Fatal("ErrParentNotFound and ErrTenantNotFound must be distinct")
	}
	if errors.Is(ErrLockContention, ErrHookVetoed) {
		t.Fatal("ErrLockContention and ErrHookVetoed must be distinct")
	}
}
