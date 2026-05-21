package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// TestBuildInput_IsMachineActor_OAuthClientPrefix covers slice 196's
// extension to the slice-035 machine-actor detection: an OAuth
// client_credentials JWT lands in the credstore.Credential with a
// UserID of the form `oauth_client:<uuid>` (slice 188's
// MachineSubjectPrefix wired through jwtmw.Middleware). BuildInput
// must recognise that prefix as a machine actor so the slice-035
// system.rego carve-outs (evidence push for slice 034 connectors,
// upload-bundle for slice 196 bootstrap) fire.
//
// Prior to slice 196 the only machine-actor prefix was `key_`
// (slice-014/034 api_keys). This test pins the slice-196 widening.
func TestBuildInput_IsMachineActor_OAuthClientPrefix(t *testing.T) {
	t.Parallel()
	req := buildRequestWithCredential(credstore.Credential{
		ID:       "jwt:abc-123",
		TenantID: "00000000-0000-4000-8000-000000000001",
		UserID:   "oauth_client:0e3b4a7e-1111-2222-3333-444455556666",
	})
	in := authz.BuildInput(req, nil)
	got, ok := in.User.Attrs["is_machine_actor"].(bool)
	if !ok {
		t.Fatalf("is_machine_actor missing or wrong type: %#v", in.User.Attrs["is_machine_actor"])
	}
	if !got {
		t.Errorf("is_machine_actor = false; want true for oauth_client: prefix UserID")
	}
}

// TestBuildInput_IsMachineActor_KeyPrefixUnchanged is the regression
// guard: slice 196's widening of is_machine_actor must NOT break the
// existing slice-014/034 `key_` prefix detection.
func TestBuildInput_IsMachineActor_KeyPrefixUnchanged(t *testing.T) {
	t.Parallel()
	req := buildRequestWithCredential(credstore.Credential{
		ID:       "key_abc",
		TenantID: "00000000-0000-4000-8000-000000000001",
		UserID:   "key_abc",
	})
	in := authz.BuildInput(req, nil)
	got, _ := in.User.Attrs["is_machine_actor"].(bool)
	if !got {
		t.Errorf("is_machine_actor = false; want true for key_ prefix UserID")
	}
}

// TestBuildInput_IsMachineActor_HumanUserNotMachine pins the negative
// case: a real human user (UserID is a plain UUID, no slice-machine
// prefix) is NOT a machine actor. Important regression guard — if
// slice 196's widening accidentally caught all non-empty UserIDs as
// machine, the system.rego upload-bundle carve-out would open to
// every authenticated human.
func TestBuildInput_IsMachineActor_HumanUserNotMachine(t *testing.T) {
	t.Parallel()
	req := buildRequestWithCredential(credstore.Credential{
		ID:       "jwt:def-456",
		TenantID: "00000000-0000-4000-8000-000000000001",
		UserID:   "11111111-2222-3333-4444-555555555555",
	})
	in := authz.BuildInput(req, nil)
	got, _ := in.User.Attrs["is_machine_actor"].(bool)
	if got {
		t.Errorf("is_machine_actor = true; want false for plain-UUID human UserID")
	}
}

// buildRequestWithCredential is a small helper that attaches a
// credstore.Credential to a request context via authctx.WithCredential —
// the same path BuildInput uses to read the credential.
func buildRequestWithCredential(cred credstore.Credential) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", nil)
	ctx := authctx.WithCredential(context.Background(), cred)
	return r.WithContext(ctx)
}
