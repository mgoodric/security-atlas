//go:build integration

// #1806 integration tests — policy review dates through the API: set at
// publish, changed by an approver through PATCH /v1/policies/{id}/review-date
// (audit-logged, no new version), the overdue / due-soon calendar status,
// the ?review= list filter, and a new version not inheriting its
// predecessor's date. Real Postgres under RLS, driven through the full
// HTTP stack (JWT, tenancy, authz middleware).

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
)

type reviewPolicyWire struct {
	ID            string  `json:"id"`
	Version       string  `json:"version"`
	Status        string  `json:"status"`
	PredecessorID *string `json:"predecessor_id"`
	NextReviewAt  *string `json:"next_review_at"`
	ReviewStatus  string  `json:"review_status"`
}

func decodePolicy(t *testing.T, raw []byte) reviewPolicyWire {
	t.Helper()
	var env struct {
		Policy reviewPolicyWire `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode policy: %v (%s)", err, raw)
	}
	return env.Policy
}

// publishWithReview runs draft -> under_review -> approved -> published,
// naming nextReview ("" for none) at publish.
func publishWithReview(t *testing.T, s setupResult, title, version, nextReview string) reviewPolicyWire {
	t.Helper()
	id := createDraftPolicy(t, s, title, version, nil)
	return publishDraft(t, s, id, version, nextReview)
}

func publishDraft(t *testing.T, s setupResult, id, version, nextReview string) reviewPolicyWire {
	t.Helper()
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.adminBear, http.StatusOK)
	req := map[string]any{"new_version": version}
	if nextReview != "" {
		req["next_review_at"] = nextReview
	}
	body, _ := json.Marshal(req)
	return decodePolicy(t, s.do(t, http.MethodPost, "/v1/policies/"+id+"/publish", body, s.adminBear, http.StatusCreated))
}

func patchReviewDate(t *testing.T, s setupResult, id string, next any, bearer string, want int) []byte {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"next_review_at": next})
	return s.do(t, http.MethodPatch, "/v1/policies/"+id+"/review-date", body, bearer, want)
}

func reviewAt(t *testing.T, p reviewPolicyWire) time.Time {
	t.Helper()
	if p.NextReviewAt == nil {
		t.Fatalf("policy %s: next_review_at missing", p.ID)
	}
	ts, err := time.Parse(time.RFC3339, *p.NextReviewAt)
	if err != nil {
		t.Fatalf("policy %s: next_review_at %q: %v", p.ID, *p.NextReviewAt, err)
	}
	return ts
}

type reviewAuditRow struct {
	Actor    string
	Previous pgtype.Timestamptz
	New      pgtype.Timestamptz
}

func reviewAuditRows(t *testing.T, s setupResult, policyID string) []reviewAuditRow {
	t.Helper()
	rows, err := s.admin.Query(context.Background(), `
		SELECT actor, previous_next_review_at, new_next_review_at
		FROM policy_audit_log
		WHERE tenant_id = $1 AND policy_id = $2 AND action = 'review_date_updated'
		ORDER BY created_at, id`, s.tenant, policyID)
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	defer rows.Close()
	var out []reviewAuditRow
	for rows.Next() {
		var r reviewAuditRow
		if err := rows.Scan(&r.Actor, &r.Previous, &r.New); err != nil {
			t.Fatalf("scan audit log: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestReviewDate_SetAtPublishAndUpdatedByApprover(t *testing.T) {
	s := setup(t)
	published := publishWithReview(t, s, "Access Control Policy", "1.0", "2027-01-15")
	want := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	if got := reviewAt(t, published); !got.Equal(want) {
		t.Fatalf("publish next_review_at = %v, want %v", got, want)
	}

	// Returned by the detail and list representations.
	got := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+published.ID, nil, s.adminBear, http.StatusOK))
	if !reviewAt(t, got).Equal(want) || got.ReviewStatus != "upcoming" {
		t.Fatalf("GET: next_review_at=%v review_status=%q", got.NextReviewAt, got.ReviewStatus)
	}

	// An approver changes it in place: same id, same version, no new row.
	approverUser := seedUser(t, s.admin, s.tenant, "approver@1806.test")
	approverClaims := testjwt.ApproverFor(uuid.MustParse(s.tenant))
	approverClaims.Subject = approverUser.String()
	approver := s.apiServer.IssueTestJWT(t, approverClaims)
	updated := decodePolicy(t, patchReviewDate(t, s, published.ID, "2027-06-30", approver, http.StatusOK))
	if updated.ID != published.ID || updated.Version != "1.0" || updated.Status != "published" {
		t.Fatalf("update made a new version: %+v", updated)
	}
	newWant := time.Date(2027, 6, 30, 0, 0, 0, 0, time.UTC)
	if !reviewAt(t, updated).Equal(newWant) {
		t.Fatalf("updated next_review_at = %v", updated.NextReviewAt)
	}
	if n := tenantPolicyCount(t, s); n != 1 {
		t.Fatalf("policy rows = %d, want 1", n)
	}

	// The update is audit-logged with the actor and both dates.
	audit := reviewAuditRows(t, s, published.ID)
	if len(audit) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit))
	}
	if audit[0].Actor != approverUser.String() {
		t.Errorf("audit actor = %q", audit[0].Actor)
	}
	if !audit[0].Previous.Valid || !audit[0].Previous.Time.Equal(want) ||
		!audit[0].New.Valid || !audit[0].New.Time.Equal(newWant) {
		t.Errorf("audit dates = %v -> %v", audit[0].Previous, audit[0].New)
	}

	// null clears the date, and that is logged too.
	cleared := decodePolicy(t, patchReviewDate(t, s, published.ID, nil, s.adminBear, http.StatusOK))
	if cleared.NextReviewAt != nil || cleared.ReviewStatus != "" {
		t.Fatalf("clear left next_review_at=%v review_status=%q", cleared.NextReviewAt, cleared.ReviewStatus)
	}
	audit = reviewAuditRows(t, s, published.ID)
	if len(audit) != 2 || audit[1].New.Valid || !audit[1].Previous.Time.Equal(newWant) {
		t.Fatalf("clear audit = %+v", audit)
	}
}

func TestReviewDate_RefusedForNonApprover(t *testing.T) {
	s := setup(t)
	published := publishWithReview(t, s, "Vendor Policy", "1.0", "2027-01-15")
	tenant := uuid.MustParse(s.tenant)
	for name, claims := range map[string]jwt.AtlasClaims{
		"grc_engineer": testjwt.OwnerFor(tenant, []string{"grc_engineer"}),
		"viewer":       testjwt.ViewerFor(tenant),
	} {
		t.Run(name, func(t *testing.T) {
			bearer := s.apiServer.IssueTestJWT(t, claims)
			patchReviewDate(t, s, published.ID, "2030-01-01", bearer, http.StatusForbidden)
		})
	}
	got := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+published.ID, nil, s.adminBear, http.StatusOK))
	if !reviewAt(t, got).Equal(time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("refused update changed next_review_at to %v", got.NextReviewAt)
	}
	if audit := reviewAuditRows(t, s, published.ID); len(audit) != 0 {
		t.Fatalf("refused update wrote %d audit rows", len(audit))
	}
}

func TestReviewDate_UpdateValidation(t *testing.T) {
	s := setup(t)
	draft := createDraftPolicy(t, s, "Draft Policy", "0.1", nil)
	// Only a published policy has a review date to change.
	patchReviewDate(t, s, draft, "2027-01-15", s.adminBear, http.StatusConflict)
	patchReviewDate(t, s, uuid.NewString(), "2027-01-15", s.adminBear, http.StatusNotFound)

	published := publishWithReview(t, s, "Published Policy", "1.0", "")
	if published.NextReviewAt != nil {
		t.Fatalf("publish without a date set next_review_at=%v", *published.NextReviewAt)
	}
	patchReviewDate(t, s, published.ID, "15/01/2027", s.adminBear, http.StatusBadRequest)
	s.do(t, http.MethodPatch, "/v1/policies/"+published.ID+"/review-date", []byte(`{}`), s.adminBear, http.StatusBadRequest)
	// RFC 3339 is accepted alongside YYYY-MM-DD.
	updated := decodePolicy(t, patchReviewDate(t, s, published.ID, "2027-01-15T09:30:00Z", s.adminBear, http.StatusOK))
	if !reviewAt(t, updated).Equal(time.Date(2027, 1, 15, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339 update = %v", updated.NextReviewAt)
	}

	draft2 := createDraftPolicy(t, s, "Bad Date Policy", "1.0", nil)
	s.do(t, http.MethodPatch, "/v1/policies/"+draft2+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+draft2+"/approve", nil, s.adminBear, http.StatusOK)
	body, _ := json.Marshal(map[string]any{"new_version": "1.0", "next_review_at": "next year"})
	s.do(t, http.MethodPost, "/v1/policies/"+draft2+"/publish", body, s.adminBear, http.StatusBadRequest)
}

// reviewFixture publishes three policies whose review dates are overdue,
// due within 30 days, and further out, plus one with no date and a draft.
type reviewFixture struct {
	overdue, dueSoon, upcoming, undated, draft string
}

func seedReviewFixture(t *testing.T, s setupResult) reviewFixture {
	t.Helper()
	now := time.Now().UTC()
	day := func(d int) string { return now.AddDate(0, 0, d).Format(time.RFC3339) }
	return reviewFixture{
		overdue:  publishWithReview(t, s, "Overdue Policy", "1.0", day(-5)).ID,
		dueSoon:  publishWithReview(t, s, "Due Soon Policy", "1.0", day(10)).ID,
		upcoming: publishWithReview(t, s, "Upcoming Policy", "1.0", day(60)).ID,
		undated:  publishWithReview(t, s, "Undated Policy", "1.0", "").ID,
		draft:    createDraftPolicy(t, s, "Draft Policy", "0.1", nil),
	}
}

func TestReviewDate_CalendarStatus(t *testing.T) {
	s := setup(t)
	fx := seedReviewFixture(t, s)
	now := time.Now().UTC()
	path := "/v1/calendar?types=policy&from=" + now.AddDate(0, 0, -10).Format("2006-01-02") +
		"&to=" + now.AddDate(0, 0, 70).Format("2006-01-02")
	raw := s.do(t, http.MethodGet, path, nil, s.adminBear, http.StatusOK)
	var body struct {
		Events []struct {
			RelatedEntityID string `json:"related_entity_id"`
			Type            string `json:"type"`
			Status          string `json:"status"`
			Summary         string `json:"summary"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode calendar: %v (%s)", err, raw)
	}
	got := map[string]string{}
	for _, e := range body.Events {
		if e.Type != "policy" {
			t.Errorf("unexpected event type %q", e.Type)
		}
		if e.Summary != "published" {
			t.Errorf("event %s summary = %q, want the policy state", e.RelatedEntityID, e.Summary)
		}
		got[e.RelatedEntityID] = e.Status
	}
	want := map[string]string{fx.overdue: "overdue", fx.dueSoon: "due-soon", fx.upcoming: "upcoming"}
	if len(got) != len(want) {
		t.Fatalf("calendar policy events = %v, want %v", got, want)
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("policy %s: calendar status = %q, want %q", id, got[id], status)
		}
	}
}

