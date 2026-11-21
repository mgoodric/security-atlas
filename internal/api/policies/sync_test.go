package policies

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #2850 pure-Go branches of POST /v1/policies:sync. The store path is in
// sync_integration_test.go.

func TestParseSyncPages(t *testing.T) {
	t.Parallel()
	props := func(extra string) json.RawMessage {
		return json.RawMessage(`{"version":"1.0","approved_by_name":"Jane Doe","approved_at":"2025-03-01","effective_date":"2025-03-15"` + extra + `}`)
	}
	pages, invalid := parseSyncPages([]syncPageReq{
		{ID: " 1 ", Title: "Valid", URL: "https://x.example/1", BodyMd: "Body.", Properties: props(`,"acknowledgment_required_roles":["everyone"]`)},
		{ID: "", Title: "No id", URL: "https://x.example/2", BodyMd: "Body.", Properties: props("")},
		{ID: "3", Title: "No url", BodyMd: "Body.", Properties: props("")},
		{ID: "4", Title: "No properties", URL: "https://x.example/4", BodyMd: "Body."},
		{ID: "5", Title: "Null properties", URL: "https://x.example/5", BodyMd: "Body.", Properties: json.RawMessage("null")},
		{ID: "6", Title: "Unknown property", URL: "https://x.example/6", BodyMd: "Body.", Properties: props(`,"title":"x"`)},
		{ID: "7", Title: "Bad audience", URL: "https://x.example/7", BodyMd: "Body.", Properties: props(`,"acknowledgment_required_roles":["employee","staff","employee"]`)},
		{ID: "8", Title: "", URL: "https://x.example/8", BodyMd: "Body.", Properties: props("")},
	})
	if len(pages) != 1 || pages[0].PageID != "1" || pages[0].In.Title != "Valid" || pages[0].In.SourceURL != "https://x.example/1" {
		t.Fatalf("pages = %+v", pages)
	}
	want := []struct{ id, reason string }{
		{"page 2", "missing page id"},
		{"3", "missing page url"},
		{"4", "missing page properties"},
		{"5", "missing page properties"},
		{"6", `page properties: json: unknown field "title"`},
		{"7", `acknowledgment_required_roles: unrecognised audience "employee", "staff"; choose from`},
		{"8", `missing required page property "title"`},
	}
	if len(invalid) != len(want) {
		t.Fatalf("invalid = %+v", invalid)
	}
	for i, w := range want {
		got := invalid[i]
		if got.PageID != w.id || got.Status != policy.SyncStatusSkipped || !strings.HasPrefix(got.Reason, w.reason) {
			t.Errorf("invalid[%d] = %+v; want %s skipped %q", i, got, w.id, w.reason)
		}
	}
}

func TestWireFromSyncReport(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	out := wireFromSyncReport(policy.SyncReport{Results: []policy.SyncResult{
		{PageID: "1", Status: policy.SyncStatusCreated, Policy: &policy.Policy{ID: id, Version: "2.0"}},
		{PageID: "2", Status: policy.SyncStatusUnchanged, Policy: &policy.Policy{ID: id, Version: "1.0"}},
		{PageID: "3", Status: policy.SyncStatusSkipped, Reason: "bad"},
	}})
	if out.Created != 1 || out.Unchanged != 1 || out.Skipped != 1 || len(out.Results) != 3 {
		t.Fatalf("out = %+v", out)
	}
	if out.Results[0].PolicyID != id.String() || out.Results[0].Version != "2.0" || out.Results[2].PolicyID != "" || out.Results[2].Reason != "bad" {
		t.Errorf("results = %+v", out.Results)
	}
}

// The handler refuses before any store call: no tenant (401), a
// credential that is not a policy-sync key (403), and a malformed request
// (400). A nil store proves no database is touched.
func TestSyncPolicies_RefusesBeforeStore(t *testing.T) {
	t.Parallel()
	tenant := uuid.NewString()
	syncKey := credstore.Credential{ID: "key_sync", TenantID: tenant, Purpose: credstore.PurposePolicySync}
	call := func(cred *credstore.Credential, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/policies:sync", bytes.NewReader([]byte(body)))
		ctx := req.Context()
		if cred != nil {
			ctx = authctx.WithCredential(ctx, *cred)
			tctx, err := tenancy.WithTenant(ctx, cred.TenantID)
			if err != nil {
				t.Fatalf("tenant ctx: %v", err)
			}
			ctx = tctx
		}
		w := httptest.NewRecorder()
		New(nil).SyncPolicies(w, req.WithContext(ctx))
		return w.Code
	}
	admin := credstore.Credential{ID: "key_admin", TenantID: tenant, IsAdmin: true}
	src := `"source":{"site":"acme","space":"SEC"}`
	for name, c := range map[string]struct {
		cred *credstore.Credential
		body string
		want int
	}{
		"no credential":    {nil, `{}`, http.StatusUnauthorized},
		"admin key":        {&admin, `{` + src + `,"pages":[]}`, http.StatusForbidden},
		"not json":         {&syncKey, `{`, http.StatusBadRequest},
		"no source":        {&syncKey, `{"pages":[]}`, http.StatusBadRequest},
		"blank space":      {&syncKey, `{"source":{"site":"acme","space":"  "},"pages":[]}`, http.StatusBadRequest},
		"long site":        {&syncKey, `{"source":{"site":"` + strings.Repeat("a", 501) + `","space":"SEC"},"pages":[]}`, http.StatusBadRequest},
		"too many pages":   {&syncKey, `{` + src + `,"pages":[` + strings.TrimSuffix(strings.Repeat(`{},`, maxSyncPages+1), ",") + `]}`, http.StatusBadRequest},
		"body over 16 MiB": {&syncKey, `{` + src + `,"pages":[{"body_md":"` + strings.Repeat("a", maxSyncBodyBytes) + `"}]}`, http.StatusRequestEntityTooLarge},
	} {
		if got := call(c.cred, c.body); got != c.want {
			t.Errorf("%s: status %d, want %d", name, got, c.want)
		}
	}
}
