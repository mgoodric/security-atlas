// Slice 392 — contract-test-tier ROLLOUT (provider side: GET /v1/admin/demo/status).
//
// Pins the PROVIDER half of the BFF<->atlas wire contract for the demo
// status endpoint (slice 278). The recorded golden lives at
// web/lib/contracts/demo-status.golden.json and is asserted by the
// CONSUMER half (web/lib/contracts/demo-status.contract.test.ts) against
// the BFF at web/app/api/admin/demo/status/route.ts.
//
// Why this endpoint: slice 349's named secondary target. Trivial
// `{enabled: bool}` shape, but proves the tier generalizes to an
// admin-gated endpoint. The Status happy path needs only an admin
// credential in context + the injected isEnabled gate — no DB (only
// Seed/Teardown touch authPool), so it records on the plain
// `go test ./...` unit surface per ADR-0007.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/admindemo/ -run TestContract_DemoStatus -update
//
// The compare/-update plumbing mirrors the slice-349 pilot and the
// slice-392 shared helper in internal/api/contractrecord_test.go; it is
// re-declared here because Go test files cannot cross a package boundary.

package admindemo

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

const demoStatusGoldenRelPath = "../../../web/lib/contracts/demo-status.golden.json"

var demoContractUpdate = func() *bool {
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

type demoContractGolden struct {
	Comment  string                     `json:"_comment"`
	Endpoint string                     `json:"endpoint"`
	Variants map[string]json.RawMessage `json:"variants"`
}

func canonicalizeDemoJSON(t *testing.T, raw []byte) json.RawMessage {
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

// recordStatusVariant drives the real Status handler with an admin
// credential and the given enabled-gate value, returning the
// canonicalized body.
func recordStatusVariant(t *testing.T, enabled bool) json.RawMessage {
	t.Helper()
	h := New(nil, func() bool { return enabled })
	r := httptest.NewRequest(http.MethodGet, "/v1/admin/demo/status", nil)
	cred := credstore.Credential{
		ID:       "contract-admin",
		UserID:   uuid.NewString(),
		TenantID: uuid.NewString(),
		IsAdmin:  true,
	}
	r = r.WithContext(authctx.WithCredential(r.Context(), cred))
	w := httptest.NewRecorder()
	h.Status(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status variant (enabled=%v) returned %d; want 200", enabled, w.Code)
	}
	return canonicalizeDemoJSON(t, w.Body.Bytes())
}

func TestContract_DemoStatus(t *testing.T) {
	recorded := map[string]json.RawMessage{
		"enabled":  recordStatusVariant(t, true),
		"disabled": recordStatusVariant(t, false),
	}

	path := filepath.Clean(demoStatusGoldenRelPath)
	comment := "Slice 392 contract-test-tier ROLLOUT golden. Recorded by the PROVIDER side (internal/api/admindemo/status_contract_test.go) from the real Go handler at internal/api/admindemo/handler.go (Handler.Status). Regenerate with `go test ./internal/api/admindemo/ -run TestContract_DemoStatus -update`. The CONSUMER side (web/lib/contracts/demo-status.contract.test.ts) asserts the Next.js BFF (web/app/api/admin/demo/status/route.ts) against these recorded bodies."
	endpoint := "GET /v1/admin/demo/status"

	if demoContractUpdate != nil && *demoContractUpdate {
		out := demoContractGolden{Comment: comment, Endpoint: endpoint, Variants: recorded}
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
	var golden demoContractGolden
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
		want := canonicalizeDemoJSON(t, wantRaw)
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
