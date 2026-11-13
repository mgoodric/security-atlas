// engine.go — the slice 054 aggregation engine (canvas §6.6).
//
// The engine re-evaluates active rules and auto-creates / updates parent
// meta-risks when thresholds are met. It runs INSIDE the caller's
// transaction (the same tenant tx as the risk write that triggered it), so
// evaluation is atomic with the write and re-running on identical data is
// idempotent.
//
// Three load-bearing safety properties:
//
//  1. NARROW WRITER (safety bound). The Engine holds a *dbx.Queries, but it
//     only ever calls a deliberately small set of write methods —
//     CreateAggregateRisk, UpdateMetaRiskInherentScore, LinkRiskAggregation,
//     UnlinkRiskAggregation (only for links its own rule made), and
//     WriteAggregationRuleEvaluation. It NEVER calls CreateRisk,
//     DeleteRisk, or any control / policy / evidence writer. The set of
//     methods the engine touches is the safety bound, enforced by the
//     metaRiskWriter interface below — the engine is constructed with that
//     narrow interface, so it is structurally incapable of mutating
//     arbitrary risk rows, controls, or anything else.
//
//  2. CYCLE EXCLUSION. A rule-generated meta-risk carries the union of its
//     children's themes — which always includes the rule's target_theme. If
//     the engine treated meta-risks as candidates, every rule would
//     immediately re-aggregate its own output. So every meta-risk is stamped
//     `inherent_score.rule_generated = true`, and the engine's candidate
//     filter drops any risk carrying that flag. That is the real cycle
//     prevention; the dsl.go self-referential-rule_id check is only defense
//     in depth.
//
//  3. ONE META-RISK PER ACTIVATION (#1861). A rule owns exactly one
//     meta-risk per activation. Its idempotency key —
//     sha256(rule_id|activated_at) — is fixed while the rule stays active,
//     so the rolling window moving forward each day never changes it.
//     (Keying on the window start spawned a new meta-risk every day once
//     window_days had passed.) The window is an attribute of the meta-risk,
//     not its identity: each evaluation re-links the meta-risk to exactly the
//     risks in the current window and recomputes its score, even when the
//     threshold is no longer met, so a removed theme, a deleted risk, or a
//     child ageing out of the window never leaves a stale link or score.
//     Re-activating a rule starts a new meta-risk; the old one is kept as
//     history. Every evaluation of a key runs under a transaction-scoped
//     advisory lock, so two concurrent writes cannot both create.
package aggrule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// metaRiskWriter is the NARROW write surface the engine is allowed to touch.
// It is the safety bound: the engine cannot write `risks` arbitrarily,
// cannot touch `controls`, `policies`, or `evidence_records` — the only
// mutations it can perform are the five methods declared here. *dbx.Queries
// satisfies this interface; the engine is constructed against the interface,
// not the concrete struct, so the bound holds by construction.
type metaRiskWriter interface {
	// rule + candidate reads
	ListActiveAggregationRules(ctx context.Context, tenantID pgtype.UUID) ([]dbx.AggregationRule, error)
	ListCandidateRisksForRule(ctx context.Context, arg dbx.ListCandidateRisksForRuleParams) ([]dbx.Risk, error)
	GetRuleMetaRiskByKey(ctx context.Context, arg dbx.GetRuleMetaRiskByKeyParams) (dbx.Risk, error)
	ListRiskAggregationChildren(ctx context.Context, arg dbx.ListRiskAggregationChildrenParams) ([]dbx.ListRiskAggregationChildrenRow, error)
	// transaction-scoped advisory lock on a meta-risk key (#1861)
	LockAggregationKey(ctx context.Context, aggregationKey string) error
	// the five — and only five — writes
	CreateAggregateRisk(ctx context.Context, arg dbx.CreateAggregateRiskParams) (dbx.Risk, error)
	UpdateMetaRiskInherentScore(ctx context.Context, arg dbx.UpdateMetaRiskInherentScoreParams) (dbx.Risk, error)
	LinkRiskAggregation(ctx context.Context, arg dbx.LinkRiskAggregationParams) error
	UnlinkRiskAggregation(ctx context.Context, arg dbx.UnlinkRiskAggregationParams) error
	WriteAggregationRuleEvaluation(ctx context.Context, arg dbx.WriteAggregationRuleEvaluationParams) (dbx.AggregationRuleEvaluation, error)
}

