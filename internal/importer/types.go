// Package importer reconciles PostgreSQL partition state into the
// partman metadata schema for a parent that already exists. ADR-0008
// defines the contract. This package is the implementation.
package importer

import (
	"github.com/jirevwe/gopartman/internal/hooks"
	"github.com/jirevwe/gopartman/internal/naming"
)

// ParentRef mirrors partman.ParentRef so this package does not import
// the root. Retention and Provisioner use the same pattern.
type ParentRef struct {
	SchemaName string
	TableName  string
}

// ReconcileReport summarizes one Import call. See ADR-0008 acceptance
// criteria for the exact meaning of each field.
type ReconcileReport struct {
	Imported []hooks.PartitionRef
	Drifted  []DriftedPartition
	Orphaned []hooks.PartitionRef
	Skipped  []SkippedPartition
}

// DriftedPartition records disagreement between what the child's NAME
// says and what pg_get_expr(relpartbound) says.
type DriftedPartition struct {
	Name        string
	NameBounds  naming.Bounds
	ActualBound string
	Reason      string
}

// SkippedPartition records a PG child that Import could not act on.
type SkippedPartition struct {
	Name   string
	Reason string
}
