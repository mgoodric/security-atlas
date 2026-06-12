// Slice 457 — unit coverage for the OSCAL signed-export DOWNLOAD verb.
//
// Load-bearing functions covered here:
//
//   - Handler.Download — the attachment-serving sibling of Handler.Export.
//     It runs the SAME tenant-scoped export (the shared runExport helper)
//     and serializes the IDENTICAL signed envelope, but adds:
//       • Content-Disposition: attachment; filename="oscal-bundle-..."
//       • X-Content-Type-Options: nosniff
//     The body must be byte-identical to the Export envelope (same
//     manifest + four members + slice-413 signature) so a downloaded
//     bundle is the same artifact the wire endpoint returns.
//   - downloadFilename / frozenDate — the deterministic filename builder
//     (AC-2: "a deterministic filename"), including the malformed /
//     empty FrozenAt fallback (the date segment is omitted, never
//     guessed).
//   - Error mapping reuse — Download maps oscal error sentinels to the
//     same HTTP statuses Export does (it shares writeExportError), and on
//     the error path it sets NO Content-Disposition (an error is not a
//     downloadable artifact).
//
// The DB-backed tenant-scoped happy path + the headline cross-tenant
// denial are pinned at the internal/oscal integration tier (real
// Postgres + RLS). These unit tests inject a fakeExporter (defined in
// handler_more_test.go) so each branch is hit without standing up the
// platform.

package oscalexport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/oscal"
)

// newDownloadRouter wires a chi router with a fakeExporter behind the
// slice-457 download verb. Mirrors newRouterWith (handler_more_test.go)
// but for the :download route.
func newDownloadRouter(fake *fakeExporter) *chi.Mux {
	h := &Handler{exporter: fake}
	r := chi.NewRouter()
	r.Post("/v1/audit-periods/{id}/oscal-export:download", h.Download)
	return r
}

func newDownloadRequest(body string) *http.Request {
	url := "/v1/audit-periods/" + fixedPeriodID + "/oscal-export:download"
	if body == "" {
		return httptest.NewRequest(http.MethodPost, url, nil)
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestDownload_HappyPath_SetsAttachmentHeadersAndBundleBody(t *testing.T) {
	bundle := newFakeBundle(t)
	fake := &fakeExporter{bundle: bundle}
	r := newDownloadRouter(fake)

	req := newDownloadRequest(`{"organization_name":"Acme","system_name":"Atlas"}`)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// AC-2: Content-Disposition: attachment so the browser raises a
	// `download` event rather than rendering the JSON inline.
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q, want an attachment disposition", cd)
	}
	// AC-2: a deterministic filename grounded to the period id. FrozenAt
	// is 2026-01-01... (newFakeBundle) -> the date segment is present.
	if !strings.Contains(cd, `filename="oscal-bundle-`) {
		t.Errorf("Content-Disposition %q missing the oscal-bundle filename", cd)
	}
	if !strings.Contains(cd, fixedPeriodID) {
		t.Errorf("Content-Disposition %q should embed the period id", cd)
	}
	if !strings.Contains(cd, "2026-01-01") {
		t.Errorf("Content-Disposition %q should embed the frozen date", cd)
	}
	if !strings.HasSuffix(cd, `.json"`) {
		t.Errorf("Content-Disposition %q should end in .json", cd)
	}

	// The bundle is served as application/json with nosniff so the
	// browser honors the attachment + declared type.
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if xcto := rec.Header().Get("X-Content-Type-Options"); xcto != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", xcto)
	}

	// AC-4: the downloaded artifact carries the slice-413 signing manifest
	// and the four canonical members — byte-identical to the Export
	// envelope.
	var got struct {
		AuditPeriodID string `json:"audit_period_id"`
		FrozenAt      string `json:"frozen_at"`
		Signature     struct {
			Algorithm string `json:"algorithm"`
			Digest    string `json:"digest"`
			Signature string `json:"signature"`
		} `json:"signature"`
		Members []struct {
			ModelType string `json:"model_type"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("download body is not the export envelope JSON: %v; body=%s", err, rec.Body.String())
	}
	if got.AuditPeriodID != fixedPeriodID {
		t.Errorf("audit_period_id = %q, want %q", got.AuditPeriodID, fixedPeriodID)
	}
	if got.Signature.Algorithm != "ed25519" || got.Signature.Digest == "" || got.Signature.Signature == "" {
		t.Errorf("signing manifest did not ride in the downloaded bundle: %+v", got.Signature)
	}
	if len(got.Members) != 4 {
		t.Fatalf("members len = %d, want 4 (SSP/AP/AR/POA&M)", len(got.Members))
	}

	// The export ran exactly once under the request's tenant context (the
	// download adds no extra read).
	if fake.calls != 1 {
		t.Errorf("exporter calls = %d, want 1", fake.calls)
	}
}

func TestDownload_RejectsNonUUIDPeriodID(t *testing.T) {
	fake := &fakeExporter{}
	h := &Handler{exporter: fake}
	r := chi.NewRouter()
	r.Post("/v1/audit-periods/{id}/oscal-export:download", h.Download)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/audit-periods/not-a-uuid/oscal-export:download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-UUID period id", rec.Code)
	}
	// An error response is NOT a downloadable artifact.
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("error path set Content-Disposition = %q, want none", cd)
	}
	if fake.calls != 0 {
		t.Errorf("exporter should not run when the id fails to parse; calls=%d", fake.calls)
	}
}

func TestDownload_ErrorMapping_NoAttachmentOnError(t *testing.T) {
	// Download shares writeExportError with Export, so the sentinel->status
	// mapping is already covered by TestExport_ErrorMapping. Here we pin
	// the download-specific property: on the error path NO attachment
	// disposition is set (a 404/409 is a JSON error, not a file).
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"NotFound->404", oscal.ErrPeriodNotFound, http.StatusNotFound},
		{"NotFrozen->409", oscal.ErrPeriodNotFrozen, http.StatusConflict},
		{"SigningFailed->500", oscal.ErrSigningFailed, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeExporter{err: tc.err}
			r := newDownloadRouter(fake)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, newDownloadRequest(""))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if cd := rec.Header().Get("Content-Disposition"); cd != "" {
				t.Errorf("error path set Content-Disposition = %q, want none", cd)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json (the error envelope)", ct)
			}
		})
	}
}

func TestDownloadFilename_FrozenDateHandling(t *testing.T) {
	const pid = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name     string
		frozenAt string
		want     string
	}{
		{
			name:     "RFC3339 frozen -> date segment present",
			frozenAt: "2026-03-31T00:00:00Z",
			want:     "oscal-bundle-" + pid + "-2026-03-31.json",
		},
		{
			name:     "date-only frozen -> date segment present",
			frozenAt: "2026-03-31",
			want:     "oscal-bundle-" + pid + "-2026-03-31.json",
		},
		{
			name:     "empty frozen -> date omitted, never guessed",
			frozenAt: "",
			want:     "oscal-bundle-" + pid + ".json",
		},
		{
			name:     "malformed frozen (too short) -> date omitted",
			frozenAt: "2026-03",
			want:     "oscal-bundle-" + pid + ".json",
		},
		{
			name:     "malformed frozen (wrong separators) -> date omitted",
			frozenAt: "2026/03/31T00:00",
			want:     "oscal-bundle-" + pid + ".json",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := downloadFilename(exportResponse{
				AuditPeriodID: pid,
				FrozenAt:      tc.frozenAt,
			})
			if got != tc.want {
				t.Errorf("downloadFilename = %q, want %q", got, tc.want)
			}
		})
	}
}
