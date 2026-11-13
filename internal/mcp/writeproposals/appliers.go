// appliers.go — the four canonical Applier implementations the MCP
// write tools dispatch through. Each applier runs inside the proposal's
// confirm transaction so a downstream-write failure rolls the proposal
// back to state=ai_proposed.
//
// The two evidence appliers (update_control_state, push_evidence) never
// write evidence_records themselves. They hand the record to
// ingest.Service.Process, the one ingestion stage every other evidence
// path uses (#1751), so an approved proposal gets the same observed_at
// skew check, schema validation, credential kind scope, provenance and
// evidence_audit_log row as a REST or gRPC push.
//
// Risk applier SQL is HAND-WRITTEN here rather than routed through the
// sqlc-wrapped domain stores. Two reasons:
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
// The risk appliers run as atlas_app, under the proposal's tenant GUC
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
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// CreateRiskInput is the bounded shape the create_risk MCP tool ships
// in tool_input. A subset of risk.CreateInput — we accept only the
// fields the LLM can reasonably propose (title, description, category,
// methodology defaults, treatment with safe defaults). Score blocks are
// JSON-RawMessage so the LLM can supply a 5x5 matrix or a NIST 800-30
// envelope; defense-in-depth CHECK constraints on the risks table
// catch malformed inputs.
type CreateRiskInput struct {
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Category       string          `json:"category"`
	Methodology    string          `json:"methodology,omitempty"`
	InherentScore  json.RawMessage `json:"inherent_score,omitempty"`
	Treatment      string          `json:"treatment,omitempty"`
	TreatmentOwner string          `json:"treatment_owner,omitempty"`
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
	treatment := in.Treatment
	if treatment == "" {
		treatment = "avoid" // safe default — no required side fields
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
			accepted_until, accepter, instrument_reference
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '{}'::jsonb, NULL, '', '')
	`, id, p.TenantID, in.Title, in.Description, in.Category, methodology,
		[]byte(inherent), treatment, in.TreatmentOwner)
	if err != nil {
		return "", fmt.Errorf("apply create_risk: insert: %w", err)
	}
	return id.String(), nil
}

// EvidenceProcessor is the ingestion stage the evidence appliers hand
// their record to. *ingest.Service satisfies it.
type EvidenceProcessor interface {
	Process(ctx context.Context, rec *evidencev1.EvidenceRecord, cred credstore.Credential) (ingest.Receipt, ingest.Decision, error)
}

// ErrIngestUnavailable is returned by the evidence appliers when no ingest
// service was wired. They fail closed rather than write the ledger
// directly.
var ErrIngestUnavailable = errors.New("ingest service not configured")

// The override evidence kind, registered in the platform schema registry
// (internal/api/schemaregistry/schemas/manual.control_state_override/).
const (
	ControlStateOverrideKind    = "manual.control_state_override.v1"
	ControlStateOverrideVersion = "1.0.0"
)

// DefaultPushSchemaVersion is the schema version a push_evidence proposal
// is validated against when it names none.
const DefaultPushSchemaVersion = "1.0.0"

// UpdateControlStateInput is the bounded shape the update_control_state
// MCP tool ships. Constitutional invariant #2 forbids the AI from
// directly writing control_evaluations; instead we synthesise an
// evidence record with kind=`manual.control_state_override.v1` so
// the eval engine sees a valid evidence source and the override flows
// through the existing pipeline.
type UpdateControlStateInput struct {
	ControlID string `json:"control_id"`
	State     string `json:"state"`
	Rationale string `json:"rationale"`
}

// NewUpdateControlStateApplier returns the update_control_state applier:
// it ingests a manual override evidence record through ing and returns
// the record id for applied_subject.
func NewUpdateControlStateApplier(ing EvidenceProcessor) Applier {
	return func(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
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
		result, err := stateToProtoResult(in.State)
		if err != nil {
			return "", err
		}
		payload := map[string]any{
			"override_state": in.State,
			"ai_proposed":    true,
		}
		if in.Rationale != "" {
			payload["rationale"] = in.Rationale
		}
		rec, err := proposalRecord(p, ToolUpdateControlState, ControlStateOverrideKind,
			ControlStateOverrideVersion, controlID, time.Now().UTC(), result, payload)
		if err != nil {
			return "", fmt.Errorf("apply update_control_state: %w", err)
		}
		id, err := ingestProposalRecord(ctx, ing, tx, p, rec)
		if err != nil {
			return "", fmt.Errorf("apply update_control_state: %w", err)
		}
		return id, nil
	}
}

// PushEvidenceInput is the bounded shape the push_evidence MCP tool
// ships. Direct evidence push — same shape as the Evidence SDK (slice
// 003) but mediated by the HITL approval flow because the LLM is the
// origin.
type PushEvidenceInput struct {
	ControlID     string          `json:"control_id"`
	Kind          string          `json:"kind"`
	SchemaVersion string          `json:"schema_version,omitempty"`
	Result        string          `json:"result"`
	ObservedAt    time.Time       `json:"observed_at,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// NewPushEvidenceApplier returns the push_evidence applier. A proposal
// whose observed_at is outside ingest.MaxObservedAtSkew fails at confirm
// with ingest.ErrObservedAtSkew and leaves a rejected_observed_at_skew
// row in evidence_audit_log, exactly as the same record pushed over REST.
func NewPushEvidenceApplier(ing EvidenceProcessor) Applier {
	return func(ctx context.Context, tx pgx.Tx, p Proposal) (string, error) {
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
		result, err := stateToProtoResult(in.Result)
		if err != nil {
			return "", err
		}
		observed := in.ObservedAt
		if observed.IsZero() {
			observed = time.Now().UTC()
		}
		version := in.SchemaVersion
		if version == "" {
			version = DefaultPushSchemaVersion
		}
		payload := map[string]any{}
		if len(in.Payload) > 0 {
			if err := json.Unmarshal(in.Payload, &payload); err != nil {
				return "", fmt.Errorf("apply push_evidence: payload must be a JSON object: %w", err)
			}
		}
		rec, err := proposalRecord(p, ToolPushEvidence, in.Kind, version, controlID, observed, result, payload)
		if err != nil {
			return "", fmt.Errorf("apply push_evidence: %w", err)
		}
		id, err := ingestProposalRecord(ctx, ing, tx, p, rec)
		if err != nil {
			return "", fmt.Errorf("apply push_evidence: %w", err)
		}
		return id, nil
	}
}

// proposalRecord builds the wire record for an approved proposal.
//
// The idempotency key derives from the proposal id. Ingest writes the
// record in its own transaction, not the confirm tx (the audit row of a
// rejection must survive the confirm rollback), so the key is how a
// confirm that fails after ingest and is retried finds the first record
// instead of writing a second one; see ingestProposalRecord.
func proposalRecord(p Proposal, tool, kind, version string, controlID uuid.UUID, observed time.Time,
	result evidencev1.Result, payload map[string]any) (*evidencev1.EvidenceRecord, error) {
	st, err := structpb.NewStruct(payload)
	if err != nil {
		return nil, fmt.Errorf("build payload: %w", err)
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: "mcp-proposal:" + p.ID.String(),
		EvidenceKind:   kind,
		SchemaVersion:  version,
		ControlId:      controlID.String(),
		Scope: []*evidencev1.ScopeDimension{{
			Key:    "mcp_tool",
			Values: []string{tool},
		}},
		ObservedAt: timestamppb.New(observed),
		Result:     result,
		Payload:    st,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "system",
			ActorId:   "mcp." + tool,
			SessionId: p.ID.String(),
		},
	}, nil
}

