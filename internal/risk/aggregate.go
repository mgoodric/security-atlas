// Manual risk aggregation for slice 053 (canvas §6.4 manual rollup path,
// §6.6 severity functions).
//
// POST /v1/risks/aggregate accepts:
//
//	{
//	  parent: { title, level, org_unit_id, severity_function },
//	  child_risk_ids: [uuid, ...]
//	}
//
// and:
//
//  1. Validates every child_risk_id exists for the tenant (count-check
//     inside the tenant tx — RLS makes cross-tenant rows invisible; if the
//     count is short of the requested length, return 404 without
//     enumerating which ids were missing).
//  2. Validates every child uses an aggregable methodology
//     ({nist_800_30, qualitative_5x5}). Mixed-methodology aggregation is
//     deferred to slice 054.
//  3. Computes the aggregated severity via the chosen function (max /
//     weighted_max / sum), capped at scale max (25).
//  4. Derives (likelihood, impact) on the 5×5 grid so the parent risk
//     satisfies the qualitative_5x5 schema and the slice-019 heatmap
//     query groups it sensibly.
//  5. Computes the AC-7 idempotency key sha256(title|sorted_child_uuids).
//     If a parent with that key already exists for the tenant, returns it
//     unchanged.
//  6. Unions every child's themes into the parent's themes array.
//  7. Inserts the parent risk and one risk_aggregations row per child
//     (rule_id = NULL for manual).
//
// GET /v1/risks/{id}/aggregation re-aggregates the current children live
// and returns the recomputed severity. AC-9 calls this endpoint after
// deleting a child to verify the parent severity drops while the parent
// row survives. Per canvas §6.4: closing children never auto-closes the
// parent; the stored parent severity is historical (frozen at create
// time), the live recompute is the truth.

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrChildrenNotFound is returned when one or more requested child_risk_ids
// were missing or belonged to another tenant. The error message is
// intentionally NON-enumerating to satisfy AC-10 existence-leak prevention.
var ErrChildrenNotFound = errors.New("risk: one or more child risks not found")

// AggregateInput is the request shape for POST /v1/risks/aggregate.
type AggregateInput struct {
	ParentTitle      string
	ParentLevel      dbx.RiskLevel
	ParentOrgUnitID  *uuid.UUID
	SeverityFunction SeverityFunction
	ChildRiskIDs     []uuid.UUID
}

// AggregateResult bundles the persisted parent and the list of children
// linked. Returned by Aggregate(); the HTTP handler shapes it into JSON.
type AggregateResult struct {
	Parent           Risk
	LinkedChildren   []uuid.UUID
	Severity         int
	Likelihood       int
	Impact           int
	ChildCount       int
	SeverityFunction SeverityFunction
	AggregationKey   string
}

