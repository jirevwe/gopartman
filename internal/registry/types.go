package registry

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
	// DisableAutomaticMaintenance opts the parent out of the Maintainer
	// loop (ADR-0007). Zero value keeps maintenance enabled, matching
	// the ADR-0005 "default true" contract without a pointer field.
	DisableAutomaticMaintenance bool
}

// ParentRef identifies a registered parent by (schema, table).
type ParentRef struct {
	SchemaName string
	TableName  string
}

// ParentInfo is the read-only view returned by ListParents. Fields
// mirror the columns of partman.parent_tables that are useful to
// external callers.
type ParentInfo struct {
	SchemaName           string
	TableName            string
	TenantColumn         string
	PartitionBy          string
	PartitionType        string
	PartitionInterval    string
	Premake              int
	RetentionPeriod      time.Duration
	RetentionSchema      string
	RetentionKeepTable   bool
	AutomaticMaintenance bool
}

// TenantConfig describes a tenant to register under a parent that has
// a TenantColumn.
type TenantConfig struct {
	ParentSchema string
	ParentName   string
	TenantId     string
}

// TenantRef identifies a registered tenant under a parent.
type TenantRef struct {
	ParentSchema string
	ParentName   string
	TenantId     string
}

// TenantInfo is the read-only view returned by ListTenants.
type TenantInfo struct {
	ParentSchema string
	ParentName   string
	TenantId     string
}

// RemoveOption tunes the behavior of RemoveParent.
type RemoveOption func(*removeOptions)

type removeOptions struct {
	cascadeDrop bool
}

// WithCascadeDrop makes RemoveParent drop child partitions as part of
// removal instead of leaving them in place. Requires Retention
// (ADR-0006) to be wired; otherwise RemoveParent returns an error.
func WithCascadeDrop() RemoveOption {
	return func(o *removeOptions) { o.cascadeDrop = true }
}

func evalRemoveOptions(opts []RemoveOption) removeOptions {
	var o removeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
