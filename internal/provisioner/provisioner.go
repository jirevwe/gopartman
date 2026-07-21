// Package provisioner creates partitions and the default partition
// under a registered parent. ADR-0004 fills in the interface; this
// file declares the seam.
package provisioner

// Provisioner is the interface for creating child partitions. It is
// intentionally empty at the ADR-0001 stage.
type Provisioner any
