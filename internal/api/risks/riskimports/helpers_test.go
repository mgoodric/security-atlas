package riskimports

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Pure-Go tier (slice-353 Q-2): input parsing, error mapping and the
// deciding-person gate, no Postgres. The integration suite covers the
// stored paths.

func TestParseColumnMap(t *testing.T) {
	t.Parallel()
	got, err := parseColumnMap([]string{"external_ref = Ref No.", "title=Risk = Name"})
	if err != nil {
		t.Fatalf("parseColumnMap: %v", err)
	}
	if got["external_ref"] != "Ref No." || got["title"] != "Risk = Name" {
		t.Errorf("map = %v", got)
	}
	if m, err := parseColumnMap(nil); err != nil || m != nil {
		t.Errorf("empty = %v, %v", m, err)
	}
	for _, bad := range [][]string{{"title"}, {"=Header"}, {"title="}, {"title=A", "title=B"}} {
		if _, err := parseColumnMap(bad); err == nil {
			t.Errorf("parseColumnMap(%q) succeeded, want error", bad)
		}
	}
}

func TestWriteStageErrorStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err      error
		status   int
		contains string
	}{
		{riskimport.ErrUploadTooLarge, http.StatusRequestEntityTooLarge, "5 MiB size cap"},
		{riskimport.ErrWorkbookTooLarge, http.StatusRequestEntityTooLarge, "64 MiB uncompressed cap"},
		{riskimport.ErrTooManyRows, http.StatusBadRequest, "5000-row cap"},
		{riskimport.ErrTooManyColumns, http.StatusBadRequest, "past column 200"},
		{riskimport.ErrUnsupportedFormat, http.StatusBadRequest, ".csv or .xlsx"},
		{riskimport.ErrEmptyFile, http.StatusBadRequest, "empty"},
		{riskimport.ErrNoHeaderRow, http.StatusBadRequest, "title and a category"},
		{riskimport.ErrInvalidColumnMap, http.StatusBadRequest, "column map"},
		{riskimport.ErrMappedColumnAbsent, http.StatusBadRequest, "not in the file"},
		{errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeStageError(rec, httptest.NewRequest(http.MethodPost, "/v1/risks/imports", nil), tc.err)
		assertError(t, rec, tc.err, tc.status, tc.contains)
	}
}

func TestWriteDecisionErrorStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err      error
		status   int
		contains string
	}{
		{riskimport.ErrBatchNotFound, http.StatusNotFound, "import batch not found"},
		{riskimport.ErrRowNotFound, http.StatusNotFound, "staged row not found"},
		{riskimport.ErrRowDecided, http.StatusConflict, "already decided"},
		{riskimport.ErrApproverRequired, http.StatusForbidden, "deciding person"},
		{riskimport.ErrSelfDecision, http.StatusForbidden, "uploader of an import batch cannot approve or reject"},
		{riskimport.ErrReasonRequired, http.StatusBadRequest, "reason"},
		{riskimport.ErrInvalidDecision, http.StatusBadRequest, "invalid decision"},
		{errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeDecisionError(rec, httptest.NewRequest(http.MethodPost, "/v1/risks/imports/x/approve", nil), tc.err)
		assertError(t, rec, tc.err, tc.status, tc.contains)
	}
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, err error, status int, contains string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("%v: status = %d, want %d", err, rec.Code, status)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body["error"], contains) {
		t.Errorf("%v: error = %q, want it to contain %q", err, body["error"], contains)
	}
	if strings.Contains(body["error"], "riskimport:") || strings.Contains(body["error"], "db down") {
		t.Errorf("%v: error leaks internals: %q", err, body["error"])
	}
}

