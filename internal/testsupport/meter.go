package testsupport

import "sync"

// CaptureMeter records every Counter and Histogram call. Safe for
// concurrent use — the maintenance loop and drain fire metrics from
// multiple goroutines. Tests read the recorded state after work is
// finished.
type CaptureMeter struct {
	mu       sync.Mutex
	counters map[string]int64
	hists    map[string][]float64
}

// NewCaptureMeter returns a ready-to-use CaptureMeter.
func NewCaptureMeter() *CaptureMeter {
	return &CaptureMeter{
		counters: map[string]int64{},
		hists:    map[string][]float64{},
	}
}

func (m *CaptureMeter) Counter(name string, delta int64, tags ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += delta
}

func (m *CaptureMeter) Histogram(name string, value float64, tags ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hists[name] = append(m.hists[name], value)
}

// CounterValue returns the accumulated total for name. Zero if the
// meter never saw it.
func (m *CaptureMeter) CounterValue(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[name]
}

// HistogramCount returns the number of Histogram observations for name.
func (m *CaptureMeter) HistogramCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hists[name])
}

// Fired reports whether the meter saw name at least once, either as a
// Counter or a Histogram.
func (m *CaptureMeter) Fired(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.counters[name]; ok {
		return true
	}
	return len(m.hists[name]) > 0
}
