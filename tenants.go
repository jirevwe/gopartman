package gopartman

import "github.com/jirevwe/gopartman/internal/registry"

// TenantConfig describes a tenant to register under a parent that has
// a TenantColumn. Aliased from internal/registry.
type TenantConfig = registry.TenantConfig

// TenantRef identifies a registered tenant under a parent. Aliased
// from internal/registry.
type TenantRef = registry.TenantRef

// TenantInfo is the read-only view returned by Manager.ListTenants.
type TenantInfo = registry.TenantInfo
