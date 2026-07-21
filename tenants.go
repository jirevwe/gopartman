package go_partman

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
