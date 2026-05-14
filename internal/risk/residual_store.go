// residual_store.go — slice 020 residual-risk derivation, DB-backed.
//
// The ResidualDeriver ties the pure §6.2 math (residual.go) to the real data:
//
//   - per-link weights + design_score from `risk_control_links` (migration `_029`)
//   - operational_score from slice 012's eval.Engine.Effectiveness (rolling
//     30-day evidence pass rate) — REUSED, never reimplemented (AC-4)
//   - coverage_score from slice 012's eval.Engine.ControlState: passing scope
//     cells divided by applicable scope cells (AC-3, slice 017 applicability)
//
// It then writes the derived residual onto `risks.residual_score` (JSONB) and
// returns the breakdown for the API.
//
// Constitutional invariant #2: the deriver reads `control_evaluations` (the
// evaluation ledger, via eval.Engine) and `risk_control_links`; it writes ONLY
// `risks.residual_score`. It has no path to `evidence_records` — the evidence
// ledger is untouched. The eval.Engine it calls is itself a read-only ledger
// consumer.
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
	"github.com/mgoodric/security-atlas/internal/eval"
)

// WarningNoControlsLinked is the flag set on residual_score when a risk has no
// linked controls (AC-7). With nothing treating the risk, residual equals
// inherent and the operator is told why.
const WarningNoControlsLinked = "no_controls_linked"

// effectivenessEngine is the slice-012 surface the deriver depends on. Declared
// as an interface so the deriver is unit-testable and decoupled from
// eval.Engine's concrete type. *eval.Engine satisfies it.
type effectivenessEngine interface {
	Effectiveness(ctx context.Context, controlID uuid.UUID) (eval.Effectiveness, error)
	ControlState(ctx context.Context, controlID uuid.UUID, scopePredicate string, asOf time.Time) ([]eval.State, error)
	EvaluateControl(ctx context.Context, controlID uuid.UUID, trigger string, asOf time.Time) (int, error)
}

// ControlBreakdown is the per-linked-control effectiveness detail the API
// returns (AC-2). Every component is in [0,1]; ControlEffectiveness is the
// canvas §6.2 weighted composite.
type ControlBreakdown struct {
	ControlID            uuid.UUID `json:"control_id"`
	DesignScore          float64   `json:"design_score"`
	OperationalScore     float64   `json:"operational_score"`
	CoverageScore        float64   `json:"coverage_score"`
	WeightDesign         float64   `json:"weight_design"`
	WeightOperation      float64   `json:"weight_operation"`
	WeightCoverage       float64   `json:"weight_coverage"`
	ControlEffectiveness float64   `json:"control_effectiveness"`
	// OperationalNoData is true when the control has zero evaluations in the
	// rolling window — the operational_score is 0 but that is "no data", not
	// "0% effective" (ISC-28). The API surfaces this so a brand-new control
	// is not mistaken for a failing one.
	OperationalNoData bool `json:"operational_no_data"`
}

// ResidualResult is the full derived-residual answer for one risk (AC-2).
type ResidualResult struct {
	RiskID                       uuid.UUID          `json:"risk_id"`
	InherentScore                float64            `json:"inherent_score"`
	ResidualScore                float64            `json:"residual_score"`
	WeightedControlEffectiveness float64            `json:"weighted_control_effectiveness"`
	Breakdown                    []ControlBreakdown `json:"breakdown"`
	// Warning is "no_controls_linked" when Breakdown is empty (AC-7); empty
	// string otherwise.
	Warning string `json:"warning,omitempty"`
}

// ResidualDeriver computes and persists residual risk. Construct with
// NewResidualDeriver.
type ResidualDeriver struct {
	store  *Store
	engine effectivenessEngine
}

// NewResidualDeriver wires a deriver over a risk.Store and a slice-012
// effectiveness engine (*eval.Engine).
func NewResidualDeriver(store *Store, engine effectivenessEngine) *ResidualDeriver {
	return &ResidualDeriver{store: store, engine: engine}
}

