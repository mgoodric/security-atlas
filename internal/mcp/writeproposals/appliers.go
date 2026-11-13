// appliers.go — the four canonical Applier implementations the MCP
// write tools dispatch through. Each applier runs inside the proposal's
// confirm transaction so a downstream-write failure rolls the proposal
// back to state=ai_proposed.
//
// Applier SQL is HAND-WRITTEN here rather than routed through the sqlc-
// wrapped domain stores. Two reasons:
//
//   1. The domain stores own their own transactions; calling them from
//      inside our tx would either nest (pgx doesn't natively support
//      this without savepoints) or split the write across two txs
//      (defeats the rollback story).
//
//   2. The set of fields we accept on each tool is bounded by the
//      proposal's tool_input JSON schema — narrower than the full
//      domain-store CreateInput. Hand-writing the INSERT keeps the
//      attack surface scoped to exactly what the tool advertises.
//
// All four appliers run as atlas_app, under the proposal's tenant GUC
// (already set by writeproposals.Store.inTx before the applier executes).
// RLS therefore gates every INSERT/UPDATE; cross-tenant writes are
// impossible at the DB layer.

package writeproposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// CreateRiskInput is the bounded shape the create_risk MCP tool ships
// in tool_input. A subset of risk.CreateInput — we accept only the
// fields the LLM can reasonably propose (title, description, category,
// methodology defaults, treatment defaulting to untreated). Score blocks are
// JSON-RawMessage so the LLM can supply a 5x5 matrix or a NIST 800-30
// envelope; defense-in-depth CHECK constraints on the risks table
// catch malformed inputs.
type CreateRiskInput struct {
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Category      string          `json:"category"`
	Methodology   string          `json:"methodology,omitempty"`
	InherentScore json.RawMessage `json:"inherent_score,omitempty"`
	Treatment     string          `json:"treatment,omitempty"`
	// AcceptanceStatus applies to treatment=accept only. create_risk cannot
	// set an accepter or expiry, so only a proposed acceptance can pass.
	AcceptanceStatus string `json:"acceptance_status,omitempty"`
	TreatmentOwner   string `json:"treatment_owner,omitempty"`
}

// ApplyCreateRisk inserts a new risk row using the proposal's tool_input.
// Returns the new risk's UUID for applied_subject.
func ApplyCreateRisk(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
	var in CreateRiskInput
	if err := json.Unmarshal(p.ToolInput, &in); err != nil {
		return "", fmt.Errorf("apply create_risk: parse input: %w", err)
	}
	if in.Title == "" {
		return "", fmt.Errorf("apply create_risk: title is required")
	}
	if in.Category == "" {
		return "", fmt.Errorf("apply create_risk: category is required")
	}
	methodology := in.Methodology
	if methodology == "" {
		methodology = "nist_800_30"
	}
	// An omitted treatment is untreated: the applier never records a
	// treatment decision the proposal did not make (#1746).
	treatment := dbx.RiskTreatment(in.Treatment)
	if treatment == "" {
		treatment = dbx.RiskTreatmentUntreated
	}
	status := dbx.RiskAcceptanceStatus(in.AcceptanceStatus)
	// A new risk has no linked controls or action plans and no accepter.
	if err := risk.ValidateTreatment(risk.TreatmentInput{
		Treatment:        treatment,
		AcceptanceStatus: status,
	}); err != nil {
		return "", fmt.Errorf("apply create_risk: %w", err)
	}
	inherent := in.InherentScore
	if len(inherent) == 0 {
		inherent = json.RawMessage(`{}`)
	}

	id := uuid.New()
	_, err := tx.Exec(ctx, `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score,
			accepted_until, accepter, instrument_reference, acceptance_status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}'::jsonb, NULL, '', '', $10)
	`, id, p.TenantID, in.Title, in.Description, in.Category, methodology,
		[]byte(inherent), treatment, in.TreatmentOwner, risk.NullAcceptanceStatus(status))
	if err != nil {
		return "", fmt.Errorf("apply create_risk: insert: %w", err)
	}
	return id.String(), nil
}

// UpdateControlStateInput is the bounded shape the update_control_state
// MCP tool ships. Constitutional invariant #2 forbids the AI from
// directly writing control_evaluations; instead we synthesise an
// evidence_records row with kind=`manual.control_state_override.v1` so
// the eval engine sees a valid evidence source and the override flows
// through the existing pipeline.
type UpdateControlStateInput struct {
	ControlID string `json:"control_id"`
	State     string `json:"state"`
	Rationale string `json:"rationale"`
}

