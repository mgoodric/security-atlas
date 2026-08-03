// helpers_test.go — pure-Go unit tests for the OE-663 handler package's
// pre-DB branches (the slice-290 pattern: validators and wire mappers
// exercised with fast t.Parallel() table tests, no Postgres, no build
// tag). The Postgres-backed lifecycle lives in integration_test.go.

package personnelsecurity

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
)

func TestParseListFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                  string
		kind, status, overdue string
		want                  ListFilter
		wantErr               bool
	}{
		{name: "all empty", want: ListFilter{}},
		{name: "kind onboarding", kind: "onboarding", want: ListFilter{Kind: "onboarding"}},
		{name: "kind offboarding", kind: "offboarding", want: ListFilter{Kind: "offboarding"}},
		{name: "kind unknown", kind: "sabbatical", wantErr: true},
		{name: "status open", status: "open", want: ListFilter{Status: "open"}},
		{name: "status completed", status: "completed", want: ListFilter{Status: "completed"}},
		{name: "status unknown", status: "stalled", wantErr: true},
		{name: "overdue true", overdue: "true", want: ListFilter{OverdueOnly: true}},
		{name: "overdue false", overdue: "false", want: ListFilter{}},
		{name: "overdue junk", overdue: "yes", wantErr: true},
		{
			name: "all compose", kind: "offboarding", status: "open", overdue: "true",
			want: ListFilter{Kind: "offboarding", Status: "open", OverdueOnly: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseListFilter(tc.kind, tc.status, tc.overdue)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseListFilter(%q,%q,%q): want error, got %+v", tc.kind, tc.status, tc.overdue, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseListFilter: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseListFilter = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestManualInputFrom(t *testing.T) {
	t.Parallel()
	controlID := uuid.New()
	due := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	in, err := manualInputFrom(createReq{
		Kind:             "onboarding",
		PersonExternalID: "worker-1",
		ControlID:        controlID.String(),
		DueAt:            &due,
	}, "caller-cred")
	if err != nil {
		t.Fatalf("manualInputFrom: %v", err)
	}
	if in.CreatedBy != "caller-cred" {
		t.Fatalf("CreatedBy = %q, want caller fallback", in.CreatedBy)
	}
	if in.ControlID != controlID || !in.DueAt.Equal(due) {
		t.Fatalf("ControlID/DueAt = %s/%s", in.ControlID, in.DueAt)
	}

	in, err = manualInputFrom(createReq{Kind: "offboarding", PersonExternalID: "w", CreatedBy: "  security-admin  "}, "caller-cred")
	if err != nil {
		t.Fatalf("manualInputFrom explicit creator: %v", err)
	}
	if in.CreatedBy != "security-admin" {
		t.Fatalf("CreatedBy = %q, want explicit body value trimmed", in.CreatedBy)
	}
	if in.ControlID != uuid.Nil {
		t.Fatalf("ControlID = %s, want Nil when omitted", in.ControlID)
	}

	if _, err := manualInputFrom(createReq{Kind: "onboarding", ControlID: "not-a-uuid"}, ""); err == nil {
		t.Fatal("manualInputFrom: want error for malformed control_id")
	}
}

func TestChecklistWireFrom(t *testing.T) {
	t.Parallel()
	checklistID := uuid.New()
	itemID := uuid.New()
	evidenceID := uuid.New()
	controlID := uuid.New()
	completedAt := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)

	w := checklistWireFrom(personnelsecurity.Checklist{
		ID:               checklistID,
		Kind:             personnelsecurity.WorkflowOnboarding,
		Source:           personnelsecurity.SourceManual,
		PersonExternalID: "worker-1",
		ControlID:        controlID,
		DueAt:            completedAt.Add(24 * time.Hour),
		Status:           "open",
		Items: []personnelsecurity.Item{
			{ID: itemID, ChecklistID: checklistID, Slug: "provision_access", SortOrder: 1,
				CompletedAt: completedAt, CompletedBy: "admin", EvidenceRecordID: evidenceID},
			{ID: uuid.New(), ChecklistID: checklistID, Slug: "manager_approval"},
		},
	})
	if w.ControlID == nil || *w.ControlID != controlID.String() {
		t.Fatalf("ControlID wire = %v", w.ControlID)
	}
	if len(w.Items) != 2 {
		t.Fatalf("items = %d", len(w.Items))
	}
	done, open := w.Items[0], w.Items[1]
	if done.CompletedAt == nil || !done.CompletedAt.Equal(completedAt) || done.EvidenceRecordID == nil || *done.EvidenceRecordID != evidenceID.String() {
		t.Fatalf("completed item wire = %+v", done)
	}
	if open.CompletedAt != nil || open.EvidenceRecordID != nil {
		t.Fatalf("open item must omit completion fields: %+v", open)
	}

	// A checklist without a control binding omits control_id, and Items
	// is an empty array (never null) on the wire.
	w = checklistWireFrom(personnelsecurity.Checklist{ID: checklistID, Status: "open"})
	if w.ControlID != nil {
		t.Fatalf("ControlID = %v, want nil", w.ControlID)
	}
	if w.Items == nil || len(w.Items) != 0 {
		t.Fatalf("Items = %#v, want empty non-nil slice", w.Items)
	}
}
