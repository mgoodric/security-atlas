package writeproposals

// Pure-Go unit coverage for the four Applier implementations' pre-DB
// validation paths. Each applier:
//
//   1. Unmarshals the proposal's tool_input JSON into a bounded shape.
//   2. Validates the required fields + the UUID / enum constraints.
//   3. Only THEN executes the canonical INSERT/UPDATE inside the
//      shared proposal-confirm transaction.
//
// Steps 1-2 never touch `tx`, so we can drive them with a nil tx +
// a minimal Proposal. Step 3 (the DB writes) is covered by the
// integration suite — confirm via slice 173 store_test.go cross-
// reference.
//
// Load-bearing functions / branches covered here:
//
//   - ApplyCreateRisk         — invalid JSON, empty title, empty category
//   - ApplyUpdateControlState — invalid JSON, missing control_id, missing
//                               state, non-UUID control_id, invalid state
//   - ApplyPushEvidence       — invalid JSON, missing required fields,
//                               non-UUID control_id, invalid result enum
//   - ApplyUpdateRiskTreatment— invalid JSON, missing risk_id, missing
//                               treatment, non-UUID risk_id
//   - RegisterDefaultAppliers — registers all four tool names
//
// The shipped tests on the canonical write paths (with a real tx) live in
// integration_test.go and exercise the happy + DB-error branches.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ----- ApplyCreateRisk -----

func TestApplyCreateRisk_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolName: ToolCreateRisk, ToolInput: json.RawMessage(`{not-json`)}
	subj, err := ApplyCreateRisk(context.Background(), nil, p)
	if err == nil {
		t.Fatal("ApplyCreateRisk(bad JSON) = nil err, want parse error")
	}
	if !strings.Contains(err.Error(), "parse input") {
		t.Fatalf("err = %v, want parse-input wrap", err)
	}
	if subj != "" {
		t.Fatalf("subj = %q on err, want empty", subj)
	}
}

func TestApplyCreateRisk_RejectsEmptyTitle(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolCreateRisk,
		ToolInput: json.RawMessage(`{"title":"","category":"operational"}`),
	}
	_, err := ApplyCreateRisk(context.Background(), nil, p)
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("ApplyCreateRisk(empty title) = %v, want title-required", err)
	}
}

func TestApplyCreateRisk_RejectsEmptyCategory(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolCreateRisk,
		ToolInput: json.RawMessage(`{"title":"DC outage","category":""}`),
	}
	_, err := ApplyCreateRisk(context.Background(), nil, p)
	if err == nil || !strings.Contains(err.Error(), "category is required") {
		t.Fatalf("ApplyCreateRisk(empty category) = %v, want category-required", err)
	}
}

// ----- ApplyUpdateControlState -----

func TestApplyUpdateControlState_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolName: ToolUpdateControlState, ToolInput: json.RawMessage(`}}`)}
	if _, err := ApplyUpdateControlState(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "parse input") {
		t.Fatalf("ApplyUpdateControlState(bad JSON) = %v, want parse-input err", err)
	}
}

func TestApplyUpdateControlState_RejectsMissingControlID(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateControlState,
		ToolInput: json.RawMessage(`{"control_id":"","state":"pass"}`),
	}
	if _, err := ApplyUpdateControlState(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "control_id and state are required") {
		t.Fatalf("err = %v, want control_id+state-required", err)
	}
}

func TestApplyUpdateControlState_RejectsMissingState(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateControlState,
		ToolInput: json.RawMessage(`{"control_id":"a","state":""}`),
	}
	if _, err := ApplyUpdateControlState(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "control_id and state are required") {
		t.Fatalf("err = %v, want control_id+state-required", err)
	}
}

func TestApplyUpdateControlState_RejectsNonUUIDControlID(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateControlState,
		ToolInput: json.RawMessage(`{"control_id":"not-a-uuid","state":"pass"}`),
	}
	if _, err := ApplyUpdateControlState(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "control_id must be UUID") {
		t.Fatalf("err = %v, want UUID-parse error", err)
	}
}

func TestApplyUpdateControlState_RejectsInvalidState(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateControlState,
		ToolInput: json.RawMessage(`{"control_id":"11111111-1111-1111-1111-111111111111","state":"PASS"}`),
	}
	if _, err := ApplyUpdateControlState(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "invalid evidence result/state") {
		t.Fatalf("err = %v, want invalid-state err", err)
	}
}