// ApplyUpdateControlState appends a manual override evidence record.
// Returns the new evidence_records row's UUID for applied_subject.
func ApplyUpdateControlState(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
	var in UpdateControlStateInput
	if err := json.Unmarshal(p.ToolInput, &in); err != nil {
		return "", fmt.Errorf("apply update_control_state: parse input: %w", err)
	}
	if in.ControlID == "" || in.State == "" {
		return "", fmt.Errorf("apply update_control_state: control_id and state are required")
	}
	controlID, err := uuid.Parse(in.ControlID)
	if err != nil {
		return "", fmt.Errorf("apply update_control_state: control_id must be UUID: %w", err)
	}
	result, err := stateToEvidenceResult(in.State)
	if err != nil {
		return "", err
	}

	payload, _ := json.Marshal(map[string]any{
		"override_state": in.State,
		"rationale":      in.Rationale,
		"ai_proposed":    true,
	})
	id := uuid.New()
	hash := "mcp-override-" + id.String()
	_, err = tx.Exec(ctx, `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref,
			evidence_kind, ingestion_path, source_attribution
		)
		VALUES ($1, $2, $3, $4, now(), $5, $6, $7, $8, $9, $10, 'manual_upload', $11)
	`, id, p.TenantID, controlID, time.Now().UTC(),
		[]byte(`{"source":"mcp.write_tool"}`), result, payload, hash,
		"mcp/control_state_override",
		"manual.control_state_override.v1",
		[]byte(`{"actor":"mcp.update_control_state","ai_assisted":true}`))
	if err != nil {
		return "", fmt.Errorf("apply update_control_state: insert evidence: %w", err)
	}
	return id.String(), nil
}

