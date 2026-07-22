package maintainer

import (
	"context"
	"errors"
	"sync"
	"time"
)

// scheduler owns the one goroutine that ticks and calls Maintain. It
// is not exported; the Impl embeds it and forwards Start/Stop.
type scheduler struct {
	m *Impl

	mu      sync.Mutex
	running bool
	done    chan struct{}
	wg      sync.WaitGroup
}

func newScheduler(m *Impl) *scheduler {
	return &scheduler{m: m}
}

var errAlreadyStarted = errors.New("maintainer: already started")

// start spins the goroutine that ticks on m.schedule and calls
// Maintain on each tick. A second start while the loop is running
// returns errAlreadyStarted.
func (s *scheduler) start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errAlreadyStarted
	}
	s.running = true
	s.done = make(chan struct{})
	s.wg.Add(1)
	s.mu.Unlock()

	go s.loop(ctx)
	return nil
}

// stop closes done and waits for the goroutine to exit. When ctx
// expires before the goroutine returns, stop returns ctx.Err() and
// leaves the goroutine to clean up on its own.
func (s *scheduler) stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	close(s.done)
	done := s.done
	s.mu.Unlock()
	_ = done // keep local reference; the goroutine holds its own

	waitCh := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// loop is the ticker goroutine. It runs one Maintain per tick and
// exits when done closes. It uses time.Ticker so if a Maintain runs
// longer than the interval, intervening ticks drop.
func (s *scheduler) loop(ctx context.Context) {
	defer s.wg.Done()

	s.mu.Lock()
	done := s.done
	s.mu.Unlock()

	ticker := time.NewTicker(s.m.schedule)
	defer ticker.Stop()

	var last time.Time
	for {
		select {
		case <-done:
			s.m.logger.Info("maintainer: loop exiting")
			return
		case t := <-ticker.C:
			if !last.IsZero() {
				elapsed := t.Sub(last)
				if elapsed >= 2*s.m.schedule {
					s.m.logger.Warn("maintainer: tick dropped; Maintain slower than schedule",
						"elapsed_ms", elapsed.Milliseconds(),
						"schedule_ms", s.m.schedule.Milliseconds(),
					)
				}
			}
			last = t
			if err := s.m.Maintain(ctx); err != nil {
				s.m.logger.Warn("maintainer: Maintain returned error",
					"err", err,
				)
			}
		}
	}
}
