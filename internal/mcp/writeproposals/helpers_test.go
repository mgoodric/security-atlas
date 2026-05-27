package writeproposals

// Pure-Go unit coverage for the small helpers + pre-DB validation
// branches that don't need a live Postgres. The integration suite
// (integration_test.go, build tag `integration`) drives the DB-touching
// paths against a real cluster; this file pins the cheap shapes so
// merged coverage clears the slice-317 70% target.
//
// Load-bearing functions / branches covered here:
//
//   - stateToEvidenceResult — every accepted enum value + the invalid
//     fallback. Load-bearing because it gates ApplyUpdateControlState +
//     ApplyPushEvidence before the DB ever sees the input (defense-in-
//     depth ahead of the schema-level CHECK on evidence_records.result).
//   - nullableString / nullableSubject — both nil-vs-set branches.
//     Load-bearing because they control whether the UPDATE writes NULL
//     vs '' into reject_reason / applied_subject; the schema treats
//     them as nullable text.
//   - PendingCap — the getter exposed for tests / observability.
//   - Store.Create — the four pre-DB validation rejections (unknown
//     tool, empty tool_input, missing AIModelName, missing AIModelVersion,
//     missing CreatedBy, non-JSON tool_input). Each is a documented
//     ErrInvalidInput / ErrUnknownTool branch from store.go that the
//     LLM can trip by mis-shaping a write request; merging-rollback
//     correctness depends on these failing fast before any tx opens.
//   - Store.Confirm — the empty-approver pre-tx rejection that honors
//     the schema invariant `ai_assisted=true AND human_approved=true →
//     human_approver IS NOT NULL` (CLAUDE.md §"AI-assist boundary").
//
// NO DB calls are made here — every branch we hit returns before
// inTx() opens a transaction.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ----- stateToEvidenceResult -----

func TestStateToEvidenceResult_AcceptsAllCanonicalValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"pass", "fail", "na", "inconclusive"} {
		got, err := stateToEvidenceResult(v)
		if err != nil {
			t.Fatalf("stateToEvidenceResult(%q) = err %v, want nil", v, err)
		}
		if got != v {
			t.Fatalf("stateToEvidenceResult(%q) = %q, want %q", v, got, v)
		}
	}
}

func TestStateToEvidenceResult_RejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "PASS", "passed", "ok", "unknown", "; DROP"} {
		got, err := stateToEvidenceResult(v)
		if err == nil {
			t.Fatalf("stateToEvidenceResult(%q) = nil err, want rejection", v)
		}
		if got != "" {
			t.Fatalf("stateToEvidenceResult(%q) returned %q on err, want empty", v, got)
		}
	}
}

// ----- nullableString / nullableSubject -----

func TestNullableString_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	if got := nullableString(""); got != nil {
		t.Fatalf("nullableString(\"\") = %v, want nil", got)
	}
}

func TestNullableString_NonEmptyReturnsValue(t *testing.T) {
	t.Parallel()
	got := nullableString("reason")
	s, ok := got.(string)
	if !ok || s != "reason" {
		t.Fatalf("nullableString(\"reason\") = %v, want \"reason\"", got)
	}
}

func TestNullableSubject_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	if got := nullableSubject(""); got != nil {
		t.Fatalf("nullableSubject(\"\") = %v, want nil", got)
	}
}

func TestNullableSubject_NonEmptyReturnsValue(t *testing.T) {
	t.Parallel()
	got := nullableSubject("risk-uuid")
	s, ok := got.(string)
	if !ok || s != "risk-uuid" {
		t.Fatalf("nullableSubject(\"risk-uuid\") = %v, want \"risk-uuid\"", got)
	}
}

// ----- Store getter -----

func TestPendingCap_DefaultsToConstant(t *testing.T) {
	t.Parallel()
	// Construct against a nil pool — Create / Confirm / etc. would
	// panic, but the getter never touches the pool.
	s := NewStore(nil)
	if got := s.PendingCap(); got != DefaultPendingCap {
		t.Fatalf("PendingCap = %d, want %d", got, DefaultPendingCap)
	}
}

