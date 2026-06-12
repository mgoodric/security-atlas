//go:build integration

// HTTP-level integration tests for slice 688 — the vendor_reviews ledger
// endpoints (GET/POST /v1/vendors/{id}/reviews). Real Postgres + real chi
// router, mirroring the slice-024 handler smoke pattern in
// integration_test.go. These exercise the handler happy paths
// (ListReviews / RecordReview / toReviewWire) plus the request-validation
// branches the unit surface cannot reach through the router.

package vendors_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// seedVendor POSTs a minimal vendor and returns its id.
func seedVendor(t *testing.T, tsURL, bearer, name string) string {
	t.Helper()
	body := `{"name":"` + name + `","criticality":"high","review_cadence":"quarterly"}`
	resp, payload := doJSON(t, http.MethodPost, tsURL+"/v1/vendors", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed vendor: %d %s", resp.StatusCode, payload)
	}
	var got struct {
		Vendor struct {
			ID string `json:"id"`
		} `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	return got.Vendor.ID
}

// TestHTTP_RecordAndListReviews — AC-3/AC-5 end-to-end. POST two reviews,
// GET the history, and assert the wire shape + newest-first ordering. This
// drives RecordReview, ListReviews, and toReviewWire through the router.
func TestHTTP_RecordAndListReviews(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	id := seedVendor(t, ts.URL, bearer, "Reviewed-Co")

	// Record an older then a newer review (out of order on purpose).
	for _, rv := range []string{
		`{"reviewed_at":"2026-01-15","reviewer":"sec@example.com","outcome":"pass_with_findings","notes":"one low finding"}`,
		`{"reviewed_at":"2026-05-01","reviewer":"owner@example.com","outcome":"pass","notes":""}`,
	} {
		resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors/"+id+"/reviews", bearer, rv)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST review: %d %s", resp.StatusCode, payload)
		}
		var created struct {
			Review map[string]any `json:"review"`
		}
		if err := json.Unmarshal(payload, &created); err != nil {
			t.Fatalf("decode created review: %v", err)
		}
		if created.Review["vendor_id"] != id {
			t.Fatalf("review.vendor_id = %v; want %s", created.Review["vendor_id"], id)
		}
		if created.Review["reviewed_at"] == "" || created.Review["outcome"] == "" {
			t.Fatalf("review wire missing fields: %v", created.Review)
		}
	}

	// List — newest-first.
	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+id+"/reviews", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET reviews: %d %s", resp.StatusCode, payload)
	}
	var got struct {
		Reviews []struct {
			ReviewedAt string `json:"reviewed_at"`
			Outcome    string `json:"outcome"`
			Reviewer   string `json:"reviewer"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(got.Reviews) != 2 {
		t.Fatalf("want 2 reviews; got %d", len(got.Reviews))
	}
	if got.Reviews[0].ReviewedAt != "2026-05-01" {
		t.Fatalf("first row = %s; want newest 2026-05-01", got.Reviews[0].ReviewedAt)
	}
	if got.Reviews[0].Outcome != "pass" || got.Reviews[1].Outcome != "pass_with_findings" {
		t.Fatalf("outcome ordering off: %v", got.Reviews)
	}

	// The vendor's last_review_date scalar tracks the newest review.
	resp, payload = doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+id, bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET vendor: %d %s", resp.StatusCode, payload)
	}
	var v struct {
		Vendor struct {
			LastReviewDate string `json:"last_review_date"`
		} `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &v); err != nil {
		t.Fatalf("decode vendor: %v", err)
	}
	if v.Vendor.LastReviewDate != "2026-05-01" {
		t.Fatalf("last_review_date = %q; want 2026-05-01", v.Vendor.LastReviewDate)
	}
}

// TestHTTP_ListReviews_EmptyHistory — a vendor with no recorded reviews
// returns an empty (non-null) series, exercising the empty-slice branch of
// ListReviews.
func TestHTTP_ListReviews_EmptyHistory(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	id := seedVendor(t, ts.URL, bearer, "No-Reviews-Co")

	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+id+"/reviews", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET reviews: %d %s", resp.StatusCode, payload)
	}
	var got struct {
		Reviews []map[string]any `json:"reviews"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Reviews == nil {
		t.Fatalf("reviews should be [] not null")
	}
	if len(got.Reviews) != 0 {
		t.Fatalf("want empty history; got %d", len(got.Reviews))
	}
}

// TestHTTP_RecordReview_RejectsBadInput — the request-validation branches:
// a non-UUID id (400), a malformed JSON body (400), a missing reviewed_at
// (400), and a bad outcome (400 from the store validator via writeStoreErr).
func TestHTTP_RecordReview_RejectsBadInput(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	id := seedVendor(t, ts.URL, bearer, "Validation-Co")

	cases := []struct {
		name string
		url  string
		body string
		want int
	}{
		{
			name: "bad_uuid",
			url:  ts.URL + "/v1/vendors/not-a-uuid/reviews",
			body: `{"reviewed_at":"2026-05-01","outcome":"pass"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "malformed_json",
			url:  ts.URL + "/v1/vendors/" + id + "/reviews",
			body: `{not json`,
			want: http.StatusBadRequest,
		},
		{
			name: "missing_reviewed_at",
			url:  ts.URL + "/v1/vendors/" + id + "/reviews",
			body: `{"outcome":"pass"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "bad_reviewed_at_format",
			url:  ts.URL + "/v1/vendors/" + id + "/reviews",
			body: `{"reviewed_at":"05/01/2026","outcome":"pass"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "bad_outcome",
			url:  ts.URL + "/v1/vendors/" + id + "/reviews",
			body: `{"reviewed_at":"2026-05-01","outcome":"remediated"}`,
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, payload := doJSON(t, http.MethodPost, tc.url, bearer, tc.body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d; want %d (body %s)", resp.StatusCode, tc.want, payload)
			}
		})
	}
}

// TestHTTP_ListReviews_BadUUID — the non-UUID branch of ListReviews (400).
func TestHTTP_ListReviews_BadUUID(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/not-a-uuid/reviews", bearer, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400 (body %s)", resp.StatusCode, payload)
	}
}

// TestHTTP_Reviews_Unauthenticated — no bearer → 401 on both verbs (the
// tenantContext guard). Confirms the credential is required before any DB
// touch.
func TestHTTP_Reviews_Unauthenticated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, _ := setupHTTPServer(t, tenant)

	someID := uuid.NewString()
	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+someID+"/reviews", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET no-bearer status = %d; want 401", resp.StatusCode)
	}
	resp, _ = doJSON(t, http.MethodPost, ts.URL+"/v1/vendors/"+someID+"/reviews", "",
		`{"reviewed_at":"2026-05-01","outcome":"pass"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST no-bearer status = %d; want 401", resp.StatusCode)
	}
}
