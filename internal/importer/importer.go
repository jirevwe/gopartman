package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"

	"github.com/jirevwe/gopartman/internal/errs"
	"github.com/jirevwe/gopartman/internal/hooks"
	"github.com/jirevwe/gopartman/internal/naming"
	parentsrepo "github.com/jirevwe/gopartman/internal/parents/repo"
	partitionsrepo "github.com/jirevwe/gopartman/internal/partitions/repo"
	tenantsrepo "github.com/jirevwe/gopartman/internal/tenants/repo"
)

// Importer is the seam through which Manager calls one-shot import.
type Importer interface {
	Import(ctx context.Context, ref ParentRef) (ReconcileReport, error)
}

// Config bundles the dependencies for constructing an Impl.
type Config struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// Impl is the concrete Importer. Exported so the Manager facade can
// hold a typed field.
type Impl struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	// childLister is a seam for unit tests. Production code always uses
	// listChildrenFromPG; tests inject a fake.
	childLister childLister
}

// New constructs an Impl. Pool is required; Logger defaults to
// slog.Default().
func New(cfg Config) (*Impl, error) {
	if cfg.Pool == nil {
		return nil, errors.New("importer: Config.Pool is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	i := &Impl{pool: cfg.Pool, logger: logger}
	i.childLister = listChildrenFromPG(cfg.Pool)
	return i, nil
}

// pgChild is one row of pg_inherits for the target parent.
type pgChild struct {
	SchemaName string
	Relname    string
	BoundExpr  string
}

func (c pgChild) fq() string { return c.SchemaName + "." + c.Relname }

// childLister returns every child of a parent along with the raw
// pg_get_expr string. Extracted so tests can inject a fake without a
// real Postgres.
type childLister func(ctx context.Context, parentSchema, parentTable string) ([]pgChild, error)

func listChildrenFromPG(pool *pgxpool.Pool) childLister {
	return func(ctx context.Context, parentSchema, parentTable string) ([]pgChild, error) {
		fq := parentSchema + "." + parentTable
		rows, err := pool.Query(ctx, `
			SELECT n.nspname, c.relname,
			       pg_get_expr(c.relpartbound, c.oid) AS bound_expr
			FROM pg_inherits i
			JOIN pg_class c ON c.oid = i.inhrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE i.inhparent = to_regclass($1)
			ORDER BY c.relname
		`, fq)
		if err != nil {
			return nil, fmt.Errorf("importer: list children of %s: %w", fq, err)
		}
		defer rows.Close()
		var out []pgChild
		for rows.Next() {
			var c pgChild
			if err := rows.Scan(&c.SchemaName, &c.Relname, &c.BoundExpr); err != nil {
				return nil, fmt.Errorf("importer: scan child: %w", err)
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("importer: iterate children: %w", err)
		}
		return out, nil
	}
}

// Import reconciles PG state into partman metadata for one parent. See
// ADR-0008 for the full contract.
func (i *Impl) Import(ctx context.Context, ref ParentRef) (ReconcileReport, error) {
	parentsQ := parentsrepo.New(i.pool)
	prow, err := parentsQ.GetParentTable(ctx, parentsrepo.GetParentTableParams{
		SchemaName: ref.SchemaName,
		TableName:  ref.TableName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReconcileReport{}, fmt.Errorf("%w: %s.%s", errs.ErrParentNotFound, ref.SchemaName, ref.TableName)
		}
		return ReconcileReport{}, fmt.Errorf("importer: load parent: %w", err)
	}

	kind, err := intervalKindFromLabel(prow.PartitionInterval)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("importer: parent %s.%s: %w", ref.SchemaName, ref.TableName, err)
	}

	children, err := i.childLister(ctx, ref.SchemaName, ref.TableName)
	if err != nil {
		return ReconcileReport{}, err
	}

	// Pre-flight: bail before any write when interval disagrees.
	for _, c := range children {
		pb, err := parseBoundExpr(c.BoundExpr)
		if err != nil {
			continue // will be recorded as Skipped in the main pass
		}
		if pb.IsDefault {
			continue
		}
		if !intervalMatches(kind, pb.Bounds) {
			return ReconcileReport{}, fmt.Errorf(
				"%w: child %s has bound %s but parent %s.%s is %s",
				errs.ErrIntervalMismatch, c.fq(), c.BoundExpr,
				ref.SchemaName, ref.TableName, prow.PartitionInterval,
			)
		}
	}

	partitionsQ := partitionsrepo.New(i.pool)
	existing, err := partitionsQ.ListPartitionsForParent(ctx, prow.ID)
	if err != nil {
		return ReconcileReport{}, fmt.Errorf("importer: list metadata: %w", err)
	}
	existingByName := make(map[string]partitionsrepo.PartmanPartition, len(existing))
	for _, ep := range existing {
		existingByName[ep.Name] = ep
	}

	tenantsQ := tenantsrepo.New(i.pool)
	knownTenants, err := loadKnownTenants(ctx, tenantsQ, prow.ID)
	if err != nil {
		return ReconcileReport{}, err
	}

	report := ReconcileReport{}
	seen := make(map[string]struct{}, len(children))

	for _, c := range children {
		fq := c.fq()
		seen[fq] = struct{}{}

		nameParsed, err := (naming.TableName{}).Parse(fq)
		if err != nil {
			report.Skipped = append(report.Skipped, SkippedPartition{
				Name:   fq,
				Reason: "non-conforming name: " + err.Error(),
			})
			continue
		}

		boundParsed, err := parseBoundExpr(c.BoundExpr)
		if err != nil {
			report.Skipped = append(report.Skipped, SkippedPartition{
				Name:   fq,
				Reason: "unrecognized bound expression: " + err.Error(),
			})
			continue
		}

		if reason := compareNameAndBound(nameParsed, boundParsed); reason != "" {
			report.Drifted = append(report.Drifted, DriftedPartition{
				Name:        fq,
				NameBounds:  nameParsed.Bounds,
				ActualBound: c.BoundExpr,
				Reason:      reason,
			})
			continue
		}

		// Auto-register the tenant if the partition carries one. The
		// schema's tenant trigger enforces this ordering.
		if boundParsed.TenantId != "" {
			if _, seenTenant := knownTenants[boundParsed.TenantId]; !seenTenant {
				if _, err := tenantsQ.UpsertTenant(ctx, tenantsrepo.UpsertTenantParams{
					ID:            boundParsed.TenantId,
					ParentTableID: prow.ID,
				}); err != nil {
					return ReconcileReport{}, fmt.Errorf(
						"importer: auto-register tenant %q for %s: %w",
						boundParsed.TenantId, fq, err,
					)
				}
				knownTenants[boundParsed.TenantId] = struct{}{}
				i.logger.Info("importer: auto-registered tenant during import",
					"tenant", boundParsed.TenantId,
					"parent_schema", ref.SchemaName,
					"parent_table", ref.TableName,
				)
			}
		}

		if _, already := existingByName[fq]; already {
			// Metadata row already exists. Idempotent no-op; not
			// reported in Imported per ADR-0008 acceptance criterion.
			continue
		}

		if err := insertMetadata(ctx, partitionsQ, prow, fq, boundParsed); err != nil {
			return ReconcileReport{}, err
		}

		report.Imported = append(report.Imported, buildRef(nameParsed, boundParsed))
	}

	// Orphan detection: metadata rows whose name did not appear in the
	// pg_inherits scan.
	for _, ep := range existing {
		if _, ok := seen[ep.Name]; ok {
			continue
		}
		ref, err := refFromMetadata(ep)
		if err != nil {
			// Cannot construct a full ref; still surface the raw name.
			ref = hooks.PartitionRef{Schema: "", Parent: ep.Name}
		}
		report.Orphaned = append(report.Orphaned, ref)
	}

	return report, nil
}

// loadKnownTenants returns the set of tenant IDs already registered for
// the parent so auto-registration can skip repeats.
func loadKnownTenants(ctx context.Context, q *tenantsrepo.Queries, parentID string) (map[string]struct{}, error) {
	rows, err := q.ListTenantsForParent(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("importer: list tenants: %w", err)
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[r.ID] = struct{}{}
	}
	return out, nil
}

// compareNameAndBound returns a non-empty reason when the child's NAME
// disagrees with pg_get_expr. Empty reason means aligned.
func compareNameAndBound(name naming.TableName, bound parsedBound) string {
	if name.IsDefault != bound.IsDefault {
		if name.IsDefault {
			return "name says DEFAULT but bound is a range"
		}
		return "name says range but bound is DEFAULT"
	}
	if name.IsDefault {
		return ""
	}
	if strings.ToUpper(name.TenantId) != bound.TenantId {
		return fmt.Sprintf("tenant mismatch: name=%q, bound=%q", name.TenantId, bound.TenantId)
	}
	if !name.Bounds.From.Equal(bound.Bounds.From.UTC()) {
		return fmt.Sprintf(
			"bounds.From mismatch: name=%s, bound=%s",
			name.Bounds.From.UTC().Format("2006-01-02"),
			bound.Bounds.From.UTC().Format("2006-01-02"),
		)
	}
	return ""
}

// insertMetadata writes one row into partman.partitions using the same
// conventions Provisioner uses: epoch bounds for the default; parsed
// bounds otherwise.
func insertMetadata(
	ctx context.Context,
	q *partitionsrepo.Queries,
	prow parentsrepo.PartmanParentTable,
	fq string,
	bound parsedBound,
) error {
	var params partitionsrepo.UpsertPartitionParams
	params.ID = ulid.Make().String()
	params.Name = fq
	params.ParentTableID = prow.ID
	params.PartitionBy = prow.PartitionBy
	params.PartitionType = prow.PartitionType

	if bound.IsDefault {
		params.IsDefault = true
		params.TenantID = pgtype.Text{}
		params.PartitionBoundsFrom = pgtype.Timestamptz{Valid: true} // zero time; sentinel
		params.PartitionBoundsTo = pgtype.Timestamptz{Valid: true}
	} else {
		params.IsDefault = false
		if bound.TenantId != "" {
			params.TenantID = pgtype.Text{String: bound.TenantId, Valid: true}
		}
		params.PartitionBoundsFrom = pgtype.Timestamptz{Time: bound.Bounds.From, Valid: true}
		params.PartitionBoundsTo = pgtype.Timestamptz{Time: bound.Bounds.To, Valid: true}
	}

	if err := q.UpsertPartition(ctx, params); err != nil {
		return fmt.Errorf("importer: upsert metadata for %s: %w", fq, err)
	}
	return nil
}

// buildRef assembles a hooks.PartitionRef from a parsed name and the
// PG-side bound. The bound's To is authoritative (name only carries
// From); tenant is upper-cased.
func buildRef(name naming.TableName, bound parsedBound) hooks.PartitionRef {
	ref := hooks.PartitionRef{
		Schema:    name.SchemaName,
		Parent:    name.ParentName,
		TenantId:  bound.TenantId,
		IsDefault: bound.IsDefault,
	}
	if !bound.IsDefault {
		ref.Bounds = bound.Bounds
	}
	return ref
}

// refFromMetadata reconstructs a hooks.PartitionRef from a stored
// partman.partitions row. Used for the Orphaned list, which has no PG
// child to consult.
func refFromMetadata(ep partitionsrepo.PartmanPartition) (hooks.PartitionRef, error) {
	tn, err := (naming.TableName{}).Parse(ep.Name)
	if err != nil {
		return hooks.PartitionRef{}, err
	}
	ref := hooks.PartitionRef{
		Schema:    tn.SchemaName,
		Parent:    tn.ParentName,
		IsDefault: ep.IsDefault,
	}
	if ep.TenantID.Valid {
		ref.TenantId = ep.TenantID.String
	}
	if !ep.IsDefault {
		ref.Bounds = naming.Bounds{
			From: ep.PartitionBoundsFrom.Time,
			To:   ep.PartitionBoundsTo.Time,
		}
	}
	return ref, nil
}
