// Package retry runs a function with backoff on transient PostgreSQL
// errors. ADR-0010 defines the policy and the retriable set:
//   - serialization failure (40001)
//   - deadlock detected (40P01)
//   - connection exception family (08xxx)
//   - net.Error where Timeout() is true
//
// Never retries context.Canceled, context.DeadlineExceeded, or any
// wrapped go_partman sentinel. Callers put retry.Do outside the
// database transaction; a PostgreSQL transaction is aborted once the
// first serialization/deadlock error fires and must be restarted.
package retry

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jirevwe/go_partman/internal/errs"
)

// Meter is the minimum observability surface retry needs. The root
// partman.Meter satisfies it structurally. A nil Meter disables
// metric emission.
type Meter interface {
	Counter(name string, delta int64, tags ...string)
	Histogram(name string, value float64, tags ...string)
}

// Policy configures one Do call. MaxAttempts of 0 or 1 disables retry;
// the caller still gets one attempt.
type Policy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Jitter      float64
	// Meter is optional. When non-nil, Do emits partman.retry_attempts_total
	// on every retry and partman.retry_exhausted_total when the last attempt
	// still fails with a retriable error.
	Meter Meter
	// Op labels the caller for the metric tag `op`. Low cardinality only.
	Op string
}

// Default returns the policy documented in ADR-0010.
func Default() Policy {
	return Policy{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		Jitter:      0.2,
	}
}

// Do runs fn until it returns nil, a non-retriable error, ctx is done,
// or MaxAttempts is reached. On the retry path, Do sleeps for a jittered
// exponential backoff bounded by MaxDelay before the next attempt.
func Do(ctx context.Context, p Policy, fn func() error) error {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	var last error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		last = err
		if !Retriable(err) {
			return err
		}
		if p.Meter != nil {
			p.Meter.Counter("partman.retry_attempts_total", 1, "op", p.Op)
		}
		if attempt == p.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay(p, attempt)):
		}
	}
	if p.Meter != nil {
		p.Meter.Counter("partman.retry_exhausted_total", 1, "op", p.Op)
	}
	return last
}

// Retriable reports whether err is one of the transient PostgreSQL or
// network errors ADR-0010 lists. Callers rarely need this directly; it
// is exported so tests and adapters can share the classifier.
func Retriable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if isPartmanSentinel(err) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgerrcode.SerializationFailure, pgerrcode.DeadlockDetected:
			return true
		}
		if len(pgErr.Code) == 5 && pgErr.Code[:2] == "08" {
			return true
		}
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func isPartmanSentinel(err error) bool {
	for _, s := range partmanSentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

var partmanSentinels = []error{
	errs.ErrParentNotFound,
	errs.ErrTenantNotFound,
	errs.ErrParentAlreadyExists,
	errs.ErrTenantAlreadyExists,
	errs.ErrTargetNotPartitioned,
	errs.ErrColumnMissing,
	errs.ErrParentNotTenanted,
	errs.ErrArchiveSchemaMissing,
	errs.ErrIntervalMismatch,
	errs.ErrLockContention,
	errs.ErrHookVetoed,
	errs.ErrDefaultPartitionMissing,
	errs.ErrInvalidIdentifier,
}

// delay computes the sleep before attempt attempt+1. base * 2^(attempt-1),
// capped at MaxDelay, then a jittered addition of up to Jitter * base.
func delay(p Policy, attempt int) time.Duration {
	if p.BaseDelay <= 0 {
		return 0
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 30 {
		shift = 30
	}
	d := p.BaseDelay << shift
	if p.MaxDelay > 0 && d > p.MaxDelay {
		d = p.MaxDelay
	}
	if p.Jitter > 0 {
		d += time.Duration(rand.Float64() * p.Jitter * float64(d))
	}
	return d
}
