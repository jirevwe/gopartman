package go_partman

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Option configures a Manager during construction. Options are pure
// setters; validation happens in New.
type Option func(*Manager) error

const defaultScheduleInterval = time.Hour

// New constructs a Manager. WithDB and WithClock are required. New
// returns an error naming the missing option when either is absent.
func New(opts ...Option) (*Manager, error) {
	m := &Manager{
		logger:   slog.Default(),
		schedule: defaultScheduleInterval,
		meter:    NoopMeter{},
	}
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}
	if m.db == nil {
		return nil, fmt.Errorf("go_partman: WithDB is required")
	}
	if m.clock == nil {
		return nil, fmt.Errorf("go_partman: WithClock is required")
	}
	if err := m.initInternals(); err != nil {
		return nil, err
	}
	return m, nil
}

// WithDB supplies the pgx pool. Required.
func WithDB(db *pgxpool.Pool) Option {
	return func(m *Manager) error {
		m.db = db
		return nil
	}
}

// WithClock supplies the Clock. Required. Use NewRealClock() in
// production; NewSimulatedClock in tests.
func WithClock(c Clock) Option {
	return func(m *Manager) error {
		m.clock = c
		return nil
	}
}

// WithLogger supplies the slog.Logger. Optional. Defaults to
// slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(m *Manager) error {
		m.logger = l
		return nil
	}
}

// WithHook installs the global pre-drop hook. Optional. A nil hook
// (the default) is equivalent to always returning HookDrop.
func WithHook(h Hook) Option {
	return func(m *Manager) error {
		m.hook = h
		return nil
	}
}

// WithScheduleInterval sets the maintenance tick interval. Optional.
// Defaults to 1 hour.
func WithScheduleInterval(d time.Duration) Option {
	return func(m *Manager) error {
		m.schedule = d
		return nil
	}
}

// WithMeter installs the observability sink. Optional. Defaults to
// NoopMeter{}.
func WithMeter(mtr Meter) Option {
	return func(m *Manager) error {
		m.meter = mtr
		return nil
	}
}