// ingestProposalRecord runs rec through ing under the approving
// operator's credential, read from ctx. That credential must belong to the
// proposal's tenant, and its kind scope applies as on any other push.
//
// An approver whose credential carries a ScopePredicate is refused
// (ingest.ErrScopeViolation, 403 scope_violation at the handler): the
// record's only scope dimension is the server-set mcp_tool, which no
// predicate such as cloud_account=... can match. Confirming MCP evidence
// needs an approver credential without a scope predicate.
//
// A record already ingested for this proposal (an earlier confirm whose
// state flip rolled back after ingest committed) is returned as is. The
// record's observed_at may default to the apply time, so re-ingesting
// would hash differently and trip ingest's idempotency-mismatch reject.
func ingestProposalRecord(ctx context.Context, ing EvidenceProcessor, tx pgx.Tx, p Proposal, rec *evidencev1.EvidenceRecord) (string, error) {
	if ing == nil {
		return "", ErrIngestUnavailable
	}
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok || cred.TenantID != p.TenantID.String() {
		return "", fmt.Errorf("%w: approver credential missing or outside the proposal's tenant", ErrInvalidInput)
	}
	var existing uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM evidence_records
		WHERE tenant_id = $1 AND idempotency_key = $2
	`, p.TenantID, rec.IdempotencyKey).Scan(&existing)
	switch {
	case err == nil:
		return existing.String(), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("look up prior ingest: %w", err)
	}
	receipt, _, err := ing.Process(ctx, rec, cred)
	if err != nil {
		return "", err
	}
	return receipt.RecordID, nil
}

// UpdateRiskTreatmentInput is the bounded shape the update_risk_treatment
// MCP tool ships.
type UpdateRiskTreatmentInput struct {
	RiskID         string `json:"risk_id"`
	Treatment      string `json:"treatment"`
	TreatmentOwner string `json:"treatment_owner"`
}

// ApplyUpdateRiskTreatment updates an existing risk's treatment + owner.
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
	tag, err := tx.Exec(ctx, `
		UPDATE risks
		SET treatment = $3,
			treatment_owner = $4,
			updated_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, p.TenantID, riskID, in.Treatment, in.TreatmentOwner)
	if err != nil {
		return "", fmt.Errorf("apply update_risk_treatment: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", fmt.Errorf("apply update_risk_treatment: risk %s not found in tenant", riskID)
	}
	return riskID.String(), nil
}

