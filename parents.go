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

// ReconcileReport summarizes what ImportExisting inserted into the
// metadata schema. Anomalies is a list of conditions the operation
// detected but could not resolve. ADR-0008 fills this in.
type ReconcileReport struct {
	ParentsAdded    int
	TenantsAdded    int
	PartitionsAdded int
	Anomalies       []string
}
