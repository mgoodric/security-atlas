// Risk hierarchy links (#1748): attach existing risks as children of an
// existing parent risk, detach them, list a risk's children and parents,
// and compute the parent's roll-up severity.
//
// Canvas §6.4's manual aggregation is "a higher-level Risk explicitly
// references one or more child Risks". POST /v1/risks/aggregate only ever
// did that for a parent it created in the same step; these functions do it
// for a parent that already exists, such as an imported enterprise risk.
//
// Rules on every link, checked in this order:
//
//  1. Parent and children resolve in the active tenant. RLS hides another
//     tenant's rows, so a foreign id reads as missing (ErrNotFound for the
//     parent, ErrChildrenNotFound for a child, never naming which).
//  2. No cycle: a child that is the parent or one of its ancestors, at any
//     depth, is ErrAggregationCycle. This runs before the level check so
//     an ancestor is reported as a cycle, not as a level error.
//  3. Each child's level is strictly below the parent's (team < org <
//     company): ErrChildLevel. Org units are not compared, since findings
//     may roll up across teams.
//  4. Each child is scored on the 5x5 grid (ErrIncompatibleMethodology
//     otherwise), the same rule POST /v1/risks/aggregate applies, so the
//     roll-up has a severity to use.
//
// A link never writes the parent row. Its stored inherent_score and
// treatment stay as assessed; the roll-up is computed on read.
//
// LinkChildrenTx and UnlinkChildTx take a *dbx.Queries so the MCP
// write-proposal appliers can run the same validation inside their confirm
// transaction.

package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrChildLevel is returned when a child risk's level is not strictly below
// its parent's.
var ErrChildLevel = errors.New("risk: a child risk's level must be below its parent's level")

// ErrAggregationCycle is returned when a link would make a risk its own
// ancestor.
var ErrAggregationCycle = errors.New("risk: link would create a cycle in the risk hierarchy")

// ErrLinkNotFound is returned when a child to detach is not linked to the
// parent.
var ErrLinkNotFound = errors.New("risk: child risk is not linked to this parent")

// IsChildLinkValidation reports whether err is a caller error in a link
// request that the HTTP layer renders as a 400. A cycle (409) and a missing
// risk (404) are reported separately.
func IsChildLinkValidation(err error) bool {
	return errors.Is(err, ErrChildLevel) ||
		errors.Is(err, ErrIncompatibleMethodology) ||
		errors.Is(err, ErrEmptyChildren)
}

// LinkSource values name who made a link.
const (
	LinkSourceManual = "manual"
	LinkSourceRule   = "rule"
)

// LinkedRisk is one end of a hierarchy link, as a listing shows it.
type LinkedRisk struct {
	ID          uuid.UUID
	Title       string
	Level       dbx.RiskLevel
	OrgUnitID   *uuid.UUID
	Methodology dbx.RiskMethodology
	// Severity is the risk's own assessed 5x5 severity (AssessedSeverity).
	Severity int
	// RuleID is the aggregation rule that made the link; nil for a link
	// made by hand.
	RuleID   *uuid.UUID
	LinkedAt time.Time

	inherentScore []byte
}

// Source reports whether the link was made by hand or by a rule.
func (l LinkedRisk) Source() string {
	if l.RuleID != nil {
		return LinkSourceRule
	}
	return LinkSourceManual
}

// RollUp is a parent risk's severity computed from its children, set beside
// its own assessed score. Nothing here is stored.
type RollUp struct {
	// Severity is the children's severity combined by Function, on the
	// 0..25 grid.
	Severity int
	Function SeverityFunction
	// ChildCount is the number of children that contributed a score.
	ChildCount int
	// AssessedSeverity is the parent's own stored severity.
	AssessedSeverity int
	// ExceedsAssessed is true when Severity is higher than
	// AssessedSeverity, so the parent may be under-assessed.
	ExceedsAssessed bool
}

// levelRank orders the hierarchy: team < org < company. An unknown level
// ranks 0.
func levelRank(l dbx.RiskLevel) int {
	switch l {
	case dbx.RiskLevelTeam:
		return 1
	case dbx.RiskLevelOrg:
		return 2
	case dbx.RiskLevelCompany:
		return 3
	default:
		return 0
	}
}

// AssessedSeverity is a risk's own 5x5 severity from its inherent_score:
// the explicit `severity` of an aggregation parent, else likelihood ×
// impact, else 0 (a FAIR or malformed score).
func AssessedSeverity(inherentScore []byte) int {
	if len(inherentScore) == 0 {
		return 0
	}
	var score struct {
		Likelihood *float64 `json:"likelihood"`
		Impact     *float64 `json:"impact"`
		Severity   *float64 `json:"severity"`
	}
	if err := json.Unmarshal(inherentScore, &score); err != nil {
		return 0
	}
	if score.Severity != nil {
		return int(*score.Severity)
	}
	if score.Likelihood != nil && score.Impact != nil {
		return int(*score.Likelihood) * int(*score.Impact)
	}
	return 0
}

