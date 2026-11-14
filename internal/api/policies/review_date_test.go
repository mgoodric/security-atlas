// review_date_test.go — #1806 pure-Go branches for the review-date surface:
// the ?review= filter vocabulary, review-date parsing, the review_status
// wire field, and SetReviewDate's request validation (each 400 returns
// before any store call, so a nil store proves no DB is touched). The store
// path is covered by review_date_integration_test.go.

package policies

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestValidReviewFilter(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{
		"": true, "overdue": true, "due-soon": true, "upcoming": true,
		"due_soon": false, "late": false, "OVERDUE": false,
	} {
		if got := validReviewFilter(v); got != want {
			t.Errorf("validReviewFilter(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestParseReviewDate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2027-01-15", time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"2027-01-15T09:30:00Z", time.Date(2027, 1, 15, 9, 30, 0, 0, time.UTC), true},
		{"2027-01-15T09:30:00+02:00", time.Date(2027, 1, 15, 7, 30, 0, 0, time.UTC), true},
		{"15/01/2027", time.Time{}, false},
		{"", time.Time{}, false},
	}
	for _, c := range cases {
		got, err := parseReviewDate(c.in)
		if (err == nil) != c.ok {
			t.Errorf("parseReviewDate(%q) err = %v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && !got.Equal(c.want) {
			t.Errorf("parseReviewDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWireFromPolicy_ReviewStatus(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-48 * time.Hour)
	if got := wireFromPolicy(policy.Policy{Status: policy.StatePublished, NextReviewAt: &past}).ReviewStatus; got != policy.ReviewOverdue {
		t.Errorf("published, past date: review_status = %q", got)
	}
	if got := wireFromPolicy(policy.Policy{Status: policy.StateDraft, NextReviewAt: &past}).ReviewStatus; got != "" {
		t.Errorf("draft: review_status = %q, want empty", got)
	}
}

// reviewDateRequest builds an approver request for PATCH review-date.
func reviewDateRequest(t *testing.T, id, body string) *http.Request {
	t.Helper()
	tenant := uuid.NewString()
	base := httptest.NewRequest(http.MethodPatch, "/v1/policies/x/review-date", strings.NewReader(body))
	ctx := authctx.WithCredential(base.Context(),
		credstore.Credential{ID: "k", TenantID: tenant, IsApprover: true})
	ctx, err := tenancy.WithTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return base.WithContext(ctx)
}

func TestSetReviewDate_BadRequests(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cases := map[string]struct{ id, body string }{
		"bad id":        {"not-a-uuid", `{"next_review_at":"2027-01-15"}`},
		"not JSON":      {uuid.NewString(), `{`},
		"key missing":   {uuid.NewString(), `{}`},
		"not a string":  {uuid.NewString(), `{"next_review_at":42}`},
		"bad date text": {uuid.NewString(), `{"next_review_at":"soon"}`},
	}
	for name, c := range cases {
		name, c := name, c
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.SetReviewDate(rec, reviewDateRequest(t, c.id, c.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestListPolicies_BadReviewFilter(t *testing.T) {
	t.Parallel()
	h := New(nil)
	r := reviewDateRequest(t, uuid.NewString(), "")
	r.URL.RawQuery = "review=late"
	rec := httptest.NewRecorder()
	h.ListPolicies(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("review=late: status %d, want 400", rec.Code)
	}
}