// Aggregate runs the slice 053 manual aggregation pipeline end-to-end.
func (s *Store) Aggregate(ctx context.Context, in AggregateInput) (AggregateResult, error) {
	if in.ParentTitle == "" {
		return AggregateResult{}, fmt.Errorf("aggregate: parent.title is required")
	}
	if len(in.ChildRiskIDs) == 0 {
		return AggregateResult{}, ErrEmptyChildren
	}
	if !isValidLevel(in.ParentLevel) {
		return AggregateResult{}, ErrInvalidLevel
	}
	switch in.SeverityFunction {
	case SeverityFunctionMax, SeverityFunctionWeightedMax, SeverityFunctionSum:
	default:
		return AggregateResult{}, fmt.Errorf("%w: %q", ErrUnknownSeverityFunction, in.SeverityFunction)
	}

	// Dedupe child ids while preserving the caller's set (the AC-7
	// idempotency key normalises via sort, so order doesn't matter; but
	// dedupe keeps the aggregation_key stable when the caller resends
	// with duplicates).
	dedupedChildren := uniqueUUIDs(in.ChildRiskIDs)
	key := AggregationKey(in.ParentTitle, dedupedChildren)

	var result AggregateResult
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		// AC-7 idempotency: if a parent with this key already exists,
		// return it. Re-aggregating the same (title, child_set) is a
		// no-op.
		existing, err := q.GetRiskByAggregationKey(ctx, dbx.GetRiskByAggregationKeyParams{
			TenantID: pgUUID(tenantID),
			Column2:  key,
		})
		if err == nil {
			// Existing parent found — reload its children + return.
			children, lerr := q.ListRiskAggregationChildren(ctx, dbx.ListRiskAggregationChildrenParams{
				TenantID:     pgUUID(tenantID),
				ParentRiskID: existing.ID,
			})
			if lerr != nil {
				return fmt.Errorf("reload existing aggregation children: %w", lerr)
			}
			linked := make([]uuid.UUID, len(children))
			for i, c := range children {
				linked[i] = uuid.UUID(c.ChildRiskID.Bytes)
			}
			parent := riskFromRow(existing)
			parent.LinkedControlIDs = []uuid.UUID{} // parents never link controls in v1
			meta := extractAggregationMeta(existing.InherentScore)
			result = AggregateResult{
				Parent:           parent,
				LinkedChildren:   linked,
				Severity:         meta.Severity,
				Likelihood:       meta.Likelihood,
				Impact:           meta.Impact,
				ChildCount:       len(linked),
				SeverityFunction: meta.SeverityFunction,
				AggregationKey:   key,
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("aggregation key lookup: %w", err)
		}

		// AC-10 cross-tenant denial: read children inside the tenant
		// tx. RLS makes cross-tenant rows invisible, so a short row
		// count means one or more ids are missing/foreign — surface 404
		// without enumerating which.
		childRows, err := q.ListRisksByIDs(ctx, dbx.ListRisksByIDsParams{
			TenantID: pgUUID(tenantID),
			Column2:  uuidsToPg(dedupedChildren),
		})
		if err != nil {
			return fmt.Errorf("read child risks: %w", err)
		}
		if len(childRows) != len(dedupedChildren) {
			return ErrChildrenNotFound
		}

		scores := make([]ChildScore, 0, len(childRows))
		themeSet := make(map[string]struct{})
		for _, r := range childRows {
			if !IsAggregableMethodology(r.Methodology) {
				return fmt.Errorf("%w: child %s has methodology %q",
					ErrIncompatibleMethodology,
					uuid.UUID(r.ID.Bytes),
					r.Methodology,
				)
			}
			l, i, perr := likelihoodImpactFromInherent(r.InherentScore)
			if perr != nil {
				return fmt.Errorf("child %s inherent_score: %w", uuid.UUID(r.ID.Bytes), perr)
			}
			scores = append(scores, ChildScore{Likelihood: l, Impact: i})
			for _, t := range r.Themes {
				themeSet[t] = struct{}{}
			}
		}

		severity, err := ComputeSeverity(in.SeverityFunction, scores)
		if err != nil {
			return fmt.Errorf("compute severity: %w", err)
		}
		likelihood, impact := DeriveGridCell(severity)

		// Union themes from children. Sorted-by-normalise() so the
		// stored representation is canonical.
		themes := make([]string, 0, len(themeSet))
		for t := range themeSet {
			themes = append(themes, t)
		}
		themes = normaliseThemes(themes)

		// Build the parent risk's inherent_score blob.
		inherent := map[string]any{
			"likelihood":        likelihood,
			"impact":            impact,
			"severity":          severity,
			"severity_function": string(in.SeverityFunction),
			"child_count":       len(dedupedChildren),
			"aggregation_key":   key,
		}
		inherentBytes, err := json.Marshal(inherent)
		if err != nil {
			return fmt.Errorf("marshal inherent_score: %w", err)
		}

		// Parent risk: qualitative_5x5 + treatment=avoid (passes slice 019
		// validation without requiring linked controls — the parent is a
		// pattern tracker, not actively mitigated through its own controls).
		// Category derived from the first child's category.
		parentID := uuid.New()
		category := childRows[0].Category
		row, err := q.CreateAggregateRisk(ctx, dbx.CreateAggregateRiskParams{
			ID:                  pgUUID(parentID),
			TenantID:            pgUUID(tenantID),
			Title:               in.ParentTitle,
			Description:         fmt.Sprintf("Manual aggregation of %d child risks via %s.", len(dedupedChildren), in.SeverityFunction),
			Category:            category,
			Methodology:         dbx.RiskMethodologyQualitative5x5,
			InherentScore:       inherentBytes,
			Treatment:           dbx.RiskTreatmentAvoid,
			TreatmentOwner:      "",
			ResidualScore:       []byte("{}"),
			Accepter:            "",
			InstrumentReference: "",
			Level:               in.ParentLevel,
			OrgUnitID:           pgUUIDPtr(in.ParentOrgUnitID),
			Column15:            themes,
		})
		if err != nil {
			return fmt.Errorf("create aggregate parent risk: %w", err)
		}

		// Link each child via risk_aggregations (rule_id=NULL for manual).
		for _, childID := range dedupedChildren {
			if err := q.LinkRiskAggregation(ctx, dbx.LinkRiskAggregationParams{
				ParentRiskID: pgUUID(parentID),
				ChildRiskID:  pgUUID(childID),
				RuleID:       pgtype.UUID{}, // NULL — manual
				TenantID:     pgUUID(tenantID),
			}); err != nil {
				return fmt.Errorf("link aggregation child %s: %w", childID, err)
			}
		}

		parent := riskFromRow(row)
		parent.LinkedControlIDs = []uuid.UUID{}
		result = AggregateResult{
			Parent:           parent,
			LinkedChildren:   append([]uuid.UUID(nil), dedupedChildren...),
			Severity:         severity,
			Likelihood:       likelihood,
			Impact:           impact,
			ChildCount:       len(dedupedChildren),
			SeverityFunction: in.SeverityFunction,
			AggregationKey:   key,
		}
		return nil
	})
	return result, err
}