// RollUpFunction is the severity function a parent rolls its children up
// with: the one stored on its inherent_score (a slice-053 or rule-generated
// parent), else max.
func RollUpFunction(parentInherent []byte) SeverityFunction {
	switch fn := extractAggregationMeta(parentInherent).SeverityFunction; fn {
	case SeverityFunctionMax, SeverityFunctionWeightedMax, SeverityFunctionSum:
		return fn
	default:
		return SeverityFunctionMax
	}
}

// ComputeRollUp combines children into the parent's roll-up with the
// existing severity functions. Children without a 5x5 likelihood and
// impact are skipped. It returns nil when no child contributes a score.
// ExceedsAssessed is only set for a parent scored on the 5x5 grid; a FAIR
// parent's dollar exposure is not comparable.
func ComputeRollUp(parentMethodology dbx.RiskMethodology, parentInherent []byte, children []LinkedRisk) *RollUp {
	scores := make([]ChildScore, 0, len(children))
	for _, c := range children {
		if !IsAggregableMethodology(c.Methodology) {
			continue
		}
		l, i, err := likelihoodImpactFromInherent(c.inherentScore)
		if err != nil {
			continue
		}
		scores = append(scores, ChildScore{Likelihood: l, Impact: i})
	}
	if len(scores) == 0 {
		return nil
	}
	fn := RollUpFunction(parentInherent)
	severity, err := ComputeSeverity(fn, scores)
	if err != nil {
		// fn is one of the three known functions and scores is non-empty.
		return nil
	}
	assessed := AssessedSeverity(parentInherent)
	return &RollUp{
		Severity:         severity,
		Function:         fn,
		ChildCount:       len(scores),
		AssessedSeverity: assessed,
		ExceedsAssessed:  IsAggregableMethodology(parentMethodology) && severity > assessed,
	}
}

// WouldCycle reports whether linking parentID -> childID would make a risk
// its own ancestor: childID is parentID, or parentID is already below
// childID.
func WouldCycle(ctx context.Context, q *dbx.Queries, tenantID, parentID, childID uuid.UUID) (bool, error) {
	reaches, err := q.RiskAggregationReaches(ctx, dbx.RiskAggregationReachesParams{
		TenantID:     pgUUID(tenantID),
		FromRiskID:   pgUUID(childID),
		TargetRiskID: pgUUID(parentID),
	})
	if err != nil {
		return false, fmt.Errorf("cycle check %s -> %s: %w", parentID, childID, err)
	}
	return reaches, nil
}

// LinkChildrenTx attaches childIDs under parentID inside the caller's
// tenant transaction, after the checks in this file's header. A child that
// is already linked is left as it is (ON CONFLICT DO NOTHING), so a repeat
// is a no-op. It returns the deduplicated child ids it linked or found
// linked.
func LinkChildrenTx(ctx context.Context, q *dbx.Queries, tenantID, parentID uuid.UUID, childIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(childIDs) == 0 {
		return nil, ErrEmptyChildren
	}
	parent, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(parentID)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read parent risk: %w", err)
	}

	children := uniqueUUIDs(childIDs)
	rows, err := q.ListRisksByIDs(ctx, dbx.ListRisksByIDsParams{
		TenantID: pgUUID(tenantID),
		Column2:  uuidsToPg(children),
	})
	if err != nil {
		return nil, fmt.Errorf("read child risks: %w", err)
	}
	if len(rows) != len(children) {
		return nil, ErrChildrenNotFound
	}
	byID := make(map[uuid.UUID]dbx.Risk, len(rows))
	for _, r := range rows {
		byID[uuid.UUID(r.ID.Bytes)] = r
	}

	// Validate every child before writing any link, so a bad child in the
	// list leaves no partial result even outside a rolled-back tx.
	for _, id := range children {
		child := byID[id]
		cycle, err := WouldCycle(ctx, q, tenantID, parentID, id)
		if err != nil {
			return nil, err
		}
		if cycle {
			return nil, fmt.Errorf("%w: risk %s is %s or one of its ancestors", ErrAggregationCycle, id, parentID)
		}
		if levelRank(child.Level) >= levelRank(parent.Level) {
			return nil, fmt.Errorf("%w: risk %s is at level %s, parent %s is at level %s",
				ErrChildLevel, id, child.Level, parentID, parent.Level)
		}
		if !IsAggregableMethodology(child.Methodology) {
			return nil, fmt.Errorf("%w: risk %s has methodology %q", ErrIncompatibleMethodology, id, child.Methodology)
		}
	}
	for _, id := range children {
		if err := q.LinkRiskAggregation(ctx, dbx.LinkRiskAggregationParams{
			ParentRiskID: pgUUID(parentID),
			ChildRiskID:  pgUUID(id),
			RuleID:       pgtype.UUID{}, // NULL: made by hand
			TenantID:     pgUUID(tenantID),
		}); err != nil {
			return nil, fmt.Errorf("link child %s: %w", id, err)
		}
	}
	return children, nil
}

