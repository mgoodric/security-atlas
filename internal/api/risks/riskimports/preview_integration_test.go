//go:build integration

// #2848 — the web mapping step's API: POST /v1/risks/imports/preview reads
// a register's header row and maps it without staging, and an upload whose
// headers match no alias is fully mapped by its column map and stages.

package riskimports_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// postForm posts a multipart upload of csv to path with the given
// repeatable fields.
func postForm(t *testing.T, e env, bearer, path string, csv []byte, fields map[string][]string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "register.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(csv); err != nil {
		t.Fatalf("write part: %v", err)
	}
	for k, vs := range fields {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, e.server.URL+path, &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+bearer)
	return do(t, req)
}

func TestRiskImport_PreviewMapsWithoutStaging(t *testing.T) {
	e := newEnv(t)
	noAlias := []byte("Acme register\nHeadline,Bucket,Who,Owner\nPhishing,operational,a@example.com,b@example.com\n")

	status, raw := postForm(t, e, e.grcJWT, "/v1/risks/imports/preview", noAlias, nil)
	if status != http.StatusOK {
		t.Fatalf("preview = %d %s", status, raw)
	}
	p := decode[riskimport.Preview](t, raw)
	if p.HeaderDetected || p.HeaderRow != 2 || len(p.Columns) != 4 || p.DataRows != 1 {
		t.Fatalf("preview = %+v", p)
	}
	if p.Columns[0].Field != "" || p.Columns[3].Field != riskimport.FieldOwner {
		t.Errorf("columns = %+v, want only Owner mapped by alias", p.Columns)
	}

	mapped := map[string][]string{
		"column_map":    {"title=Headline", "category=Bucket"},
		"ignore_column": {"Owner"},
	}
	status, raw = postForm(t, e, e.grcJWT, "/v1/risks/imports/preview", noAlias, mapped)
	if status != http.StatusOK {
		t.Fatalf("mapped preview = %d %s", status, raw)
	}
	if p := decode[riskimport.Preview](t, raw); !p.HeaderDetected || p.Columns[0].Field != riskimport.FieldTitle || p.Columns[3].Field != "" {
		t.Errorf("mapped preview = %+v", p)
	}

	var batches int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_import_batches WHERE tenant_id = $1`, e.tenant).Scan(&batches); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if batches != 0 {
		t.Errorf("preview staged %d batches, want 0", batches)
	}

	if status, raw := postForm(t, e, e.viewJWT, "/v1/risks/imports/preview", noAlias, nil); status != http.StatusForbidden {
		t.Errorf("viewer preview = %d, want 403; body=%s", status, raw)
	}
	if status, raw := postForm(t, e, e.grcJWT, "/v1/risks/imports/preview", noAlias,
		map[string][]string{"column_map": {"title=Owner"}, "ignore_column": {"Owner"}}); status != http.StatusBadRequest {
		t.Errorf("mapped and ignored = %d, want 400; body=%s", status, raw)
	}

	// The same file and map stage: every row is staged, the ignored alias
	// column is kept as an unmapped value.
	status, raw = postForm(t, e, e.grcJWT, "/v1/risks/imports", noAlias, mapped)
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if b.StagedCount != 1 || b.RejectedCount != 0 || len(b.Staged) != 1 {
		t.Fatalf("batch = %+v", b)
	}
	r := b.Staged[0]
	if r.Title != "Phishing" || r.CategoryValue != "operational" || r.OwnerUserID != "" || r.OwnerUnlinked != "" ||
		r.Unmapped["Owner"] != "b@example.com" || r.Unmapped["Who"] != "a@example.com" {
		t.Errorf("row = %+v", r)
	}
}