// Derive computes the residual for one risk WITHOUT persisting it — the pure
// read path the GET handler uses (AC-2). It reads the risk, its link weights,
// and the per-control effectiveness components, then applies the §6.2 math.
//
// `recompute` controls the race fix: when true, the deriver calls
// EvaluateControl on each linked control BEFORE reading its effectiveness, so
// a freshly-ingested evidence record is reflected even if slice 012's own
// ingest subscriber has not yet written the new evaluation row. The GET path
// passes false (a read should not trigger evaluation); the NATS subscriber
// passes true.
func (d *ResidualDeriver) Derive(ctx context.Context, riskID uuid.UUID, recompute bool) (ResidualResult, error) {
	var (
		inherentJSON []byte
		methodology  dbx.RiskMethodology
		links        []dbx.ListRiskControlLinkWeightsRow
	)
	err := d.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		row, err := q.GetRiskByID(ctx, dbx.GetRiskByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(riskID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("get risk: %w", err)
		}
		inherentJSON = row.InherentScore
		methodology = row.Methodology
		links, err = q.ListRiskControlLinkWeights(ctx, dbx.ListRiskControlLinkWeightsParams{
			TenantID: pgUUID(tenantID),
			RiskID:   pgUUID(riskID),
		})
		if err != nil {
			return fmt.Errorf("list link weights: %w", err)
		}
		return nil
	})
	if err != nil {
		return ResidualResult{}, err
	}

	inherent, err := inherentScalar(methodology, inherentJSON)
	if err != nil {
		return ResidualResult{}, fmt.Errorf("risk %s inherent_score: %w", riskID, err)
	}

	result := ResidualResult{RiskID: riskID, InherentScore: inherent}
	if len(links) == 0 {
		// AC-7: no linked controls -> residual equals inherent, warning set.
		result.ResidualScore = inherent
		result.WeightedControlEffectiveness = 0
		result.Warning = WarningNoControlsLinked
		return result, nil
	}

	perControl := make([]float64, 0, len(links))
	for _, l := range links {
		controlID := uuid.UUID(l.ControlID.Bytes)
		bd, err := d.controlBreakdown(ctx, controlID, l, recompute)
		if err != nil {
			return ResidualResult{}, err
		}
		result.Breakdown = append(result.Breakdown, bd)
		perControl = append(perControl, bd.ControlEffectiveness)
	}
	result.WeightedControlEffectiveness = WeightedEffectiveness(perControl)
	result.ResidualScore = ResidualScore(inherent, result.WeightedControlEffectiveness)
	return result, nil
}

// DeriveAndPersist computes the residual and writes it onto
// `risks.residual_score`. The NATS residual subscriber and the link endpoint
// both call this; the GET handler calls Derive (no write). `recompute` is
// forwarded to Derive — the subscriber passes true to close the race with
// slice 012's ingest subscriber.
func (d *ResidualDeriver) DeriveAndPersist(ctx context.Context, riskID uuid.UUID, recompute bool) (ResidualResult, error) {
	result, err := d.Derive(ctx, riskID, recompute)
	if err != nil {
		return ResidualResult{}, err
	}
	blob, err := json.Marshal(residualBlobFromResult(result))
	if err != nil {
		return ResidualResult{}, fmt.Errorf("marshal residual_score: %w", err)
	}
	err = d.store.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if _, err := q.UpdateRiskResidual(ctx, dbx.UpdateRiskResidualParams{
			TenantID:      pgUUID(tenantID),
			ID:            pgUUID(riskID),
			ResidualScore: blob,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("update residual_score: %w", err)
		}
		return nil
	})
	if err != nil {
		return ResidualResult{}, err
	}
	return result, nil
}

// controlBreakdown computes the §6.2 effectiveness components for one linked
// control. operational_score is slice 012's rolling pass rate; coverage_score
// is passing-cells / applicable-cells from the evaluation ledger.
func (d *ResidualDeriver) controlBreakdown(ctx context.Context, controlID uuid.UUID, link dbx.ListRiskControlLinkWeightsRow, recompute bool) (ControlBreakdown, error) {
	if recompute {
		// Race fix (grill decision): re-evaluate the control from the ledger
		// BEFORE reading its effectiveness so a just-ingested evidence record
		// is reflected even if slice 012's ingest subscriber has not yet
		// written the new row. EvaluateControl is idempotent (append-only,
		// latest-by-evaluated_at wins) so the extra row is harmless.
		if _, err := d.engine.EvaluateControl(ctx, controlID, eval.TriggerIngest, eval.FarFuture); err != nil {
			if !errors.Is(err, eval.ErrControlNotFound) {
				return ControlBreakdown{}, fmt.Errorf("recompute control %s: %w", controlID, err)
			}
			// A linked control that no longer exists in-tenant contributes
			// nothing — fall through with zeroed operational/coverage.
		}
	}

	design := numericToFloat(link.DesignScore)
	wD := numericToFloat(link.WeightDesign)
	wO := numericToFloat(link.WeightOperation)
	wC := numericToFloat(link.WeightCoverage)

	operational, noData, err := d.operationalScore(ctx, controlID)
	if err != nil {
		return ControlBreakdown{}, err
	}
	coverage, err := d.coverageScore(ctx, controlID)
	if err != nil {
		return ControlBreakdown{}, err
	}

	components := EffectivenessComponents{
		DesignScore:      design,
		OperationalScore: operational,
		CoverageScore:    coverage,
		WeightDesign:     wD,
		WeightOperation:  wO,
		WeightCoverage:   wC,
	}
	return ControlBreakdown{
		ControlID:            controlID,
		DesignScore:          design,
		OperationalScore:     operational,
		CoverageScore:        coverage,
		WeightDesign:         wD,
		WeightOperation:      wO,
		WeightCoverage:       wC,
		ControlEffectiveness: ControlEffectiveness(components),
		OperationalNoData:    noData,
	}, nil
}

