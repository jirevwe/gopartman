// Package drain moves rows out of a parent's default partition and
// into the correct bounded child partitions. ADR-0009 defines the
// contract. The drain holds a per-parent advisory lock for the full
// call so concurrent Maintainer or peer Drain sessions skip the parent.
package drain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jirevwe/go_partman/internal/maintainer"
	"github.com/jirevwe/go_partman/internal/naming"
	parentsrepo "github.com/jirevwe/go_partman/internal/parents/repo"
	partitionsrepo "github.com/jirevwe/go_partman/internal/partitions/repo"
)

// ErrParentBusy is returned when the advisory lock for the parent is
// already held by another session (Maintainer or peer Drain). Callers
// can retry after a backoff.
var ErrParentBusy = errors.New("drain: parent is busy")

// ErrParentNotFound is returned when the parent is not registered in
// the metadata schema.
var ErrParentNotFound = errors.New("drain: parent not found")

// ParentRef identifies a registered parent by (schema, table). Mirrors
// partman.ParentRef; the drain package keeps a local copy to avoid a
// circular import.
type ParentRef struct {
	SchemaName string
	TableName  string
}

// Anomaly is one condition the drain could not fix. A zero-value
// MissingPartitionBounds signals "control column was NULL" for the
// tenant; otherwise it names the target bounds that had no partition.
type Anomaly struct {
	MissingPartitionBounds naming.Bounds
	TenantId               string
	RowCount               int
}