// LiveAggregation re-aggregates a parent risk's current children and returns
// the live severity. AC-8 / AC-9: closing a child removes the
// risk_aggregations row (ON DELETE CASCADE on child_risk_id from slice 052).
// The next call to LiveAggregation reflects the smaller set.
//
// The stored severity on the parent's inherent_score stays historical
// (frozen at create time) — never silently mutated on read.
func (s *Store) LiveAggregation(ctx context.Context, parentID uuid.UUID) (AggregateResult, error) {
	var result AggregateResult
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		parent, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(parentID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get parent: %w", err)
		}
		meta := extractAggregationMeta(parent.InherentScore)
		if meta.SeverityFunction == "" {
			return fmt.Errorf("%w: risk %s is not an aggregation (no severity_function on inherent_score)",
				ErrNotFound, parentID)
		}

		links, err := q.ListRiskAggregationChildren(ctx, dbx.ListRiskAggregationChildrenParams{
			TenantID:     pgUUID(tenantID),
			ParentRiskID: pgUUID(parentID),
		})
		if err != nil {
			return fmt.Errorf("list children: %w", err)
		}

		linked := make([]uuid.UUID, len(links))
		childIDs := make([]uuid.UUID, len(links))
		for i, l := range links {
			cid := uuid.UUID(l.ChildRiskID.Bytes)
			linked[i] = cid
			childIDs[i] = cid
		}

		parentRisk := riskFromRow(parent)
		parentRisk.LinkedControlIDs = []uuid.UUID{}

		// When every child has been closed (deleted), AC-8 says the
		// parent still survives. We return Severity=0, ChildCount=0,
		// and grid (1, 1) so the wire shape stays consistent.
		if len(childIDs) == 0 {
			result = AggregateResult{
				Parent:           parentRisk,
				LinkedChildren:   linked,
				Severity:         0,
				Likelihood:       1,
				Impact:           1,
				ChildCount:       0,
				SeverityFunction: meta.SeverityFunction,
				AggregationKey:   meta.AggregationKey,
			}
			return nil
		}

		childRows, err := q.ListRisksByIDs(ctx, dbx.ListRisksByIDsParams{
			TenantID: pgUUID(tenantID),
			Column2:  uuidsToPg(childIDs),
		})
		if err != nil {
			return fmt.Errorf("read child risks: %w", err)
		}
		scores := make([]ChildScore, 0, len(childRows))
		for _, r := range childRows {
			if !IsAggregableMethodology(r.Methodology) {
				// Skip ineligible children rather than fail — the
				// historical aggregation might predate the
				// constraint. Live recompute is best-effort.
				continue
			}
			l, i, perr := likelihoodImpactFromInherent(r.InherentScore)
			if perr != nil {
				continue
			}
			scores = append(scores, ChildScore{Likelihood: l, Impact: i})
		}
		if len(scores) == 0 {
			result = AggregateResult{
				Parent:           parentRisk,
				LinkedChildren:   linked,
				Severity:         0,
				Likelihood:       1,
				Impact:           1,
				ChildCount:       0,
				SeverityFunction: meta.SeverityFunction,
				AggregationKey:   meta.AggregationKey,
			}
			return nil
		}
		severity, err := ComputeSeverity(meta.SeverityFunction, scores)
		if err != nil {
			return fmt.Errorf("recompute severity: %w", err)
		}
		l, i := DeriveGridCell(severity)
		result = AggregateResult{
			Parent:           parentRisk,
			LinkedChildren:   linked,
			Severity:         severity,
			Likelihood:       l,
			Impact:           i,
			ChildCount:       len(scores),
			SeverityFunction: meta.SeverityFunction,
			AggregationKey:   meta.AggregationKey,
		}
		return nil
	})
	return result, err
}

