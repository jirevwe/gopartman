// Package registry manages the lifecycle of parents and tenants in the
// partman metadata schema. ADR-0005 fills in the interface; this file
// declares the seam.
package registry

// Registry is the interface for parent and tenant lifecycle
// operations. It is intentionally empty at the ADR-0001 stage.
type Registry any