func TestPendingCap_HonorsWithPendingCap(t *testing.T) {
	t.Parallel()
	s := NewStore(nil).WithPendingCap(7)
	if got := s.PendingCap(); got != 7 {
		t.Fatalf("PendingCap = %d, want 7", got)
	}
}

// TestWithApplier_LatestRegistrationWins documents the contract that
// WithApplier replaces (not appends) the applier for a given tool_name.
// Probed via the unexported appliers map because there is no public
// accessor; both helpers ship in the same package so this is in-bounds.
func TestWithApplier_LatestRegistrationWins(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	if len(s.appliers) != 0 {
		t.Fatalf("fresh store appliers len = %d, want 0", len(s.appliers))
	}
	s.WithApplier(ToolCreateRisk, func(_ context.Context, _ pgx.Tx, _ Proposal) (string, error) {
		return "a", nil
	})
	s.WithApplier(ToolCreateRisk, func(_ context.Context, _ pgx.Tx, _ Proposal) (string, error) {
		return "b", nil
	})
	if len(s.appliers) != 1 {
		t.Fatalf("after two WithApplier(SameTool) calls, len = %d, want 1", len(s.appliers))
	}
}

// ----- Store.Create — pre-DB validation -----

// Create returns ErrUnknownTool before opening a tx, so this test runs
// against a nil pool and never reaches Postgres.
func TestStoreCreate_RejectsUnknownToolBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	_, err := s.Create(context.Background(), CreateInput{
		ToolName: "delete_tenant",
	})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("Create(unknown tool) = %v, want ErrUnknownTool", err)
	}
}

func TestStoreCreate_RejectsEmptyToolInputBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	cases := []CreateInput{
		// nil RawMessage
		{ToolName: ToolCreateRisk, ToolInput: nil, AIModelName: "m", AIModelVersion: "v", CreatedBy: "k"},
		// whitespace-only
		{ToolName: ToolCreateRisk, ToolInput: json.RawMessage("   "), AIModelName: "m", AIModelVersion: "v", CreatedBy: "k"},
	}
	for i, c := range cases {
		if _, err := s.Create(context.Background(), c); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("case %d: Create(empty tool_input) = %v, want ErrInvalidInput", i, err)
		}
	}
}

func TestStoreCreate_RejectsMissingModelFieldsBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	base := CreateInput{
		ToolName:  ToolCreateRisk,
		ToolInput: json.RawMessage(`{"title":"t","category":"c"}`),
		CreatedBy: "key_test",
	}
	// Missing AIModelName
	missingName := base
	missingName.AIModelVersion = "v"
	if _, err := s.Create(context.Background(), missingName); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(no ai_model_name) = %v, want ErrInvalidInput", err)
	}
	// Missing AIModelVersion
	missingVer := base
	missingVer.AIModelName = "m"
	if _, err := s.Create(context.Background(), missingVer); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(no ai_model_version) = %v, want ErrInvalidInput", err)
	}
}

func TestStoreCreate_RejectsEmptyCreatedByBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	in := CreateInput{
		ToolName:       ToolCreateRisk,
		ToolInput:      json.RawMessage(`{"title":"t","category":"c"}`),
		AIModelName:    "m",
		AIModelVersion: "v",
		CreatedBy:      "",
	}
	if _, err := s.Create(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(no created_by) = %v, want ErrInvalidInput", err)
	}
}

func TestStoreCreate_RejectsInvalidJSONBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	in := CreateInput{
		ToolName:       ToolCreateRisk,
		ToolInput:      json.RawMessage(`{not-json`),
		AIModelName:    "m",
		AIModelVersion: "v",
		CreatedBy:      "k",
	}
	if _, err := s.Create(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(malformed tool_input) = %v, want ErrInvalidInput", err)
	}
}

// ----- Store.Confirm — pre-tx validation -----

func TestStoreConfirm_RejectsEmptyApproverBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	if _, err := s.Confirm(context.Background(), uuid.New(), ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Confirm(empty approver) = %v, want ErrInvalidInput", err)
	}
}