// ---- internals ----

type aggregationMeta struct {
	Severity         int
	Likelihood       int
	Impact           int
	SeverityFunction SeverityFunction
	AggregationKey   string
}

func extractAggregationMeta(inherentBytes []byte) aggregationMeta {
	var raw map[string]any
	if err := json.Unmarshal(inherentBytes, &raw); err != nil {
		return aggregationMeta{}
	}
	m := aggregationMeta{}
	if v, ok := raw["severity"].(float64); ok {
		m.Severity = int(v)
	}
	if v, ok := raw["likelihood"].(float64); ok {
		m.Likelihood = int(v)
	}
	if v, ok := raw["impact"].(float64); ok {
		m.Impact = int(v)
	}
	if v, ok := raw["severity_function"].(string); ok {
		m.SeverityFunction = SeverityFunction(v)
	}
	if v, ok := raw["aggregation_key"].(string); ok {
		m.AggregationKey = v
	}
	return m
}

// likelihoodImpactFromInherent extracts (likelihood, impact) from a child
// risk's inherent_score JSONB. The aggregable methodologies (nist_800_30 +
// qualitative_5x5) both put integer likelihood + impact at the root.
func likelihoodImpactFromInherent(b []byte) (int, int, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return 0, 0, fmt.Errorf("parse inherent_score: %w", err)
	}
	lf, ok := raw["likelihood"].(float64)
	if !ok {
		return 0, 0, fmt.Errorf("inherent_score missing numeric `likelihood`")
	}
	imp, ok := raw["impact"].(float64)
	if !ok {
		return 0, 0, fmt.Errorf("inherent_score missing numeric `impact`")
	}
	return int(lf), int(imp), nil
}

func uniqueUUIDs(in []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(in))
	out := make([]uuid.UUID, 0, len(in))
	for _, u := range in {
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out
}

func uuidsToPg(in []uuid.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, len(in))
	for i, u := range in {
		out[i] = pgUUID(u)
	}
	return out
}
