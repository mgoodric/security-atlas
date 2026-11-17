package approvals

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

const (
	requester = "11111111-1111-1111-1111-111111111111"
	other     = "22222222-2222-2222-2222-222222222222"
)

var (
	asOther     = approval.Approver{UserID: other, CredentialID: "cred-other"}
	asRequester = approval.Approver{UserID: requester, CredentialID: "cred-requester"}
	asMachine   = approval.Approver{CredentialID: "key-machine"}
)

func pgID(s string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true} }

func strp(s string) *string { return &s }

// filterCase is one caller against one pending row: listed or not, and
// marked self_approval or not.
type filterCase struct {
	name      string
	approver  approval.Approver
	allowSelf bool
	listed    bool
	self      bool
}

// The rule every type shares (#2101): a second person sees it; the
// requester sees it only with Allow self-approval on, marked; a machine
// never does.
var shared = []filterCase{
	{"second person", asOther, false, true, false},
	{"requester, setting off", asRequester, false, false, false},
	{"requester, setting on", asRequester, true, true, true},
	{"machine, setting on", asMachine, true, false, false},
}

func assertOne(t *testing.T, items []Item, tc filterCase, wantType string) {
	t.Helper()
	if !tc.listed {
		if len(items) != 0 {
			t.Fatalf("listed %d items, want none", len(items))
		}
		return
	}
	if len(items) != 1 {
		t.Fatalf("listed %d items, want 1", len(items))
	}
	if items[0].Type != wantType || items[0].SelfApproval != tc.self {
		t.Fatalf("item = %+v, want type %s self_approval %v", items[0], wantType, tc.self)
	}
}

func TestPendingExceptions(t *testing.T) {
	t.Parallel()
	ctl := uuid.New()
	rows := []dbx.Exception{
		{ID: pgID(uuid.NewString()), ControlID: pgtype.UUID{Bytes: ctl, Valid: true}, RequestedBy: requester, Status: "requested"},
		{ID: pgID(uuid.NewString()), RequestedBy: requester, Status: "approved"},
		{ID: pgID(uuid.NewString()), RequestedBy: requester, Status: "active"},
	}
	for _, tc := range shared {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PendingExceptions(rows, map[uuid.UUID]string{ctl: "MFA everywhere"}, tc.approver, tc.allowSelf)
			assertOne(t, got, tc, TypeException)
			if tc.listed && got[0].Title != "MFA everywhere" {
				t.Fatalf("title = %q, want the control's", got[0].Title)
			}
		})
	}
}

func TestPendingRiskAcceptances(t *testing.T) {
	t.Parallel()
	proposed := dbx.RiskAcceptanceStatusProposed
	approved := dbx.RiskAcceptanceStatusApproved
	rows := []dbx.Risk{
		{ID: pgID(uuid.NewString()), Title: "open", Status: dbx.RiskStatusOpen, AcceptanceStatus: &proposed, AcceptanceProposedBy: requester},
		// A closed risk's acceptance cannot be approved until reopened.
		{ID: pgID(uuid.NewString()), Title: "closed", Status: dbx.RiskStatusClosed, AcceptanceStatus: &proposed, AcceptanceProposedBy: requester},
		{ID: pgID(uuid.NewString()), Title: "approved", Status: dbx.RiskStatusOpen, AcceptanceStatus: &approved, AcceptanceProposedBy: requester},
		{ID: pgID(uuid.NewString()), Title: "untreated", Status: dbx.RiskStatusOpen},
		// No recorded proposer: the approve handler refuses it (#2371).
		{ID: pgID(uuid.NewString()), Title: "no proposer", Status: dbx.RiskStatusOpen, AcceptanceStatus: &proposed},
	}
	for _, tc := range shared {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PendingRiskAcceptances(rows, tc.approver, tc.allowSelf)
			assertOne(t, got, tc, TypeRiskAcceptance)
			if tc.listed && got[0].Title != "open" {
				t.Fatalf("listed %q, want the open risk", got[0].Title)
			}
		})
	}
}

