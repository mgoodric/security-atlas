//go:build integration

package admincreds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #2849: an admin mints a policy-sync key through POST
// /v1/admin/credentials. The purpose is in the issue response, in the key
// list and in the api_key_issue audit row, and a rotation keeps it.
func TestIssue_PolicySyncKeyListedAuditedAndRotated(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; reading me_audit_log needs the migrate role")
	}
	tenantID := uuid.New()
	_, _, handler := newHandler(t, tenantID)
	defer cleanupTenant(tenantID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(ctx, "DELETE FROM me_audit_log WHERE tenant_id = $1", tenantID)
	}()

	body, _ := json.Marshal(admincreds.IssueRequest{Purpose: credstore.PurposePolicySync, Label: "Confluence sync"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("issue: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var issued admincreds.IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if issued.Purpose != credstore.PurposePolicySync {
		t.Fatalf("issue response purpose = %q, want policy-sync", issued.Purpose)
	}

	ordinary := httptest.NewRecorder()
	handler.ServeHTTP(ordinary, httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader([]byte(`{}`))))
	if ordinary.Code != http.StatusCreated {
		t.Fatalf("issue ordinary: want 201, got %d: %s", ordinary.Code, ordinary.Body.String())
	}
	var plain admincreds.IssueResponse
	_ = json.Unmarshal(ordinary.Body.Bytes(), &plain)

	// The api_key_issue row records the purpose.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var after []byte
	if err := adminPool.QueryRow(ctx,
		`SELECT after FROM me_audit_log WHERE tenant_id = $1 AND action = 'api_key_issue' AND after->>'key_id' = $2`,
		tenantID, issued.ID).Scan(&after); err != nil {
		t.Fatalf("read api_key_issue row: %v", err)
	}
	var audited struct {
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal(after, &audited); err != nil {
		t.Fatalf("decode audit after: %v", err)
	}
	if audited.Purpose != credstore.PurposePolicySync {
		t.Fatalf("api_key_issue after.purpose = %q, want policy-sync (row: %s)", audited.Purpose, after)
	}

	// A rotation keeps the purpose: it must not mint a general key.
	rotW := httptest.NewRecorder()
	handler.ServeHTTP(rotW, httptest.NewRequest(http.MethodPost, "/v1/admin/credentials/"+issued.ID+"/rotate", nil))
	if rotW.Code != http.StatusOK {
		t.Fatalf("rotate: want 200, got %d: %s", rotW.Code, rotW.Body.String())
	}
	var rot admincreds.RotateResponse
	_ = json.Unmarshal(rotW.Body.Bytes(), &rot)

	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, httptest.NewRequest(http.MethodGet, "/v1/admin/credentials", nil))
	var list admincreds.ListResponse
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	purposes := map[string]string{}
	for _, it := range list.Items {
		purposes[it.ID] = it.Purpose
	}
	for id, want := range map[string]string{
		issued.ID: credstore.PurposePolicySync,
		rot.ID:    credstore.PurposePolicySync,
		plain.ID:  "",
	} {
		got, ok := purposes[id]
		if !ok {
			t.Errorf("%s missing from list %v", id, purposes)
		} else if got != want {
			t.Errorf("%s purpose = %q, want %q", id, got, want)
		}
	}
}
