package go_partman

import "github.com/jirevwe/go_partman/internal/registry"

// ParentConfig describes a partitioned parent table to register with
// RegisterParent. Aliased from internal/registry.
type ParentConfig = registry.ParentConfig

// ParentRef identifies a registered parent by (schema, table). Aliased
// from internal/registry.
type ParentRef = registry.ParentRef

// ParentInfo is the read-only view returned by Manager.ListParents.
type ParentInfo = registry.ParentInfo

// RemoveOption tunes the behavior of RemoveParent.
type RemoveOption = registry.RemoveOption

// WithCascadeDrop makes RemoveParent drop child partitions as part of
// removal instead of leaving them in place.
var WithCascadeDrop = registry.WithCascadeDrop

// ReconcileReport summarizes what ImportExisting observed and inserted.
// Imported lists partitions whose metadata was newly written on this
// call. Drifted lists partitions whose child name disagrees with the
// PG-side bound expression. Orphaned lists metadata rows with no
// matching PG child. Skipped lists PG children whose names do not
// match TableName.Build's format. See ADR-0008.
type ReconcileReport struct {
	Imported []PartitionRef
	Drifted  []DriftedPartition
	Orphaned []PartitionRef
	Skipped  []SkippedPartition
}

// DriftedPartition records disagreement between what the child's NAME
// says (the parsed bound) and what PG's pg_get_expr says (the actual
// bound). ImportExisting does NOT rewrite drifted partitions; the
// operator inspects the report.
type DriftedPartition struct {
	Name        string
	NameBounds  Bounds
	ActualBound string // raw pg_get_expr(relpartbound) output
	Reason      string
}

// SkippedPartition records a PG child that could not be imported. Reason
// is a short human-readable string, e.g. "non-conforming name".
type SkippedPartition struct {
	Name   string
	Reason string
}