// TestDecidingPerson: only a person who is a tenant admin or holds the
// approver flag decides. A machine credential with the approver flag is
// refused, as is a person without approver rights, and a grc_engineer
// (#2881), whose role IsTenantApprover admits elsewhere.
func TestDecidingPerson(t *testing.T) {
	t.Parallel()
	tenant := uuid.NewString()
	user := uuid.New()
	cases := []struct {
		name   string
		cred   credstore.Credential
		status int
		want   string
	}{
		{"person approver", credstore.Credential{ID: "jwt:1", TenantID: tenant, UserID: "user:" + user.String(), IsApprover: true}, http.StatusOK, user.String()},
		{"key bound to a person", credstore.Credential{ID: "key_bound", TenantID: tenant, UserID: user.String(), IsApprover: true}, http.StatusOK, user.String()},
		{"machine token", credstore.Credential{ID: "jwt:2", TenantID: tenant, UserID: "oauth_client:ci", IsApprover: true}, http.StatusForbidden, "a person must approve"},
		{"unbound key", credstore.Credential{ID: "key_unbound", TenantID: tenant, UserID: "key_unbound", IsApprover: true}, http.StatusForbidden, "a person must approve"},
		{"person without approver rights", credstore.Credential{ID: "jwt:3", TenantID: tenant, UserID: "user:" + user.String()}, http.StatusForbidden, "an admin or approver must decide"},
		{"live tenant admin", credstore.Credential{ID: "jwt:4", TenantID: tenant, UserID: "user:" + user.String(), TenantRoles: []string{"admin"}}, http.StatusOK, user.String()},
		{"live grc_engineer", credstore.Credential{ID: "jwt:5", TenantID: tenant, UserID: "user:" + user.String(), TenantRoles: []string{"grc_engineer"}}, http.StatusForbidden, "a GRC engineer stages an import"},
		{"key bound to a grc_engineer", credstore.Credential{ID: "key_grc", TenantID: tenant, UserID: user.String(), TenantRoles: []string{"grc_engineer"}}, http.StatusForbidden, "a GRC engineer stages an import"},
	}
	for _, tc := range cases {
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/risks/imports/x/approve", nil).
			WithContext(authctx.WithCredential(ctx, tc.cred))
		rec := httptest.NewRecorder()
		got, ok := decidingPerson(rec, req)
		if tc.status == http.StatusOK {
			if !ok || got != tc.want {
				t.Errorf("%s: decidingPerson = %q, %v; want %q", tc.name, got, ok, tc.want)
			}
			continue
		}
		if ok || rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: ok=%v status=%d body=%s; want %d containing %q", tc.name, ok, rec.Code, rec.Body.String(), tc.status, tc.want)
		}
	}
}

// TestDecideRoutesRefuseGRCEngineer (#2881): approve-all, per-row approve
// and reject each refuse a grc_engineer with 403 before the store is
// reached (it is nil here).
func TestDecideRoutesRefuseGRCEngineer(t *testing.T) {
	t.Parallel()
	tenant := uuid.NewString()
	grc := credstore.Credential{ID: "jwt:grc", TenantID: tenant, UserID: "user:" + uuid.NewString(), TenantRoles: []string{"grc_engineer"}}
	h := New(nil)
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{{"approve all", h.Approve}, {"approve row", h.ApproveRow}, {"reject row", h.Reject}} {
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/risks/imports/x/approve", strings.NewReader(`{"reason":"no"}`)).
			WithContext(authctx.WithCredential(ctx, grc))
		rec := httptest.NewRecorder()
		tc.fn(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "a GRC engineer stages an import") {
			t.Errorf("%s as grc_engineer: status=%d body=%s; want 403", tc.name, rec.Code, rec.Body.String())
		}
	}
}

