package gopartman

// Meter is the observability sink for gopartman. Implementations must
// be safe for concurrent use — the maintenance loop calls Counter and
// Histogram from multiple goroutines. ADR-0010 documents the metric
// names and tag conventions. Tags are alternating key, value strings;
// never include tenant_id.
type Meter interface {
	Counter(name string, delta int64, tags ...string)
	Histogram(name string, value float64, tags ...string)
}

// NoopMeter is a Meter that records nothing. It is the default value
// used when WithMeter is not supplied to New. Zero cost at the call
// site because the calls compile to nothing.
type NoopMeter struct{}

func (NoopMeter) Counter(name string, delta int64, tags ...string)     {}
func (NoopMeter) Histogram(name string, value float64, tags ...string) {}
