package go_partman

import "time"

// ParentConfig describes a partitioned parent table to register with
// RegisterParent. The library reads the shape at registration time and
// creates the default partition. The user must have created the parent
// table itself.
type ParentConfig struct {
	SchemaName         string
	TableName          string
	TenantColumn       string
	PartitionBy        string
	PartitionType      string
	PartitionInterval  time.Duration
	Premake            int
	RetentionPeriod    time.Duration
	RetentionSchema    string
	RetentionKeepTable bool
}

// ParentRef identifies a registered parent by (schema, table).
type ParentRef struct {
	SchemaName string
	TableName  string
}

// RemoveOption tunes the behavior of RemoveParent.
type RemoveOption func(*removeOptions)

type removeOptions struct {
	cascadeDrop bool
}

// WithCascadeDrop makes RemoveParent drop child partitions as part of
// removal instead of leaving them in place.
func WithCascadeDrop() RemoveOption {
	return func(o *removeOptions) { o.cascadeDrop = true }
}

// ReconcileReport summarizes what ImportExisting inserted into the
// metadata schema. Anomalies is a list of conditions the operation
// detected but could not resolve.
type ReconcileReport struct {
	ParentsAdded    int
	TenantsAdded    int
	PartitionsAdded int
	Anomalies       []string
}