func TestPendingPolicies(t *testing.T) {
	t.Parallel()
	rows := []dbx.Policy{
		{ID: pgID(uuid.NewString()), Title: "Access", Version: "1.0", Status: "under_review", SubmittedBy: strp(requester)},
		{ID: pgID(uuid.NewString()), Title: "Draft", Version: "1.0", Status: "draft", SubmittedBy: strp(requester)},
		// Submitted by a session id: the approve handler cannot compare
		// it, so it is not listed.
		{ID: pgID(uuid.NewString()), Title: "Legacy", Version: "1.0", Status: "under_review", SubmittedBy: strp("jwt:token-1")},
	}
	for _, tc := range shared {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PendingPolicies(rows, tc.approver, tc.allowSelf)
			assertOne(t, got, tc, TypePolicy)
			if tc.listed && got[0].Title != "Access v1.0" {
				t.Fatalf("title = %q, want Access v1.0", got[0].Title)
			}
		})
	}
}

// #2446: a draft editor is an author too. A policy submitted by someone
// else but edited by the caller is listed for them only under Allow
// self-approval, and then marked; a person who neither submitted nor
// edited it sees it unmarked.
func TestPendingPolicies_DraftEditor(t *testing.T) {
	t.Parallel()
	const submitter = "33333333-3333-3333-3333-333333333333"
	rows := []dbx.Policy{{
		ID: pgID(uuid.NewString()), Title: "Edited", Version: "2.0", Status: "under_review",
		SubmittedBy: strp(submitter), DraftEditors: []string{submitter, requester},
	}}
	for _, tc := range shared {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertOne(t, PendingPolicies(rows, tc.approver, tc.allowSelf), tc, TypePolicy)
		})
	}
}

func TestPendingFrameworkScopes(t *testing.T) {
	t.Parallel()
	fv := uuid.NewString()
	rows := []dbx.FrameworkScope{
		{ID: pgID(uuid.NewString()), FrameworkVersionID: pgID(fv), Name: "SOC 2 system", State: "review", SubmittedBy: strp(requester)},
		{ID: pgID(uuid.NewString()), Name: "draft", State: "draft", SubmittedBy: strp(requester)},
		// No recorded submitter: must be re-submitted before approval.
		{ID: pgID(uuid.NewString()), Name: "unknown", State: "review"},
	}
	for _, tc := range shared {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PendingFrameworkScopes(rows, tc.approver, tc.allowSelf)
			assertOne(t, got, tc, TypeFrameworkScope)
			if tc.listed && got[0].FrameworkVersionID != fv {
				t.Fatalf("framework_version_id = %q, want %q", got[0].FrameworkVersionID, fv)
			}
		})
	}
}

// Build counts every item but lists at most MaxPerType per type, oldest
// first, and an empty list still reports every type with zero.
func TestBuild(t *testing.T) {
	t.Parallel()
	empty := Build(nil)
	if empty.Total != 0 || len(empty.Items) != 0 || len(empty.Counts) != len(Types) || empty.Truncated {
		t.Fatalf("empty = %+v", empty)
	}

	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var items []Item
	for i := 0; i < MaxPerType+5; i++ {
		at := base.Add(time.Duration(MaxPerType+5-i) * time.Hour)
		items = append(items, Item{Type: TypePolicy, ID: fmt.Sprintf("p%03d", i), RequestedAt: &at})
	}
	items = append(items, Item{Type: TypeException, ID: "undated"})
	early := base.Add(-time.Hour)
	items = append(items, Item{Type: TypeException, ID: "dated", RequestedAt: &early})

	got := Build(items)
	if got.Total != MaxPerType+7 || got.Counts[TypePolicy] != MaxPerType+5 || got.Counts[TypeException] != 2 {
		t.Fatalf("counts = %v total %d", got.Counts, got.Total)
	}
	if !got.Truncated || len(got.Items) != MaxPerType+2 {
		t.Fatalf("truncated = %v, %d items", got.Truncated, len(got.Items))
	}
	// Page order: exceptions before policies; dated before undated; the
	// oldest policy (the last one appended) first.
	if got.Items[0].ID != "dated" || got.Items[1].ID != "undated" {
		t.Fatalf("exceptions out of order: %s, %s", got.Items[0].ID, got.Items[1].ID)
	}
	if got.Items[2].ID != fmt.Sprintf("p%03d", MaxPerType+4) {
		t.Fatalf("first policy = %s, want the oldest", got.Items[2].ID)
	}
}