// operationalScore reuses slice 012's eval.Engine.Effectiveness — the rolling
// 30-day evidence pass rate (AC-4, ISC-27, ISC-29). It does NOT reimplement
// the pass-rate computation. Returns (score, noData, err): noData is true when
// the control has zero evaluations in the window (ISC-28).
func (d *ResidualDeriver) operationalScore(ctx context.Context, controlID uuid.UUID) (float64, bool, error) {
	eff, err := d.engine.Effectiveness(ctx, controlID)
	if err != nil {
		if errors.Is(err, eval.ErrControlNotFound) {
			return 0, true, nil
		}
		return 0, false, fmt.Errorf("operational score for control %s: %w", controlID, err)
	}
	return eff.PassRate, eff.TotalCount == 0, nil
}

// coverageScore is the slice-017 applicability set intersected with the scope
// cells where the control currently passes (canvas §6.2, AC-3). It reads
// slice 012's per-cell control state: coverage = passing cells / total
// applicable cells.
//
// ISC-31: a control that resolves to zero applicable cells has a single
// whole-tenant degenerate evaluation row (slice 012 writes one with a NULL
// scope_cell_id). Coverage is then "does that single state pass?" — 1.0 if it
// passes, 0.0 otherwise — never a divide-by-zero.
func (d *ResidualDeriver) coverageScore(ctx context.Context, controlID uuid.UUID) (float64, error) {
	states, err := d.engine.ControlState(ctx, controlID, "", eval.FarFuture)
	if err != nil {
		if errors.Is(err, eval.ErrControlNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("coverage score for control %s: %w", controlID, err)
	}
	if len(states) == 0 {
		// No evaluation rows at all — the control has never been evaluated.
		// Coverage is undefined; treat as 0 (no demonstrated coverage).
		return 0, nil
	}
	passing := 0
	for _, s := range states {
		if s.Result == eval.ResultPass {
			passing++
		}
	}
	return float64(passing) / float64(len(states)), nil
}

// residualBlob is the JSONB shape persisted to `risks.residual_score`. Kept
// flat and self-describing so the slice-019 GET handler and the board-report
// slices can read it without recomputing.
type residualBlob struct {
	Score                        float64            `json:"score"`
	InherentScore                float64            `json:"inherent_score"`
	WeightedControlEffectiveness float64            `json:"weighted_control_effectiveness"`
	Breakdown                    []ControlBreakdown `json:"breakdown"`
	Warning                      string             `json:"warning,omitempty"`
}

func residualBlobFromResult(r ResidualResult) residualBlob {
	return residualBlob{
		Score:                        r.ResidualScore,
		InherentScore:                r.InherentScore,
		WeightedControlEffectiveness: r.WeightedControlEffectiveness,
		Breakdown:                    r.Breakdown,
		Warning:                      r.Warning,
	}
}

// inherentScalar reduces a methodology-specific inherent_score JSONB to the
// single numeric the §6.2 residual formula multiplies. nist_800_30 +
// qualitative_5x5: likelihood × impact (the 5×5 scalar, 1..25). fair: lef ×
// lm. Aggregated parent risks (slice 053) carry a precomputed `severity`
// field — preferred when present. Other methodologies fall back to a
// `severity` field if the operator supplied one.
func inherentScalar(methodology dbx.RiskMethodology, inherentJSON []byte) (float64, error) {
	if len(inherentJSON) == 0 {
		return 0, errors.New("inherent_score is empty")
	}
	var raw map[string]any
	if err := json.Unmarshal(inherentJSON, &raw); err != nil {
		return 0, fmt.Errorf("parse inherent_score: %w", err)
	}
	// A precomputed severity (slice 053 aggregated parents) always wins.
	if sev, ok := numField(raw, "severity"); ok {
		return sev, nil
	}
	switch methodology {
	case dbx.RiskMethodologyNist80030, dbx.RiskMethodologyQualitative5x5:
		l, lok := numField(raw, "likelihood")
		i, iok := numField(raw, "impact")
		if !lok || !iok {
			return 0, errors.New("inherent_score missing numeric likelihood/impact")
		}
		return l * i, nil
	case dbx.RiskMethodologyFair:
		lef, lok := numField(raw, "lef")
		lm, mok := numField(raw, "lm")
		if !lok || !mok {
			return 0, errors.New("inherent_score missing numeric lef/lm")
		}
		return lef * lm, nil
	default:
		return 0, fmt.Errorf("methodology %q has no scalar inherent_score and no `severity` field", methodology)
	}
}

// numField reads a numeric field from a decoded JSON object. JSON numbers
// decode to float64 through encoding/json.
func numField(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

// numericToFloat converts a pgtype.Numeric (the sqlc type for the NUMERIC link
// columns) to float64. An invalid/NULL numeric yields 0 — the column is NOT
// NULL with a DEFAULT so this only fires on a genuinely absent value.
func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// floatToNumeric converts a float64 to a pgtype.Numeric for the link-weight
// write path. Used by the link endpoint when persisting per-link weights.
func floatToNumeric(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(formatNumeric(f)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("convert %v to numeric: %w", f, err)
	}
	return n, nil
}

// formatNumeric renders a float as a fixed-point decimal string pgtype can
// Scan. The link columns are NUMERIC(4,3); three decimals is sufficient.
func formatNumeric(f float64) string {
	return fmt.Sprintf("%.3f", f)
}