func listIDs(t *testing.T, s setupResult, path string) []string {
	t.Helper()
	raw := s.do(t, http.MethodGet, path, nil, s.adminBear, http.StatusOK)
	var body struct {
		Policies []reviewPolicyWire `json:"policies"`
		Count    int                `json:"count"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode list: %v (%s)", err, raw)
	}
	if body.Count != len(body.Policies) {
		t.Errorf("%s: count %d != %d rows", path, body.Count, len(body.Policies))
	}
	ids := make([]string, len(body.Policies))
	for i, p := range body.Policies {
		ids[i] = p.ID
	}
	sort.Strings(ids)
	return ids
}

func TestReviewDate_ListOverdueFilter(t *testing.T) {
	s := setup(t)
	fx := seedReviewFixture(t, s)
	for _, path := range []string{
		"/v1/policies?review=overdue",
		"/v1/policies?review=overdue&include=ack_rate",
	} {
		if got := listIDs(t, s, path); len(got) != 1 || got[0] != fx.overdue {
			t.Errorf("%s = %v, want exactly [%s]", path, got, fx.overdue)
		}
	}
	if got := listIDs(t, s, "/v1/policies?review=due-soon&include=ack_rate"); len(got) != 1 || got[0] != fx.dueSoon {
		t.Errorf("review=due-soon = %v, want [%s]", got, fx.dueSoon)
	}
	if got := listIDs(t, s, "/v1/policies?include=ack_rate"); len(got) != 5 {
		t.Errorf("unfiltered list = %d rows, want 5", len(got))
	}
	s.do(t, http.MethodGet, "/v1/policies?review=late", nil, s.adminBear, http.StatusBadRequest)
}

func TestReviewDate_NewVersionCarriesItsOwnDate(t *testing.T) {
	s := setup(t)
	v1 := publishWithReview(t, s, "Retention Policy", "1.0", "2027-01-15")

	body, _ := json.Marshal(map[string]any{
		"title":              "Retention Policy",
		"version":            "2.0",
		"body_md":            "# Retention Policy v2",
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{s.ctrlID.String()},
		"source_attribution": "tenant_authored",
		"predecessor_id":     v1.ID,
	})
	v2ID := decodePolicy(t, s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusCreated)).ID
	v2 := publishDraft(t, s, v2ID, "2.0", "2028-03-01")
	if !reviewAt(t, v2).Equal(time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("v2 next_review_at = %v", v2.NextReviewAt)
	}
	old := decodePolicy(t, s.do(t, http.MethodGet, "/v1/policies/"+v1.ID, nil, s.adminBear, http.StatusOK))
	if old.Status != "superseded" || old.NextReviewAt != nil {
		t.Fatalf("superseded v1: status=%s next_review_at=%v", old.Status, old.NextReviewAt)
	}
	// The superseded row has no review due, so it cannot be changed.
	patchReviewDate(t, s, v1.ID, "2027-02-01", s.adminBear, http.StatusConflict)
}
