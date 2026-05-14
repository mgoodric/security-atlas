// Unit tests for the OSCAL audit-narrative emission (AC-7). The emission
// functions are pure -- no DB -- so these run without the `integration`
// build tag.

package decision

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func mkDecision(optOut bool, revisit *time.Time) Decision {
	return Decision{
		ID:                   uuid.New(),
		DecisionID:           "DL-2026-05-14-0007",
		Title:                "Ship MVP, defer SAML to v1.2",
		DecisionMaker:        "alice@example.com",
		DecidedAt:            time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC),
		RevisitBy:            revisit,
		AuditNarrativeOptOut: optOut,
	}
}

func TestEmitRemarkText_Format(t *testing.T) {
	revisit := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	d := mkDecision(false, &revisit)
	got := EmitRemarkText(d, []string{"RSK-2", "RSK-1"})
	// risk ids render sorted + comma-separated; decided_at + revisit_by as
	// YYYY-MM-DD dates.
	want := "[DL-2026-05-14-0007] Ship MVP, defer SAML to v1.2 (alice@example.com, 2026-05-14) — Linked risks: RSK-1, RSK-2. Revisit: 2026-07-01."
	if got != want {
		t.Fatalf("EmitRemarkText mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitRemarkText_NoRisksNoRevisit(t *testing.T) {
	d := mkDecision(false, nil)
	got := EmitRemarkText(d, nil)
	want := "[DL-2026-05-14-0007] Ship MVP, defer SAML to v1.2 (alice@example.com, 2026-05-14) — Linked risks: none. Revisit: n/a."
	if got != want {
		t.Fatalf("EmitRemarkText mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitRemark_OptOutExcluded(t *testing.T) {
	d := mkDecision(true, nil)
	if _, ok := EmitRemark(d, []uuid.UUID{uuid.New()}, nil); ok {
		t.Fatal("expected opted-out decision to be excluded from narrative")
	}
}

func TestEmitRemark_NoLinkedControlsExcluded(t *testing.T) {
	d := mkDecision(false, nil)
	if _, ok := EmitRemark(d, nil, nil); ok {
		t.Fatal("expected decision with no linked controls to be excluded")
	}
}

func TestEmitRemark_EmittedWhenLinkedAndOptedIn(t *testing.T) {
	d := mkDecision(false, nil)
	ctrl := uuid.New()
	remark, ok := EmitRemark(d, []uuid.UUID{ctrl}, []string{"RSK-9"})
	if !ok {
		t.Fatal("expected linked, opted-in decision to emit a remark")
	}
	if remark.DecisionID != d.DecisionID {
		t.Fatalf("remark decision id mismatch: %q", remark.DecisionID)
	}
	if len(remark.ControlIDs) != 1 || remark.ControlIDs[0] != ctrl {
		t.Fatalf("remark control ids mismatch: %+v", remark.ControlIDs)
	}
}

func TestEmitRemarks_DropsExcluded(t *testing.T) {
	ctrl := uuid.New()
	inputs := []NarrativeInput{
		{Decision: mkDecision(false, nil), LinkedControlIDs: []uuid.UUID{ctrl}},      // emitted
		{Decision: mkDecision(true, nil), LinkedControlIDs: []uuid.UUID{uuid.New()}}, // opted out
		{Decision: mkDecision(false, nil), LinkedControlIDs: nil},                    // no controls
	}
	out := EmitRemarks(inputs)
	if len(out) != 1 {
		t.Fatalf("expected 1 emitted remark, got %d", len(out))
	}
}