// RegisterDefaultAppliers wires all four canonical appliers onto the
// store. The evidence appliers ingest through ing — push_evidence under
// ingestion_path=push, update_control_state under manual_upload — and a
// nil ing makes them fail closed with ErrIngestUnavailable. Callers may
// register custom appliers (e.g., tests) BEFORE or AFTER this — later
// registrations win.
func RegisterDefaultAppliers(s *Store, ing *ingest.Service) *Store {
	var push, manual EvidenceProcessor
	if ing != nil {
		push = ing.WithPath("push")
		manual = ing.WithPath("manual_upload")
	}
	return s.
		WithApplier(ToolCreateRisk, ApplyCreateRisk).
		WithApplier(ToolUpdateControlState, NewUpdateControlStateApplier(manual)).
		WithApplier(ToolPushEvidence, NewPushEvidenceApplier(push)).
		WithApplier(ToolUpdateRiskTreatment, ApplyUpdateRiskTreatment)
}

// stateToProtoResult validates that a string is one of the four
// evidence_result enum values and maps it to the wire enum.
func stateToProtoResult(s string) (evidencev1.Result, error) {
	switch s {
	case "pass":
		return evidencev1.Result_RESULT_PASS, nil
	case "fail":
		return evidencev1.Result_RESULT_FAIL, nil
	case "na":
		return evidencev1.Result_RESULT_NA, nil
	case "inconclusive":
		return evidencev1.Result_RESULT_INCONCLUSIVE, nil
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED,
			fmt.Errorf("invalid evidence result/state %q (want pass|fail|na|inconclusive)", s)
	}
}
