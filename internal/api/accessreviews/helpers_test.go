// helpers_test.go — pure-Go unit tests for the OE-670 handler package's
// pre-DB branches (the slice-290 pattern: guards, error mapping, and
// wire helpers exercised with fast t.Parallel() table tests, no
// Postgres, no build tag). The Postgres-backed lifecycle lives in
// integration_test.go.

package accessreviews

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/accessreview"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestHasProgramGuards(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{name: "bare credential", cred: credstore.Credential{}, want: false},
		{name: "admin", cred: credstore.Credential{IsAdmin: true}, want: true},
		{name: "approver", cred: credstore.Credential{IsApprover: true}, want: true},
		{name: "owner roles", cred: credstore.Credential{OwnerRoles: []string{"control_owner"}}, want: true},
		{name: "empty owner roles slice", cred: credstore.Credential{OwnerRoles: []string{}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasProgramRead(tc.cred); got != tc.want {
				t.Fatalf("hasProgramRead(%+v) = %v, want %v", tc.cred, got, tc.want)
			}
			if got := hasProgramWrite(tc.cred); got != tc.want {
				t.Fatalf("hasProgramWrite(%+v) = %v, want %v", tc.cred, got, tc.want)
			}
		})
	}
}

func TestRequireGuards(t *testing.T) {
	t.Parallel()
	guards := map[string]func(http.ResponseWriter, *http.Request) bool{
		"read":  requireProgramRead,
		"write": requireProgramWrite,
	}
	for name, guard := range guards {
		t.Run(name+" no credential", func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/access-reviews", nil)
			if guard(w, r) {
				t.Fatal("guard admitted a request with no credential")
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
		t.Run(name+" bare credential", func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/access-reviews", nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{TenantID: uuid.NewString()}))
			if guard(w, r) {
				t.Fatal("guard admitted a bare credential")
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
		t.Run(name+" admin credential", func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/access-reviews", nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{TenantID: uuid.NewString(), IsAdmin: true}))
			if !guard(w, r) {
				t.Fatalf("guard denied an admin credential: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWriteStoreErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: accessreview.ErrNotFound, want: http.StatusNotFound},
		{name: "wrapped not found", err: fmt.Errorf("wrap: %w", accessreview.ErrNotFound), want: http.StatusNotFound},
		{name: "incomplete", err: accessreview.ErrIncomplete, want: http.StatusConflict},
		{name: "reason required", err: accessreview.ErrReasonRequired, want: http.StatusUnprocessableEntity},
		{name: "invalid decision", err: accessreview.ErrInvalidDecision, want: http.StatusUnprocessableEntity},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	h := &Handler{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/access-reviews", nil)
			h.writeStoreErr(w, r, "test op", tc.err)
			if w.Code != tc.want {
				t.Fatalf("writeStoreErr(%v) = %d, want %d", tc.err, w.Code, tc.want)
			}
		})
	}
}

func TestWriteCreateErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "name required", err: accessreview.ErrNameRequired, want: http.StatusBadRequest},
		{name: "due required", err: accessreview.ErrDueRequired, want: http.StatusBadRequest},
		{name: "created_by required", err: accessreview.ErrCreatedByRequired, want: http.StatusBadRequest},
		{name: "reviewer required", err: accessreview.ErrReviewerRequired, want: http.StatusBadRequest},
		{name: "items required", err: accessreview.ErrItemsRequired, want: http.StatusUnprocessableEntity},
		{name: "csv missing column", err: errors.New("access_review: csv missing system column"), want: http.StatusUnprocessableEntity},
		{name: "csv malformed row", err: fmt.Errorf("access_review: read csv: %w", errors.New("wrong number of fields")), want: http.StatusUnprocessableEntity},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	h := &Handler{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/access-reviews", nil)
			h.writeCreateErr(w, r, tc.err)
			if w.Code != tc.want {
				t.Fatalf("writeCreateErr(%v) = %d, want %d", tc.err, w.Code, tc.want)
			}
		})
	}
}

func TestValidListStatus(t *testing.T) {
	t.Parallel()
	valid := []string{"", "draft", "active", "completed", "cancelled"}
	for _, s := range valid {
		if !validListStatus(s) {
			t.Fatalf("validListStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"open", "ACTIVE", "done", "junk"} {
		if validListStatus(s) {
			t.Fatalf("validListStatus(%q) = true, want false", s)
		}
	}
}

func TestSplitList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil", in: nil, want: nil},
		{name: "single", in: []string{"a"}, want: []string{"a"}},
		{name: "comma separated", in: []string{"a, b ,c"}, want: []string{"a", "b", "c"}},
		{name: "repeated and comma", in: []string{"a,b", "c"}, want: []string{"a", "b", "c"}},
		{name: "blank fragments dropped", in: []string{" , a ,, ", ""}, want: []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitList(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("splitList(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitList(%v) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestParseDueAt(t *testing.T) {
	t.Parallel()
	if got, err := parseDueAt(""); err != nil || !got.IsZero() {
		t.Fatalf("parseDueAt(\"\") = %v, %v; want zero time, nil", got, err)
	}
	if got, err := parseDueAt("  "); err != nil || !got.IsZero() {
		t.Fatalf("parseDueAt(blank) = %v, %v; want zero time, nil", got, err)
	}
	want := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	got, err := parseDueAt("2026-09-30T12:00:00Z")
	if err != nil || !got.Equal(want) {
		t.Fatalf("parseDueAt(rfc3339) = %v, %v; want %v", got, err, want)
	}
	if _, err := parseDueAt("next tuesday"); err == nil {
		t.Fatal("parseDueAt accepted a non-RFC3339 value")
	}
}

func TestCampaignWireFrom(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	evidenceID := uuid.New()
	c := accessreview.Campaign{
		ID:               uuid.New(),
		Name:             "Q3",
		Source:           accessreview.SourceManualCSV,
		Status:           accessreview.StatusCompleted,
		DueAt:            now,
		CreatedBy:        "owner",
		CompletedAt:      &now,
		EvidenceRecordID: &evidenceID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	w := campaignWireFrom(c)
	if w.ID != c.ID.String() || w.Status != "completed" || w.Source != "manual_csv" {
		t.Fatalf("campaignWireFrom = %+v", w)
	}
	if w.CompletedAt == nil || w.EvidenceRecordID == nil || *w.EvidenceRecordID != evidenceID.String() {
		t.Fatalf("completed fields not mapped: %+v", w)
	}
	// Scope arrays are always non-nil on the wire.
	if w.Scope.Systems == nil || w.Scope.Entitlements == nil || w.Scope.UserIDs == nil {
		t.Fatalf("scope arrays must be non-nil: %+v", w.Scope)
	}
}

func TestItemWireFrom(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	decision := accessreview.DecisionRevoke
	attestedBy := "reviewer-1"
	item := accessreview.Item{
		ID:              uuid.New(),
		CampaignID:      uuid.New(),
		System:          "prod-db",
		Entitlement:     "admin",
		PrincipalUserID: "u-1",
		PrincipalEmail:  "alice@example.test",
		ReviewerID:      "reviewer-1",
		Status:          accessreview.ItemStatusAttested,
		Decision:        &decision,
		Reason:          "left team",
		AttestedBy:      &attestedBy,
		AttestedAt:      &now,
		Source:          accessreview.SourceManualCSV,
		SourceRef:       "prod-db:admin",
	}
	w := itemWireFrom(item)
	if w.Decision == nil || *w.Decision != "revoke" || w.AttestedBy == nil || w.AttestedAt == nil {
		t.Fatalf("itemWireFrom attested fields = %+v", w)
	}
	pending := accessreview.Item{ID: uuid.New(), CampaignID: item.CampaignID, Status: accessreview.ItemStatusPending}
	pw := itemWireFrom(pending)
	if pw.Decision != nil || pw.AttestedBy != nil || pw.AttestedAt != nil {
		t.Fatalf("pending item must omit attested fields: %+v", pw)
	}
}
