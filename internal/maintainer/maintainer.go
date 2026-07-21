// Package maintainer runs the periodic maintenance loop and holds the
// per-parent advisory lock. ADR-0007 fills in the interface; this
// file declares the seam.
package maintainer

// Maintainer is the interface for the scheduler + advisory lock loop.
// It is intentionally empty at the ADR-0001 stage.
type Maintainer any
