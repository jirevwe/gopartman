package retry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jirevwe/gopartman/internal/errs"
)

func fastPolicy() Policy {
	return Policy{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    2 * time.Millisecond,
		Jitter:      0,
	}
}

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	calls := int32(0)
	err := Do(context.Background(), fastPolicy(), func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_RetriesSerializationFailure(t *testing.T) {
	calls := int32(0)
	err := Do(context.Background(), fastPolicy(), func() error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &pgconn.PgError{Code: pgerrcode.SerializationFailure}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil after retry, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestDo_ExhaustsOnRepeatedDeadlock(t *testing.T) {
	m := &captureMeter{}
	p := fastPolicy()
	p.Meter = m
	p.Op = "test-op"

	calls := int32(0)
	deadlock := &pgconn.PgError{Code: pgerrcode.DeadlockDetected}
	err := Do(context.Background(), p, func() error {
		atomic.AddInt32(&calls, 1)
		return deadlock
	})
	if !errors.Is(err, deadlock) {
		t.Fatalf("expected wrapped deadlock, got %v", err)
	}
	if calls != int32(p.MaxAttempts) {
		t.Fatalf("expected %d calls, got %d", p.MaxAttempts, calls)
	}
	if m.count("partman.retry_exhausted_total") != 1 {
		t.Fatalf("expected exhausted metric to fire once, got %d", m.count("partman.retry_exhausted_total"))
	}
	if m.count("partman.retry_attempts_total") != int64(p.MaxAttempts) {
		t.Fatalf("expected %d retry_attempts_total, got %d", p.MaxAttempts, m.count("partman.retry_attempts_total"))
	}
}

func TestDo_NoRetryOnConstraintViolation(t *testing.T) {
	calls := int32(0)
	unique := &pgconn.PgError{Code: pgerrcode.UniqueViolation}
	err := Do(context.Background(), fastPolicy(), func() error {
		atomic.AddInt32(&calls, 1)
		return unique
	})
	if !errors.Is(err, unique) {
		t.Fatalf("expected unique violation, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_NoRetryOnContextCanceled(t *testing.T) {
	calls := int32(0)
	err := Do(context.Background(), fastPolicy(), func() error {
		atomic.AddInt32(&calls, 1)
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_NoRetryOnWrappedSentinel(t *testing.T) {
	calls := int32(0)
	wrapped := fmt.Errorf("%w: extra", errs.ErrParentNotFound)
	err := Do(context.Background(), fastPolicy(), func() error {
		atomic.AddInt32(&calls, 1)
		return wrapped
	})
	if !errors.Is(err, errs.ErrParentNotFound) {
		t.Fatalf("expected sentinel, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_RetriesConnection08Family(t *testing.T) {
	calls := int32(0)
	err := Do(context.Background(), fastPolicy(), func() error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &pgconn.PgError{Code: "08006"} // connection failure
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestDo_RetriesNetTimeout(t *testing.T) {
	calls := int32(0)
	err := Do(context.Background(), fastPolicy(), func() error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return timeoutErr{}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestDo_ContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	deadlock := &pgconn.PgError{Code: pgerrcode.DeadlockDetected}
	p := Policy{MaxAttempts: 5, BaseDelay: 50 * time.Millisecond, MaxDelay: 100 * time.Millisecond}

	calls := int32(0)
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := Do(ctx, p, func() error {
		atomic.AddInt32(&calls, 1)
		return deadlock
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestDefault_HasADRValues(t *testing.T) {
	d := Default()
	if d.MaxAttempts != 5 {
		t.Fatalf("MaxAttempts want 5, got %d", d.MaxAttempts)
	}
	if d.BaseDelay != 100*time.Millisecond {
		t.Fatalf("BaseDelay want 100ms, got %s", d.BaseDelay)
	}
	if d.MaxDelay != 2*time.Second {
		t.Fatalf("MaxDelay want 2s, got %s", d.MaxDelay)
	}
	if d.Jitter != 0.2 {
		t.Fatalf("Jitter want 0.2, got %f", d.Jitter)
	}
}

// timeoutErr satisfies net.Error with Timeout()=true.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

type captureMeter struct {
	counters map[string]int64
	hists    map[string][]float64
}

func (m *captureMeter) Counter(name string, delta int64, tags ...string) {
	if m.counters == nil {
		m.counters = map[string]int64{}
	}
	m.counters[name] += delta
}

func (m *captureMeter) Histogram(name string, value float64, tags ...string) {
	if m.hists == nil {
		m.hists = map[string][]float64{}
	}
	m.hists[name] = append(m.hists[name], value)
}

func (m *captureMeter) count(name string) int64 { return m.counters[name] }