// PushEvidenceInput is the bounded shape the push_evidence MCP tool
// ships. Direct evidence push — same shape as the Evidence SDK (slice
// 003) but mediated by the HITL approval flow because the LLM is the
// origin.
type PushEvidenceInput struct {
	ControlID  string          `json:"control_id"`
	Kind       string          `json:"kind"`
	Result     string          `json:"result"`
	ObservedAt time.Time       `json:"observed_at,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// ApplyPushEvidence inserts an evidence record.
func ApplyPushEvidence(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
	var in PushEvidenceInput
	if err := json.Unmarshal(p.ToolInput, &in); err != nil {
		return "", fmt.Errorf("apply push_evidence: parse input: %w", err)
	}
	if in.ControlID == "" || in.Kind == "" || in.Result == "" {
		return "", fmt.Errorf("apply push_evidence: control_id, kind, result are required")
	}
	controlID, err := uuid.Parse(in.ControlID)
	if err != nil {
		return "", fmt.Errorf("apply push_evidence: control_id must be UUID: %w", err)
	}
	if _, err := stateToEvidenceResult(in.Result); err != nil {
		return "", err
	}
	observed := in.ObservedAt
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	payload := in.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	id := uuid.New()
	hash := "mcp-push-" + id.String()
	_, err = tx.Exec(ctx, `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref,
			evidence_kind, ingestion_path, source_attribution
		)
		VALUES ($1, $2, $3, $4, now(), $5, $6, $7, $8, $9, $10, 'push', $11)
	`, id, p.TenantID, controlID, observed,
		[]byte(`{"source":"mcp.write_tool"}`), in.Result, []byte(payload),
		hash, "mcp/push_evidence", in.Kind,
		[]byte(`{"actor":"mcp.push_evidence","ai_assisted":true}`))
	if err != nil {
		return "", fmt.Errorf("apply push_evidence: insert: %w", err)
	}
	return id.String(), nil
}

// UpdateRiskTreatmentInput is the bounded shape the update_risk_treatment
// MCP tool ships. Accepter and AcceptedUntil (YYYY-MM-DD) are optional;
// when set they overwrite the risk's values, which is how a proposed
// acceptance moves to approved.
type UpdateRiskTreatmentInput struct {
	RiskID           string  `json:"risk_id"`
	Treatment        string  `json:"treatment"`
	AcceptanceStatus string  `json:"acceptance_status"`
	TreatmentOwner   string  `json:"treatment_owner"`
	Accepter         *string `json:"accepter,omitempty"`
	AcceptedUntil    *string `json:"accepted_until,omitempty"`
}

// ApplyUpdateRiskTreatment updates an existing risk's treatment + owner.
// The change runs through risk.ValidateTreatment against the risk's
// current links and side fields, so an approved proposal cannot set a
// treatment the REST path would reject (#1746).
func ApplyUpdateRiskTreatment(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
	var in UpdateRiskTreatmentInput
	if err := json.Unmarshal(p.ToolInput, &in); err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: parse input: %w", err)
	}
	if in.RiskID == "" || in.Treatment == "" {
		return "", fmt.Errorf("apply update_risk_treatment: risk_id and treatment are required")
	}
	riskID, err := uuid.Parse(in.RiskID)
	if err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: risk_id must be UUID: %w", err)
	}

	var (
		acceptedUntil       *time.Time
		accepter            string
		instrumentReference string
		controlCount        int
		actionPlanCount     int
	)
	err = tx.QueryRow(ctx, `
		SELECT r.accepted_until, r.accepter, r.instrument_reference,
			(SELECT count(*) FROM risk_control_links l
			  WHERE l.tenant_id = r.tenant_id AND l.risk_id = r.id),
			(SELECT count(*) FROM action_plan_risks apr
			  JOIN action_plans ap
			    ON ap.id = apr.action_plan_id AND ap.tenant_id = apr.tenant_id
			  WHERE apr.tenant_id = r.tenant_id AND apr.risk_id = r.id
			    AND ap.tombstoned_at IS NULL)
		FROM risks r
		WHERE r.tenant_id = $1 AND r.id = $2
	`, p.TenantID, riskID).Scan(&acceptedUntil, &accepter, &instrumentReference, &controlCount, &actionPlanCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("apply update_risk_treatment: risk %s not found in tenant", riskID)
	}
	if err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: read risk: %w", err)
	}
	if in.Accepter != nil {
		accepter = *in.Accepter
	}
	if in.AcceptedUntil != nil {
		t, perr := time.Parse("2006-01-02", *in.AcceptedUntil)
		if perr != nil {
			return "", fmt.Errorf("apply update_risk_treatment: accepted_until must be YYYY-MM-DD: %w", perr)
		}
		acceptedUntil = &t
	}

	treatment := dbx.RiskTreatment(in.Treatment)
	status := dbx.RiskAcceptanceStatus(in.AcceptanceStatus)
	if err := risk.ValidateTreatment(risk.TreatmentInput{
		Treatment:             treatment,
		AcceptanceStatus:      status,
		AcceptedUntilPresent:  acceptedUntil != nil,
		Accepter:              accepter,
		InstrumentReference:   instrumentReference,
		LinkedControlCount:    controlCount,
		LinkedActionPlanCount: actionPlanCount,
	}); err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE risks
		SET treatment = $3,
			acceptance_status = $4,
			treatment_owner = $5,
			accepter = $6,
			accepted_until = $7,
			updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, p.TenantID, riskID, treatment, risk.NullAcceptanceStatus(status), in.TreatmentOwner, accepter, acceptedUntil)
	if err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("apply update_risk_treatment: risk %s not found in tenant", riskID)
	}
	return riskID.String(), nil
}

// RegisterDefaultAppliers wires all four canonical appliers onto the
// store. Callers may register custom appliers (e.g., tests) BEFORE or
// AFTER this — later registrations win.
func RegisterDefaultAppliers(s *Store) *Store {
	return s.
		WithApplier(ToolCreateRisk, ApplyCreateRisk).
		WithApplier(ToolUpdateControlState, ApplyUpdateControlState).
		WithApplier(ToolPushEvidence, ApplyPushEvidence).
		WithApplier(ToolUpdateRiskTreatment, ApplyUpdateRiskTreatment)
}

// stateToEvidenceResult validates that a string is one of the four
// evidence_result enum values. Returns the canonical lowercased form.
func stateToEvidenceResult(s string) (string, error) {
	switch s {
	case "pass", "fail", "na", "inconclusive":
		return s, nil
	default:
		return "", fmt.Errorf("invalid evidence result/state %q (want pass|fail|na|inconclusive)", s)
	}
}