// TestCanDecide: the page's can_decide is the decide gate and not the
// uploader (#2881).
func TestCanDecide(t *testing.T) {
	t.Parallel()
	uploader, other := uuid.New(), uuid.New()
	batch := riskimport.Batch{UploaderUserID: uploader.String()}
	for _, tc := range []struct {
		name  string
		cred  credstore.Credential
		batch riskimport.Batch
		want  bool
	}{
		{"another approver", credstore.Credential{ID: "jwt:1", UserID: "user:" + other.String(), IsApprover: true}, batch, true},
		{"another live admin", credstore.Credential{ID: "jwt:2", UserID: "user:" + other.String(), TenantRoles: []string{"admin"}}, batch, true},
		{"the uploader, an approver", credstore.Credential{ID: "jwt:3", UserID: "user:" + uploader.String(), IsApprover: true}, batch, false},
		{"the uploader through a bound key", credstore.Credential{ID: "key_up", UserID: uploader.String(), IsApprover: true}, batch, false},
		{"a grc_engineer", credstore.Credential{ID: "jwt:4", UserID: "user:" + other.String(), TenantRoles: []string{"grc_engineer"}}, batch, false},
		{"a machine approver", credstore.Credential{ID: "jwt:5", UserID: "oauth_client:ci", IsApprover: true}, batch, false},
		{"an approver, no person uploaded", credstore.Credential{ID: "jwt:6", UserID: "user:" + uploader.String(), IsApprover: true}, riskimport.Batch{}, true},
	} {
		if got := canDecide(tc.cred, tc.batch); got != tc.want {
			t.Errorf("%s: canDecide = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHandlersRequireTenant(t *testing.T) {
	t.Parallel()
	h := New(nil) // never reached: the tenant guard refuses first
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{{"create", h.Create}, {"get", h.Get}, {"approve", h.Approve}, {"reject", h.Reject}, {"map value", h.MapValue}} {
		rec := httptest.NewRecorder()
		tc.fn(rec, httptest.NewRequest(http.MethodPost, "/v1/risks/imports", nil).WithContext(context.Background()))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without tenant = %d, want 401", tc.name, rec.Code)
		}
	}
}

func TestWriteValueErrorStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err      error
		status   int
		contains string
	}{
		{riskimport.ErrBatchNotFound, http.StatusNotFound, "import batch not found"},
		{riskimport.ErrValueNotFound, http.StatusNotFound, "no such value"},
		{riskimport.ErrBatchCompleted, http.StatusConflict, "completed"},
		{risk.ErrCategoryConflict, http.StatusConflict, "already in use"},
		{risk.ErrThemeConflict, http.StatusConflict, "already in use"},
		{riskimport.ErrInvalidValueKind, http.StatusBadRequest, "category or theme"},
		{&risk.InvalidCategoryError{Reason: "slug must be lowercase"}, http.StatusBadRequest, "slug must be lowercase"},
		{&risk.InvalidThemeError{Reason: "name must be lowercase"}, http.StatusBadRequest, "name must be lowercase"},
		{&riskimport.UnknownTargetError{Kind: "category", Target: "nope", Valid: []string{"operational"}}, http.StatusUnprocessableEntity, "valid: operational"},
		{errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeValueError(rec, httptest.NewRequest(http.MethodPut, "/v1/risks/imports/x/values", nil), tc.err)
		assertError(t, rec, tc.err, tc.status, tc.contains)
	}
}

// TestMapValueRefusals: a body that names no value or gives both target and
// create is a 400, and creating a category or theme needs a tenant
// approver. Each is refused before the store is reached (it is nil here).
func TestMapValueRefusals(t *testing.T) {
	t.Parallel()
	tenant := uuid.NewString()
	user := "user:" + uuid.NewString()
	approver := credstore.Credential{ID: "jwt:1", TenantID: tenant, UserID: user, IsApprover: true}
	person := credstore.Credential{ID: "jwt:2", TenantID: tenant, UserID: user}
	h := New(nil)
	for _, tc := range []struct {
		name   string
		cred   credstore.Credential
		body   string
		status int
		want   string
	}{
		{"no value", approver, `{"kind":"category","target":"operational"}`, http.StatusBadRequest, "value is required"},
		{"target and create", approver, `{"kind":"category","value":"Ops","target":"operational","create":{"slug":"ops","name":"Ops"}}`, http.StatusBadRequest, "not both"},
		{"unknown field", approver, `{"kind":"category","value":"Ops","slug":"ops"}`, http.StatusBadRequest, "unknown field"},
		{"create category without approver rights", person, `{"kind":"category","value":"Ops","create":{"slug":"ops","name":"Ops"}}`, http.StatusForbidden, "approver role required"},
		{"create theme without approver rights", person, `{"kind":"theme","value":"Vendors","create":{"name":"vendors"}}`, http.StatusForbidden, "approver role required"},
	} {
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/v1/risks/imports/x/values", strings.NewReader(tc.body)).
			WithContext(authctx.WithCredential(ctx, tc.cred))
		rec := httptest.NewRecorder()
		h.MapValue(rec, req)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: status=%d body=%s; want %d containing %q", tc.name, rec.Code, rec.Body.String(), tc.status, tc.want)
		}
	}
}