// Report summarizes one PartitionData call.
type Report struct {
	RowsMoved  int
	BatchesRun int
	Anomalies  []Anomaly
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// Impl is the concrete drain. Exported so the Manager facade can hold a
// typed field.
type Impl struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New constructs an Impl. Pool is required; Logger defaults to
// slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Pool == nil {
		return nil, errors.New("drain: Config.Pool is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Impl{pool: cfg.Pool, logger: logger}, nil
}

// PartitionData drains rows from the parent's default partition into
// the correct bounded partitions, in batches. See ADR-0009 for the
// semantics.
//
// The advisory lock is held for the whole call. When the lock is not
// obtainable, PartitionData returns ErrParentBusy.
//
// Rows whose control column is NULL are recorded as one anomaly per
// tenant at the end (Bounds zero-value signals NULL). Rows whose target
// bounded partition does not exist are recorded as one anomaly per
// (tenant, bounds) group.
func (d *Impl) PartitionData(ctx context.Context, ref ParentRef, opts Options) (Report, error) {
	o := opts.resolved()

	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("drain: acquire conn: %w", err)
	}
	defer conn.Release()

	locked, err := maintainer.TryLock(ctx, conn, ref.SchemaName, ref.TableName)
	if err != nil {
		return Report{}, fmt.Errorf("drain: try lock: %w", err)
	}
	if !locked {
		return Report{}, ErrParentBusy
	}
	defer func() {
		if err := maintainer.Unlock(ctx, conn, ref.SchemaName, ref.TableName); err != nil {
			d.logger.Warn("drain: unlock failed",
				"parent", ref.SchemaName+"."+ref.TableName,
				"err", err,
			)
		}
	}()

	parents := parentsrepo.New(conn)
	prow, err := parents.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: ref.SchemaName,
		TableName:  ref.TableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Report{}, fmt.Errorf("%w: %s.%s", ErrParentNotFound, ref.SchemaName, ref.TableName)
		}
		return Report{}, fmt.Errorf("drain: load parent: %w", err)
	}

	interval, err := intervalFromLabel(prow.PartitionInterval)
	if err != nil {
		return Report{}, fmt.Errorf("drain: %w", err)
	}
	tenantCol := ""
	if prow.TenantColumn.Valid {
		tenantCol = prow.TenantColumn.String
	}
	defaultTable := ref.TableName + "_default"

	cols, err := lookupInsertColumns(ctx, conn, ref.SchemaName, ref.TableName)
	if err != nil {
		return Report{}, fmt.Errorf("drain: lookup columns: %w", err)
	}
	if len(cols) == 0 {
		return Report{}, fmt.Errorf("drain: parent %s.%s has no non-generated columns", ref.SchemaName, ref.TableName)
	}

	partitions := partitionsrepo.New(conn)
	anomalies := newAnomalyTracker()
	report := Report{}

	for batchIdx := 0; o.MaxBatches == 0 || batchIdx < o.MaxBatches; batchIdx++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		rq := buildReadBatch(readParams{
			Schema:       ref.SchemaName,
			DefaultTable: defaultTable,
			ControlCol:   prow.PartitionBy,
			TenantCol:    tenantCol,
			Tenant:       o.Tenant,
			AnomalyKeys:  anomalies.keys(),
			BatchSize:    o.BatchSize,
		})

		rows, err := readBatch(ctx, conn, rq, tenantCol != "")
		if err != nil {
			return report, fmt.Errorf("drain: read batch: %w", err)
		}
		if len(rows) == 0 {
			break
		}

		report.BatchesRun++

		groups, err := groupByBounds(rows, interval)
		if err != nil {
			return report, fmt.Errorf("drain: group rows: %w", err)
		}

		for key, ctids := range groups {
			target, err := partitions.FindActivePartitionByBounds(ctx, partitionsrepo.FindActivePartitionByBoundsParams{
				ParentTableID: prow.ID,
				TenantID:      pgTenantID(key),
				BoundsFrom:    pgtype.Timestamptz{Time: key.Bounds.From, Valid: true},
				BoundsTo:      pgtype.Timestamptz{Time: key.Bounds.To, Valid: true},
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					anomalies.record(key, len(ctids))
					d.logger.Info("drain: target partition missing; recording anomaly",
						"parent", ref.SchemaName+"."+ref.TableName,
						"tenant", key.Tenant,
						"bounds_from", key.Bounds.From,
						"bounds_to", key.Bounds.To,
						"row_count", len(ctids),
					)
					continue
				}
				return report, fmt.Errorf("drain: lookup target: %w", err)
			}

			moved, err := moveGroup(ctx, conn, ref.SchemaName, defaultTable, target.Name, cols, ctids)
			if err != nil {
				return report, fmt.Errorf("drain: move group: %w", err)
			}
			report.RowsMoved += moved
		}
	}

	if err := appendNullAnomalies(ctx, conn, ref.SchemaName, defaultTable, prow.PartitionBy, tenantCol, o.Tenant, &report); err != nil {
		return report, fmt.Errorf("drain: null summary: %w", err)
	}

	report.Anomalies = append(report.Anomalies, anomalies.list()...)

	d.logger.Info("drain: complete",
		"parent", ref.SchemaName+"."+ref.TableName,
		"batches_run", report.BatchesRun,
		"rows_moved", report.RowsMoved,
		"anomaly_count", len(report.Anomalies),
	)
	// TODO(ADR-0010): emit partman.drain_rows_moved_total,
	// partman.drain_batches_total, partman.drain_anomalies_total,
	// partman.drain_duration_seconds.
	return report, nil
}

