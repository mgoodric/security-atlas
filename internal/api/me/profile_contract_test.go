// Slice 392 — contract-test-tier ROLLOUT (provider side: GET /v1/me).
//
// Pins the PROVIDER half of the BFF<->atlas wire contract for the
// identity/profile endpoint (slice 108/130). The recorded golden lives
// at web/lib/contracts/me.golden.json and is asserted by the CONSUMER
// half (web/lib/contracts/me.contract.test.ts) against the BFF at
// web/app/api/me/route.ts.
//
// Why this endpoint: high consumer coupling — this is the slice-210-class
// identity surface (the BFF + multiple frontend components read user_id /
// tenant_id / is_admin / roles). The wire shape carries a nullable
// time_zone and two always-present arrays (roles, owner_roles); a silent
// rename there is exactly the drift this tier catches.
//
// No-DB recording: GetMe -> buildProfile only calls users.Store.GetByID
// when cred.UserID parses as a UUID. With a non-UUID credential id (the
// API-key / bootstrap-admin path) buildProfile returns the SYNTHETIC
// profile with zero pool/store access, and resolveRoles returns [] when
// the resolver is nil. So NewProfile(nil, nil, nil) + an injected
// credential + tenancy context records the real wire shape on the plain
// `go test ./...` unit surface per ADR-0007.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/me/ -run TestContract_Me -update
//
// The compare/-update plumbing mirrors the slice-349 pilot and the
// slice-392 shared helper in internal/api/contractrecord_test.go; it is
// re-declared here because Go test files cannot cross a package boundary.

package me

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const meGoldenRelPath = "../../../web/lib/contracts/me.golden.json"

var meContractUpdate = func() *bool {
	if f := flag.Lookup("update"); f != nil {
		if gv, ok := f.Value.(flag.Getter); ok {
			if b, ok := gv.Get().(bool); ok {
				return &b
			}
		}
		return nil
	}
	return flag.Bool("update", false, "rewrite contract golden files")
}()

type meContractGolden struct {
	Comment  string                     `json:"_comment"`
	Endpoint string                     `json:"endpoint"`
	Variants map[string]json.RawMessage `json:"variants"`
}

func canonicalizeMeJSON(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("canonicalize: %v; body=%q", err, raw)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("canonicalize re-marshal: %v", err)
	}
	return out
}

// recordMeVariant drives the real GetMe handler through the no-DB
// synthetic-profile path. The credential id is intentionally a non-UUID
// ("key_…") so buildProfile never reaches the users store, and the
// resolver is nil so resolveRoles returns []. A valid (UUID) tenant id is
// set in both the credential and the tenancy context (authnContext
// requires both).
func recordMeVariant(t *testing.T, cred credstore.Credential) json.RawMessage {
	t.Helper()
	h := NewProfile(nil, nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	ctx := authctx.WithCredential(r.Context(), cred)
	ctx, err := tenancy.WithTenant(ctx, cred.TenantID)
	if err != nil {
		t.Fatalf("set tenancy context: %v", err)
	}
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetMe(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("me variant returned %d; want 200; body=%q", w.Code, w.Body.String())
	}
	return canonicalizeMeJSON(t, w.Body.Bytes())
}

func TestContract_Me(t *testing.T) {
	// A fixed tenant UUID keeps the golden byte-stable across runs (the
	// synthetic profile echoes the credential's tenant id verbatim).
	const tenantID = "22222222-2222-4222-8222-222222222222"

	recorded := map[string]json.RawMessage{
		// API-key admin, no users row: synthetic profile, tenant_role
		// "admin", is_admin true, roles [] (nil resolver).
		"synthetic_admin": recordMeVariant(t, credstore.Credential{
			ID:       "key_admin",
			UserID:   "key_admin",
			TenantID: tenantID,
			IsAdmin:  true,
			Last4:    "ad01",
		}),
		// Non-admin credential: tenant_role "user", is_admin false,
		// owner_roles carries a granted role to prove the array shape.
		"synthetic_non_admin": recordMeVariant(t, credstore.Credential{
			ID:         "key_viewer",
			UserID:     "key_viewer",
			TenantID:   tenantID,
			IsAdmin:    false,
			OwnerRoles: []string{"control_owner"},
			Last4:      "vw02",
		}),
	}

	path := filepath.Clean(meGoldenRelPath)
	comment := "Slice 392 contract-test-tier ROLLOUT golden. Recorded by the PROVIDER side (internal/api/me/profile_contract_test.go) from the real Go handler at internal/api/me/profile.go (ProfileHandler.GetMe) on the no-DB synthetic-profile path. Regenerate with `go test ./internal/api/me/ -run TestContract_Me -update`. The CONSUMER side (web/lib/contracts/me.contract.test.ts) asserts the Next.js BFF (web/app/api/me/route.ts) against these recorded bodies."
	endpoint := "GET /v1/me"

	if meContractUpdate != nil && *meContractUpdate {
		out := meContractGolden{Comment: comment, Endpoint: endpoint, Variants: recorded}
		buf, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		buf = append(buf, '\n')
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated contract golden at %s", path)
		return
	}

	rawGolden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to regenerate)", path, err)
	}
	var golden meContractGolden
	if err := json.Unmarshal(rawGolden, &golden); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}
	if golden.Endpoint != endpoint {
		t.Errorf("golden endpoint = %q; recorder = %q (run -update)", golden.Endpoint, endpoint)
	}
	for name, got := range recorded {
		wantRaw, ok := golden.Variants[name]
		if !ok {
			t.Errorf("variant %q missing from golden; run -update", name)
			continue
		}
		want := canonicalizeMeJSON(t, wantRaw)
		if !bytes.Equal(got, want) {
			t.Errorf("variant %q drifted:\n  handler: %s\n  golden:  %s\nrun -update if intentional", name, got, want)
		}
	}
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q in golden but not emitted; run -update", name)
		}
	}
}