// ----- ApplyPushEvidence -----

func TestApplyPushEvidence_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolName: ToolPushEvidence, ToolInput: json.RawMessage(`[`)}
	if _, err := ApplyPushEvidence(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "parse input") {
		t.Fatalf("err = %v, want parse-input err", err)
	}
}

func TestApplyPushEvidence_RejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"control_id":"","kind":"k","result":"pass"}`,
		`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"","result":"pass"}`,
		`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"k","result":""}`,
	}
	for i, body := range cases {
		p := Proposal{ToolName: ToolPushEvidence, ToolInput: json.RawMessage(body)}
		if _, err := ApplyPushEvidence(context.Background(), nil, p); err == nil ||
			!strings.Contains(err.Error(), "control_id, kind, result are required") {
			t.Fatalf("case %d: err = %v, want required-fields err", i, err)
		}
	}
}

func TestApplyPushEvidence_RejectsNonUUIDControlID(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolPushEvidence,
		ToolInput: json.RawMessage(`{"control_id":"x","kind":"k","result":"pass"}`),
	}
	if _, err := ApplyPushEvidence(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "control_id must be UUID") {
		t.Fatalf("err = %v, want UUID-parse err", err)
	}
}

func TestApplyPushEvidence_RejectsInvalidResultEnum(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolPushEvidence,
		ToolInput: json.RawMessage(`{"control_id":"11111111-1111-1111-1111-111111111111","kind":"k","result":"unknown"}`),
	}
	if _, err := ApplyPushEvidence(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "invalid evidence result/state") {
		t.Fatalf("err = %v, want invalid-result err", err)
	}
}

// ----- ApplyUpdateRiskTreatment -----

func TestApplyUpdateRiskTreatment_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolName: ToolUpdateRiskTreatment, ToolInput: json.RawMessage(`null`)}
	if _, err := ApplyUpdateRiskTreatment(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "risk_id and treatment are required") {
		// `null` unmarshals to zero-value struct (not parse error), so
		// the next branch — required-fields — should fire.
		t.Fatalf("err = %v, want required-fields err for null body", err)
	}
}

func TestApplyUpdateRiskTreatment_RejectsParseError(t *testing.T) {
	t.Parallel()
	p := Proposal{ToolName: ToolUpdateRiskTreatment, ToolInput: json.RawMessage(`{bad`)}
	if _, err := ApplyUpdateRiskTreatment(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "parse input") {
		t.Fatalf("err = %v, want parse-input err", err)
	}
}

func TestApplyUpdateRiskTreatment_RejectsMissingRiskID(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateRiskTreatment,
		ToolInput: json.RawMessage(`{"risk_id":"","treatment":"mitigate"}`),
	}
	if _, err := ApplyUpdateRiskTreatment(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "risk_id and treatment are required") {
		t.Fatalf("err = %v, want required-fields err", err)
	}
}

func TestApplyUpdateRiskTreatment_RejectsMissingTreatment(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateRiskTreatment,
		ToolInput: json.RawMessage(`{"risk_id":"11111111-1111-1111-1111-111111111111","treatment":""}`),
	}
	if _, err := ApplyUpdateRiskTreatment(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "risk_id and treatment are required") {
		t.Fatalf("err = %v, want required-fields err", err)
	}
}

func TestApplyUpdateRiskTreatment_RejectsNonUUIDRiskID(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolUpdateRiskTreatment,
		ToolInput: json.RawMessage(`{"risk_id":"x","treatment":"mitigate"}`),
	}
	if _, err := ApplyUpdateRiskTreatment(context.Background(), nil, p); err == nil ||
		!strings.Contains(err.Error(), "risk_id must be UUID") {
		t.Fatalf("err = %v, want UUID-parse err", err)
	}
}

// ----- RegisterDefaultAppliers -----

func TestRegisterDefaultAppliers_RegistersAllFourTools(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	RegisterDefaultAppliers(s)
	for _, want := range []string{
		ToolCreateRisk, ToolUpdateControlState, ToolPushEvidence, ToolUpdateRiskTreatment,
	} {
		if _, ok := s.appliers[want]; !ok {
			t.Errorf("RegisterDefaultAppliers did not wire %q", want)
		}
	}
	if len(s.appliers) != 4 {
		t.Fatalf("appliers count = %d, want 4", len(s.appliers))
	}
}