// readBatch executes rq and materializes the rows. When tenantExists is
// false the query returns two columns (ctid, control); otherwise three.
func readBatch(ctx context.Context, conn *pgxpool.Conn, rq readBatchQuery, tenantExists bool) ([]batchRow, error) {
	rows, err := conn.Query(ctx, rq.SQL, rq.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []batchRow
	for rows.Next() {
		var r batchRow
		var ctrl pgtype.Timestamptz
		if tenantExists {
			var tenant pgtype.Text
			if err := rows.Scan(&r.CTID, &ctrl, &tenant); err != nil {
				return nil, err
			}
			r.TenantOK = tenant.Valid
			if tenant.Valid {
				r.Tenant = tenant.String
			}
		} else {
			if err := rows.Scan(&r.CTID, &ctrl); err != nil {
				return nil, err
			}
		}
		r.Control = ctrl.Time.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// moveGroup runs one per-group transaction: DELETE ... RETURNING inside
// a CTE, INSERT ... SELECT of the returned rows into the target. Uses
// the same connection that holds the advisory lock so the lock stays
// session-scoped across all groups.
func moveGroup(ctx context.Context, conn *pgxpool.Conn, schema, defaultTable, targetFQ string, cols []string, ctids []pgtype.TID) (int, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	sql := buildMoveCTE(schema, defaultTable, targetFQ, cols)
	tag, err := tx.Exec(ctx, sql, ctids)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	committed = true
	return int(tag.RowsAffected()), nil
}

// lookupInsertColumns queries information_schema.columns for the parent
// and returns the non-generated column names in ordinal order.
func lookupInsertColumns(ctx context.Context, conn *pgxpool.Conn, schema, table string) ([]string, error) {
	rows, err := conn.Query(ctx, insertColumnsSQL(), schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// appendNullAnomalies runs the terminal NULL-control-column summary and
// appends one anomaly per tenant to report.
func appendNullAnomalies(ctx context.Context, conn *pgxpool.Conn, schema, defaultTable, controlCol, tenantCol string, tenant *string, report *Report) error {
	sql, args := buildNullSummary(schema, defaultTable, controlCol, tenantCol, tenant)
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var count int64
		if tenantCol == "" {
			if err := rows.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			report.Anomalies = append(report.Anomalies, Anomaly{RowCount: int(count)})
			continue
		}
		var t pgtype.Text
		if err := rows.Scan(&t, &count); err != nil {
			return err
		}
		if count == 0 {
			continue
		}
		a := Anomaly{RowCount: int(count)}
		if t.Valid {
			a.TenantId = t.String
		}
		report.Anomalies = append(report.Anomalies, a)
	}
	return rows.Err()
}

// pgTenantID converts a groupKey's tenant into the pgtype.Text bound to
// the FindActivePartitionByBounds param. An unset tenant becomes SQL
// NULL, which lines up with the query's IS NOT DISTINCT FROM clause.
func pgTenantID(key groupKey) pgtype.Text {
	if !key.TenantOK || key.Tenant == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: key.Tenant, Valid: true}
}

// intervalFromLabel maps a stored parent_interval label back to the
// canonical time.Duration constant. Symmetric with
// naming.PartitionIntervalLabel.
func intervalFromLabel(label string) (time.Duration, error) {
	switch label {
	case "hourly":
		return naming.PartitionHourInterval, nil
	case "daily":
		return naming.PartitionDayInterval, nil
	case "weekly":
		return naming.PartitionWeekInterval, nil
	case "monthly":
		return naming.PartitionMonthInterval, nil
	default:
		return 0, fmt.Errorf("unknown partition_interval label %q", label)
	}
}

// anomalyTracker accumulates missing-partition anomalies across the
// batch loop. Its keys go into the read query's exclusion clause so
// subsequent batches skip the same rows.
type anomalyTracker struct {
	byKey map[groupKey]int
	order []groupKey
}

func newAnomalyTracker() *anomalyTracker {
	return &anomalyTracker{byKey: map[groupKey]int{}}
}

func (a *anomalyTracker) record(k groupKey, n int) {
	if _, ok := a.byKey[k]; !ok {
		a.order = append(a.order, k)
	}
	a.byKey[k] += n
}

func (a *anomalyTracker) keys() []groupKey {
	return a.order
}

func (a *anomalyTracker) list() []Anomaly {
	out := make([]Anomaly, 0, len(a.order))
	for _, k := range a.order {
		out = append(out, Anomaly{
			MissingPartitionBounds: k.Bounds,
			TenantId:               k.Tenant,
			RowCount:               a.byKey[k],
		})
	}
	return out
}
