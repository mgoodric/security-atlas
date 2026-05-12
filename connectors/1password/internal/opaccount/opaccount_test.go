package opaccount_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opaccount"
	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

func newFake1Password(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/account") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInspect_PassesOnStrongPolicy(t *testing.T) {
	srv := newFake1Password(t, `{
		"id": "acme-corp",
		"name": "Acme Corp",
		"two_factor_required": true,
		"minimum_password_length": 14,
		"domain_restrictions_enabled": true,
		"active_member_count": 47
	}`)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)

	state, err := opaccount.Inspect(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if state.OrgID != "acme-corp" {
		t.Errorf("OrgID = %q", state.OrgID)
	}
	if !state.TwoFactorRequired {
		t.Error("TwoFactorRequired = false; want true")
	}
	if state.MinimumPasswordLength != 14 {
		t.Errorf("MinimumPasswordLength = %d", state.MinimumPasswordLength)
	}
	if !state.DomainRestrictionsEnabled {
		t.Error("DomainRestrictionsEnabled = false")
	}
	if state.ActiveMembers != 47 {
		t.Errorf("ActiveMembers = %d", state.ActiveMembers)
	}
	if state.Result != opaccount.ResultPass {
		t.Errorf("Result = %q; want pass", state.Result)
	}
}

func TestInspect_FailsWhenTwoFactorNotRequired(t *testing.T) {
	srv := newFake1Password(t, `{
		"id": "acme-corp",
		"two_factor_required": false,
		"minimum_password_length": 14
	}`)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)
	state, err := opaccount.Inspect(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if state.Result != opaccount.ResultFail {
		t.Errorf("Result = %q; want fail", state.Result)
	}
}

func TestInspect_FailsWhenPasswordLengthBelow12(t *testing.T) {
	srv := newFake1Password(t, `{
		"id": "acme-corp",
		"two_factor_required": true,
		"minimum_password_length": 8
	}`)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)
	state, err := opaccount.Inspect(context.Background(), c, nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if state.Result != opaccount.ResultFail {
		t.Errorf("Result = %q; want fail (length 8 < 12)", state.Result)
	}
}

func TestInspect_ErrorsOnHTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)
	if _, err := opaccount.Inspect(context.Background(), c, nil); err == nil {
		t.Fatal("expected error on HTTP 500; got nil")
	}
}

func TestInspect_RejectsEmptyOrgID(t *testing.T) {
	srv := newFake1Password(t, `{"id": "", "two_factor_required": true}`)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)
	if _, err := opaccount.Inspect(context.Background(), c, nil); err == nil {
		t.Fatal("expected error on empty org id; got nil")
	}
}

// Defensive: clamp negative ActiveMembers (schema requires minimum 0).
func TestInspect_RejectsNegativeMemberCount(t *testing.T) {
	srv := newFake1Password(t, `{
		"id": "acme-corp",
		"two_factor_required": true,
		"minimum_password_length": 14,
		"active_member_count": -1
	}`)
	creds, _ := opauth.Resolve(opauth.ResolveOpts{Token: "test-token"})
	c := opaccount.NewClient(srv.Client(), srv.URL, creds)
	if _, err := opaccount.Inspect(context.Background(), c, nil); err == nil {
		t.Fatal("expected error on negative member count; got nil")
	}
}

// Sanity: ensure the test fixture JSON parses cleanly under encoding/json
// so the integration test against the same response shape stays honest.
func TestRawResponse_RoundtripJSON(t *testing.T) {
	const body = `{"id":"acme","two_factor_required":true,"minimum_password_length":14,"active_member_count":47,"domain_restrictions_enabled":true}`
	var got opaccount.RawAccount
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != "acme" || got.MinimumPasswordLength != 14 || got.ActiveMembers != 47 {
		t.Fatalf("decoded shape wrong: %+v", got)
	}
}
