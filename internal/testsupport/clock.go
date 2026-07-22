package testsupport

import (
	"testing"
	"time"

	partman "github.com/jirevwe/gopartman"
)

// clockEpoch is the default starting instant for the simulated clock.
// Tests advance from here via SetTime or AdvanceTime.
var clockEpoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// NewSimulatedClock returns a fresh SimulatedClock rooted at a fixed
// epoch and registers a t.Cleanup that logs the clock's final time.
// The cleanup log helps diagnose tests that drift the clock unexpectedly.
func NewSimulatedClock(t *testing.T) *partman.SimulatedClock {
	t.Helper()
	clock := partman.NewSimulatedClock(clockEpoch)
	t.Cleanup(func() {
		t.Logf("simulated clock final time: %s", clock.Now().Format(time.RFC3339Nano))
	})
	return clock
}