// Compile-time proof that *dbx.Queries satisfies the narrow writer.
var _ metaRiskWriter = (*dbx.Queries)(nil)

// Engine evaluates aggregation rules. Construct one per evaluation cycle via
// NewEngine, inside the caller's transaction.
type Engine struct {
	w   metaRiskWriter
	now func() time.Time
}

// NewEngine constructs an Engine over the narrow metaRiskWriter. Callers pass
// a *dbx.Queries bound to their transaction; the engine only ever sees the
// narrow interface.
func NewEngine(w metaRiskWriter) *Engine {
	return &Engine{w: w, now: time.Now}
}

// WithClock replaces the engine's clock, which places the rolling window.
// Tests use it to step across more than window_days.
func (e *Engine) WithClock(now func() time.Time) *Engine {
	e.now = now
	return e
}

// Evaluation is one row of the aggregation_rule_evaluations ledger surfaced
// to Go callers.
type Evaluation struct {
	ID          uuid.UUID
	RuleID      uuid.UUID
	Outcome     string // fired | near_miss | no_match
	RiskCount   int
	TeamCount   int
	WindowStart *time.Time
	MetaRiskID  *uuid.UUID
	EvaluatedAt time.Time
}

// Evaluate runs every active rule for tenantID. When triggerThemes is
// non-empty, a rule is only evaluated if its target_theme appears in the set
// (the themes of the risk that triggered this cycle); pass nil to force a
// full sweep. Every evaluated rule writes exactly one
// aggregation_rule_evaluations row — including `no_match` — so the audit
// trail proves the engine ran (AC-8).
func (e *Engine) Evaluate(ctx context.Context, tenantID uuid.UUID, triggerThemes []string) ([]Evaluation, error) {
	rules, err := e.w.ListActiveAggregationRules(ctx, pgUUID(tenantID))
	if err != nil {
		return nil, fmt.Errorf("aggrule: list active rules: %w", err)
	}

	triggerSet := make(map[string]struct{}, len(triggerThemes))
	for _, t := range triggerThemes {
		triggerSet[t] = struct{}{}
	}

	var out []Evaluation
	for _, ruleRow := range rules {
		if len(triggerSet) > 0 {
			if _, hit := triggerSet[ruleRow.TargetTheme]; !hit {
				continue
			}
		}
		ev, err := e.evaluateRule(ctx, tenantID, ruleRow)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// evaluateRule runs one active rule: reads candidates, checks thresholds,
// and on a met threshold creates or updates the meta-risk. Exactly one
// aggregation_rule_evaluations row is written before return.
func (e *Engine) evaluateRule(ctx context.Context, tenantID uuid.UUID, ruleRow dbx.AggregationRule) (Evaluation, error) {
	var rule Rule
	if err := json.Unmarshal(ruleRow.RuleBody, &rule); err != nil {
		return Evaluation{}, fmt.Errorf("aggrule: unmarshal rule_body for %s: %w",
			uuid.UUID(ruleRow.ID.Bytes), err)
	}
	ruleID := uuid.UUID(ruleRow.ID.Bytes)

	// Serialize every evaluation of this rule's meta-risk before reading
	// anything, so the candidate read and the meta-risk lookup below both see
	// a concurrent evaluation's committed result (#1861).
	key := metaRiskKey(ruleID, ruleRow.ActivatedAt)
	if err := e.w.LockAggregationKey(ctx, key); err != nil {
		return Evaluation{}, fmt.Errorf("aggrule: lock meta-risk key for rule %s: %w", ruleID, err)
	}

	// Window cut-off: the LATER of (now - window_days) and the rule's
	// activated_at. Using activated_at as a floor means a re-activated rule
	// never re-fires on risks written before its (re)activation — P0
	// anti-criterion "no re-fire on historical data".
	now := e.now().UTC()
	windowStart := dayFloorUTC(now.AddDate(0, 0, -int(ruleRow.WindowDays)))
	if ruleRow.ActivatedAt.Valid {
		activatedFloor := ruleRow.ActivatedAt.Time.UTC()
		if activatedFloor.After(windowStart) {
			windowStart = activatedFloor
		}
	}

	candidates, err := e.w.ListCandidateRisksForRule(ctx, dbx.ListCandidateRisksForRuleParams{
		TenantID:    pgUUID(tenantID),
		TargetTheme: rule.TargetTheme,
		WindowStart: pgtype.Timestamptz{Time: windowStart, Valid: true},
	})
	if err != nil {
		return Evaluation{}, fmt.Errorf("aggrule: list candidates for rule %s: %w", ruleID, err)
	}

	// CYCLE EXCLUSION: drop any candidate that is itself a rule-generated
	// meta-risk. Without this, a rule would immediately re-aggregate its own
	// output (a rule-generated meta-risk carries the union of its children's
	// themes, which always includes target_theme).
	eligible := make([]dbx.Risk, 0, len(candidates))
	for _, c := range candidates {
		if isRuleGenerated(c.InherentScore) {
			continue
		}
		eligible = append(eligible, c)
	}

	// Threshold dimensions: distinct scored-risk count and distinct team count.
	teamSet := make(map[string]struct{})
	scores := make([]ChildSeverity, 0, len(eligible))
	childIDs := make([]uuid.UUID, 0, len(eligible))
	themeUnion := make(map[string]struct{})
	var category dbx.RiskCategory
	for _, c := range eligible {
		sev, perr := severityFromInherent(c.InherentScore)
		if perr != nil {
			// A candidate with an unreadable inherent_score is skipped, not
			// fatal — the rule still evaluates on the rest. It also does not
			// count toward the risk OR team threshold, so the two dimensions
			// stay consistent.
			continue
		}
		scores = append(scores, ChildSeverity(sev))
		childIDs = append(childIDs, uuid.UUID(c.ID.Bytes))
		for _, t := range c.Themes {
			themeUnion[t] = struct{}{}
		}
		// "Team" = the risk's org_unit_id (a nil org_unit_id counts as the
		// implicit "unassigned" team so the count is never silently zero).
		teamKey := "unassigned"
		if c.OrgUnitID.Valid {
			teamKey = uuid.UUID(c.OrgUnitID.Bytes).String()
		}
		teamSet[teamKey] = struct{}{}
		// Category of the meta-risk is the first scored child's category.
		if category == "" {
			category = c.Category
		}
	}
	if category == "" {
		category = dbx.RiskCategoryOperational
	}

	riskCount := len(childIDs)
	teamCount := len(teamSet)

	thresholdMet := riskCount >= int(ruleRow.MinRisks) && teamCount >= int(ruleRow.MinTeams)

	// Severity over the current window. With no scored child (possible only
	// below threshold) it is 0, which empties a stale meta-risk's score.
	severity := 0
	if len(scores) > 0 {
		var serr error
		severity, serr = ComputeRuleSeverity(ctx, rule.SeverityFunction, scores, rule.CustomRego)
		if serr != nil {
			return Evaluation{}, fmt.Errorf("aggrule: compute severity for rule %s: %w", ruleID, serr)
		}
	}
	likelihood, impact := risk.DeriveGridCell(severity)
	inherent, err := metaRiskInherent(rule, key, windowStart, severity, likelihood, impact, riskCount)
	if err != nil {
		return Evaluation{}, err
	}

	existing, err := e.w.GetRuleMetaRiskByKey(ctx, dbx.GetRuleMetaRiskByKeyParams{
		TenantID: pgUUID(tenantID),
		Column2:  key,
	})
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Evaluation{}, fmt.Errorf("aggrule: meta-risk key lookup for rule %s: %w", ruleID, err)
	}

	if !thresholdMet {
		outcome := "no_match"
		// near_miss: at least one dimension reached its threshold but not all.
		if (riskCount >= int(ruleRow.MinRisks)) != (teamCount >= int(ruleRow.MinTeams)) {
			outcome = "near_miss"
		}
		// A meta-risk this activation already created stays (the engine never
		// closes or deletes a parent), but its links and score follow the
		// current window so it never reports a child that has left (#1861).
		if found {
			if err := e.refreshMetaRisk(ctx, tenantID, existing, ruleID, inherent, childIDs); err != nil {
				return Evaluation{}, err
			}
		}
		return e.writeEvaluation(ctx, tenantID, ruleID, outcome, riskCount, teamCount, nil, nil)
	}

	// Threshold met — fire: update this activation's meta-risk, or create it.
	var metaRiskID uuid.UUID
	if found {
		if err := e.refreshMetaRisk(ctx, tenantID, existing, ruleID, inherent, childIDs); err != nil {
			return Evaluation{}, err
		}
		metaRiskID = uuid.UUID(existing.ID.Bytes)
	} else {
		themes := make([]string, 0, len(themeUnion))
		for t := range themeUnion {
			themes = append(themes, t)
		}
		metaRiskID, err = e.createMetaRisk(ctx, tenantID, rule, ruleID, inherent, riskCount, category, themes, childIDs)
		if err != nil {
			return Evaluation{}, err
		}
	}

	return e.writeEvaluation(ctx, tenantID, ruleID, "fired", riskCount, teamCount, &windowStart, &metaRiskID)
}

// metaRiskInherent builds a meta-risk's inherent_score blob. window_start
// records the window the score was computed over; it is an attribute, not
// part of the key.
func metaRiskInherent(rule Rule, key string, windowStart time.Time, severity, likelihood, impact, childCount int) ([]byte, error) {
	inherent := map[string]any{
		"likelihood":        likelihood,
		"impact":            impact,
		"severity":          severity,
		"severity_function": rule.SeverityFunction,
		"child_count":       childCount,
		"aggregation_key":   key,
		// CYCLE EXCLUSION flag — the engine's candidate filter drops any
		// risk carrying this, so a rule never re-aggregates its own output.
		"rule_generated": true,
		"rule_id":        rule.RuleID,
		"window_start":   windowStart.Format(time.RFC3339),
	}
	b, err := json.Marshal(inherent)
	if err != nil {
		return nil, fmt.Errorf("aggrule: marshal meta-risk inherent_score: %w", err)
	}
	return b, nil
}

// refreshMetaRisk recomputes an existing meta-risk's inherent_score and
// reconciles its rule-made child links to childIDs. Title / level /
// lifecycle stay frozen.
func (e *Engine) refreshMetaRisk(ctx context.Context, tenantID uuid.UUID, existing dbx.Risk, ruleID uuid.UUID, inherent []byte, childIDs []uuid.UUID) error {
	metaRiskID := uuid.UUID(existing.ID.Bytes)
	if _, err := e.w.UpdateMetaRiskInherentScore(ctx, dbx.UpdateMetaRiskInherentScoreParams{
		TenantID:      pgUUID(tenantID),
		ID:            existing.ID,
		InherentScore: inherent,
	}); err != nil {
		return fmt.Errorf("aggrule: update meta-risk %s: %w", metaRiskID, err)
	}
	if err := e.unlinkStaleChildren(ctx, tenantID, metaRiskID, ruleID, childIDs); err != nil {
		return err
	}
	return e.linkChildren(ctx, tenantID, metaRiskID, ruleID, childIDs)
}

// createMetaRisk creates this activation's meta-risk and links its
// children. qualitative_5x5 + treatment=untreated mirrors slice 053's
// manual-aggregation parent: a generated meta-risk carries no treatment
// decision until someone makes one (#1746).
func (e *Engine) createMetaRisk(
	ctx context.Context,
	tenantID uuid.UUID,
	rule Rule,
	ruleID uuid.UUID,
	inherent []byte,
	childCount int,
	category dbx.RiskCategory,
	themes []string,
	childIDs []uuid.UUID,
) (uuid.UUID, error) {
	metaRiskID := uuid.New()
	if _, err := e.w.CreateAggregateRisk(ctx, dbx.CreateAggregateRiskParams{
		ID:                  pgUUID(metaRiskID),
		TenantID:            pgUUID(tenantID),
		Title:               rule.Title(),
		Description:         fmt.Sprintf("Rule-generated aggregation (%s) of %d child risks via %s.", rule.RuleID, childCount, rule.SeverityFunction),
		Category:            category,
		Methodology:         dbx.RiskMethodologyQualitative5x5,
		InherentScore:       inherent,
		Treatment:           dbx.RiskTreatmentUntreated,
		TreatmentOwner:      "",
		ResidualScore:       []byte("{}"),
		Accepter:            "",
		InstrumentReference: "",
		Level:               dbx.RiskLevel(rule.ParentLevel),
		OrgUnitID:           pgtype.UUID{}, // meta-risks span teams; no single org unit
		Column15:            themes,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("aggrule: create meta-risk for rule %s: %w", ruleID, err)
	}
	if err := e.linkChildren(ctx, tenantID, metaRiskID, ruleID, childIDs); err != nil {
		return uuid.Nil, err
	}
	return metaRiskID, nil
}

// unlinkStaleChildren removes this rule's links from the meta-risk to any
// child no longer in childIDs: a theme removed, or a child that aged out of
// the window. Links made by hand (rule_id NULL) or by another rule are left
// alone. A deleted child's link is already gone (ON DELETE CASCADE).
func (e *Engine) unlinkStaleChildren(ctx context.Context, tenantID, metaRiskID, ruleID uuid.UUID, childIDs []uuid.UUID) error {
	links, err := e.w.ListRiskAggregationChildren(ctx, dbx.ListRiskAggregationChildrenParams{
		TenantID:     pgUUID(tenantID),
		ParentRiskID: pgUUID(metaRiskID),
	})
	if err != nil {
		return fmt.Errorf("aggrule: list children of meta-risk %s: %w", metaRiskID, err)
	}
	keep := make(map[uuid.UUID]struct{}, len(childIDs))
	for _, id := range childIDs {
		keep[id] = struct{}{}
	}
	for _, l := range links {
		if !l.RuleID.Valid || uuid.UUID(l.RuleID.Bytes) != ruleID {
			continue
		}
		childID := uuid.UUID(l.ChildRiskID.Bytes)
		if _, ok := keep[childID]; ok {
			continue
		}
		if err := e.w.UnlinkRiskAggregation(ctx, dbx.UnlinkRiskAggregationParams{
			TenantID:     pgUUID(tenantID),
			ParentRiskID: pgUUID(metaRiskID),
			ChildRiskID:  l.ChildRiskID,
		}); err != nil {
			return fmt.Errorf("aggrule: unlink child %s from meta-risk %s: %w", childID, metaRiskID, err)
		}
	}
	return nil
}

// linkChildren links every childID under the meta-risk via risk_aggregations,
// stamping the originating rule_id. LinkRiskAggregation is ON CONFLICT DO
// NOTHING so re-linking on a window update is a harmless no-op.
func (e *Engine) linkChildren(ctx context.Context, tenantID, metaRiskID, ruleID uuid.UUID, childIDs []uuid.UUID) error {
	for _, childID := range childIDs {
		if err := e.w.LinkRiskAggregation(ctx, dbx.LinkRiskAggregationParams{
			ParentRiskID: pgUUID(metaRiskID),
			ChildRiskID:  pgUUID(childID),
			RuleID:       pgUUID(ruleID),
			TenantID:     pgUUID(tenantID),
		}); err != nil {
			return fmt.Errorf("aggrule: link child %s under meta-risk %s: %w", childID, metaRiskID, err)
		}
	}
	return nil
}

// writeEvaluation appends exactly one aggregation_rule_evaluations row and
// returns the Go-side Evaluation. window + metaRiskID are non-nil only for
// outcome="fired" (the DB CHECK enforces this coherence).
func (e *Engine) writeEvaluation(
	ctx context.Context,
	tenantID, ruleID uuid.UUID,
	outcome string,
	riskCount, teamCount int,
	window *time.Time,
	metaRiskID *uuid.UUID,
) (Evaluation, error) {
	params := dbx.WriteAggregationRuleEvaluationParams{
		ID:        pgUUID(uuid.New()),
		TenantID:  pgUUID(tenantID),
		RuleID:    pgUUID(ruleID),
		Outcome:   outcome,
		RiskCount: toInt32(riskCount),
		TeamCount: toInt32(teamCount),
	}
	if window != nil {
		params.WindowStart = pgtype.Timestamptz{Time: *window, Valid: true}
	}
	if metaRiskID != nil {
		params.MetaRiskID = pgUUID(*metaRiskID)
	}
	row, err := e.w.WriteAggregationRuleEvaluation(ctx, params)
	if err != nil {
		return Evaluation{}, fmt.Errorf("aggrule: write evaluation for rule %s: %w", ruleID, err)
	}
	ev := Evaluation{
		ID:          uuid.UUID(row.ID.Bytes),
		RuleID:      ruleID,
		Outcome:     outcome,
		RiskCount:   riskCount,
		TeamCount:   teamCount,
		WindowStart: window,
		MetaRiskID:  metaRiskID,
	}
	if row.EvaluatedAt.Valid {
		ev.EvaluatedAt = row.EvaluatedAt.Time
	}
	return ev, nil
}

// ----- internals -----

// toInt32 narrows a non-negative int to int32, clamping at math.MaxInt32 so
// a pathological count can never wrap to a negative value (CodeQL CWE-681).
// Every caller here passes a validated-positive threshold or a slice length,
// so the clamp branch is unreachable in practice — it exists to make the
// narrowing provably safe.
func toInt32(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

// dayFloorUTC truncates t to the start of its UTC day, so every evaluation
// within one day reads the same window.
func dayFloorUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// metaRiskKey is the idempotency key of a rule activation's meta-risk:
// sha256_hex(rule_id|activated_at_rfc3339), stored on the meta-risk's
// inherent_score.aggregation_key. activated_at changes only on
// (re)activation, so the key holds still while the window rolls (#1861).
//
// It hashes the same string the old window key hashed during a rule's first
// window_days, when window_start was activated_at, so a meta-risk created
// then keeps matching. An active rule always has activated_at (DB CHECK);
// without one the key falls back to the rule id alone.
func metaRiskKey(ruleID uuid.UUID, activatedAt pgtype.Timestamptz) string {
	h := sha256.New()
	h.Write([]byte(ruleID.String()))
	h.Write([]byte("|"))
	if activatedAt.Valid {
		h.Write([]byte(activatedAt.Time.UTC().Format(time.RFC3339)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// isRuleGenerated reports whether a risk's inherent_score carries the
// rule_generated flag. Such risks are excluded from candidate reads (cycle
// prevention).
func isRuleGenerated(inherentBytes []byte) bool {
	var raw map[string]any
	if err := json.Unmarshal(inherentBytes, &raw); err != nil {
		return false
	}
	v, ok := raw["rule_generated"].(bool)
	return ok && v
}

// severityFromInherent extracts a severity scalar from a child risk's
// inherent_score. The aggregable methodologies (nist_800_30 +
// qualitative_5x5) carry integer likelihood + impact at the root; severity =
// likelihood × impact.
func severityFromInherent(inherentBytes []byte) (int, error) {
	var raw map[string]any
	if err := json.Unmarshal(inherentBytes, &raw); err != nil {
		return 0, fmt.Errorf("parse inherent_score: %w", err)
	}
	// A risk that already carries a computed `severity` (e.g. a manual
	// aggregation parent) — use it directly.
	if s, ok := raw["severity"].(float64); ok {
		return int(s), nil
	}
	lf, ok := raw["likelihood"].(float64)
	if !ok {
		return 0, fmt.Errorf("inherent_score missing numeric likelihood")
	}
	imp, ok := raw["impact"].(float64)
	if !ok {
		return 0, fmt.Errorf("inherent_score missing numeric impact")
	}
	return int(lf) * int(imp), nil
}