// UnlinkChildTx detaches childID from parentID inside the caller's tenant
// transaction. ErrLinkNotFound when no such link exists in the tenant.
func UnlinkChildTx(ctx context.Context, q *dbx.Queries, tenantID, parentID, childID uuid.UUID) error {
	n, err := q.DeleteRiskAggregationLink(ctx, dbx.DeleteRiskAggregationLinkParams{
		TenantID:     pgUUID(tenantID),
		ParentRiskID: pgUUID(parentID),
		ChildRiskID:  pgUUID(childID),
	})
	if err != nil {
		return fmt.Errorf("unlink child %s: %w", childID, err)
	}
	if n == 0 {
		return ErrLinkNotFound
	}
	return nil
}

// LinkChildren attaches childIDs under parentID (see LinkChildrenTx).
func (s *Store) LinkChildren(ctx context.Context, parentID uuid.UUID, childIDs []uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		_, err := LinkChildrenTx(ctx, q, tenantID, parentID, childIDs)
		return err
	})
}

// UnlinkChild detaches childID from parentID (see UnlinkChildTx).
func (s *Store) UnlinkChild(ctx context.Context, parentID, childID uuid.UUID) error {
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		return UnlinkChildTx(ctx, q, tenantID, parentID, childID)
	})
}

// ListChildren returns parentID's children and its roll-up (nil when no
// child contributes a score). ErrNotFound when the parent does not resolve
// in the tenant.
func (s *Store) ListChildren(ctx context.Context, parentID uuid.UUID) ([]LinkedRisk, *RollUp, error) {
	var (
		out    []LinkedRisk
		rollUp *RollUp
	)
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		parent, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(parentID)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("read parent risk: %w", err)
		}
		rows, err := q.ListRiskChildLinks(ctx, dbx.ListRiskChildLinksParams{
			TenantID:     pgUUID(tenantID),
			ParentRiskID: pgUUID(parentID),
		})
		if err != nil {
			return fmt.Errorf("list child links: %w", err)
		}
		out = make([]LinkedRisk, len(rows))
		for i, r := range rows {
			out[i] = linkedRisk(r.ID, r.Title, r.Level, r.OrgUnitID, r.Methodology, r.InherentScore, r.RuleID, r.LinkedAt)
		}
		rollUp = ComputeRollUp(parent.Methodology, parent.InherentScore, out)
		return nil
	})
	return out, rollUp, err
}

// ListParents returns the parents childID rolls up to. ErrNotFound when the
// risk does not resolve in the tenant.
func (s *Store) ListParents(ctx context.Context, childID uuid.UUID) ([]LinkedRisk, error) {
	var out []LinkedRisk
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{TenantID: pgUUID(tenantID), ID: pgUUID(childID)}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("read risk: %w", err)
		}
		rows, err := q.ListRiskParentLinks(ctx, dbx.ListRiskParentLinksParams{
			TenantID:    pgUUID(tenantID),
			ChildRiskID: pgUUID(childID),
		})
		if err != nil {
			return fmt.Errorf("list parent links: %w", err)
		}
		out = make([]LinkedRisk, len(rows))
		for i, r := range rows {
			out[i] = linkedRisk(r.ID, r.Title, r.Level, r.OrgUnitID, r.Methodology, r.InherentScore, r.RuleID, r.LinkedAt)
		}
		return nil
	})
	return out, err
}

func linkedRisk(id pgtype.UUID, title string, level dbx.RiskLevel, orgUnit pgtype.UUID,
	methodology dbx.RiskMethodology, inherent []byte, ruleID pgtype.UUID, linkedAt pgtype.Timestamptz) LinkedRisk {
	l := LinkedRisk{
		ID:            uuid.UUID(id.Bytes),
		Title:         title,
		Level:         level,
		Methodology:   methodology,
		Severity:      AssessedSeverity(inherent),
		LinkedAt:      linkedAt.Time,
		inherentScore: inherent,
	}
	if orgUnit.Valid {
		u := uuid.UUID(orgUnit.Bytes)
		l.OrgUnitID = &u
	}
	if ruleID.Valid {
		u := uuid.UUID(ruleID.Bytes)
		l.RuleID = &u
	}
	return l
}
