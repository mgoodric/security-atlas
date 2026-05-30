// Slice 349 — contract-test-tier PILOT (provider side).
//
// This is the proof-of-concept for the option-1 (golden-file) contract
// tier evaluated in docs/adr/0007-contract-test-tier.md. It pins the
// PROVIDER half of the BFF<->atlas wire contract for one endpoint pair
// (GET /v1/install-state) so a drift in the Go handler's response shape
// is caught at the provider, and the recorded golden is the single
// source of truth the Next.js BFF's consumer-side test asserts against.
//
// Why this endpoint: slice 210 fixed a real BE/FE contract bug here —
// the install-state response gained a `tenant_id` field that the slice
// 209 login form needed, and the silent mock-vs-reality gap (the BFF's
// vitest mocks hand-write `{first_install: true}`) is exactly the
// failure mode Q-1 (slice 333) and P-1 (slice 334) describe.
//
// How the tier works (two halves, one golden):
//
//	provider (this file):  exercise the real handler -> serialize the
//	                       body -> diff against web/lib/contracts/
//	                       install-state.golden.json. Mismatch = the Go
//	                       shape changed without regenerating the golden.
//	consumer (vitest):     read the same golden -> assert the BFF's
//	                       passthrough + field assumptions hold against
//	                       the recorded provider truth.
//
// Regenerate the golden after an intentional shape change:
//
//	go test ./internal/api/ -run TestContract_InstallState -update
//
// Runs on the plain `go test ./...` unit surface (no DB) because the
// handler reads through the PlatformStatus interface, which the
// existing fakePlatformStatus already satisfies.

package api

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
)

// updateGolden is registered lazily so it composes with whatever flag
// set the surrounding `go test` invocation uses without colliding with
// other -update flags in sibling packages.
var updateGolden = func() *bool {
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

// installStateGoldenPath is relative to this test file's package dir
// (internal/api). The golden lives under web/lib/contracts so the
// Next.js consumer-side test can import it without reaching across a
// module boundary it cannot resolve.
const installStateGoldenRelPath = "../../web/lib/contracts/install-state.golden.json"

// installStateGolden mirrors the JSON committed at the golden path. The
// `variants` map keys are stable contract identifiers shared verbatim
// with the consumer-side test.
type installStateGolden struct {
	Comment  string                     `json:"_comment"`
	Endpoint string                     `json:"endpoint"`
	Variants map[string]json.RawMessage `json:"variants"`
}

// recordInstallStateVariant drives the real handler with the given
// PlatformStatus and returns the canonicalized response body. Canonical
// = re-marshalled through encoding/json with sorted keys so the golden
// is byte-stable regardless of struct field order.
func recordInstallStateVariant(t *testing.T, ps *fakePlatformStatus) json.RawMessage {
	t.Helper()
	srv := New(Config{})
	srv.AttachPlatformStatus(ps)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/install-state", nil)
	srv.handleInstallState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("variant returned status %d; want 200 (contract variants are happy-path only)", rec.Code)
	}
	// Re-marshal through a map to canonicalize key order. encoding/json
	// sorts map keys, giving a deterministic golden body.
	var generic map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &generic); err != nil {
		t.Fatalf("decode handler body: %v", err)
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("canonicalize body: %v", err)
	}
	return canon
}

func TestContract_InstallState(t *testing.T) {
	// The three wire-shape variants the BFF must tolerate. These keys
	// are the contract surface shared with the consumer-side vitest test.
	variants := map[string]*fakePlatformStatus{
		"fresh_install_with_tenant": {
			firstInstall:      true,
			bootstrapTenantID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		},
		"fresh_install_without_tenant": {
			firstInstall: true,
		},
		"post_first_install": {
			firstInstall: false,
		},
	}

	recorded := make(map[string]json.RawMessage, len(variants))
	for name, ps := range variants {
		recorded[name] = recordInstallStateVariant(t, ps)
	}

	goldenPath := filepath.Clean(installStateGoldenRelPath)

	if updateGolden != nil && *updateGolden {
		writeInstallStateGolden(t, goldenPath, recorded)
		t.Logf("updated contract golden at %s", goldenPath)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to regenerate)", goldenPath, err)
	}
	var golden installStateGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("parse golden %s: %v", goldenPath, err)
	}

	for name, gotBody := range recorded {
		wantRaw, ok := golden.Variants[name]
		if !ok {
			t.Errorf("variant %q present in handler output but missing from golden; run -update", name)
			continue
		}
		// Canonicalize the golden variant the same way so the compare is
		// shape-equivalence, not byte-equivalence (tolerates the golden's
		// pretty-printing).
		var wantGeneric map[string]any
		if err := json.Unmarshal(wantRaw, &wantGeneric); err != nil {
			t.Errorf("variant %q golden is not an object: %v", name, err)
			continue
		}
		wantCanon, err := json.Marshal(wantGeneric)
		if err != nil {
			t.Errorf("variant %q canonicalize golden: %v", name, err)
			continue
		}
		if !bytes.Equal(gotBody, wantCanon) {
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/api/ -run TestContract_InstallState -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
}

// writeInstallStateGolden rewrites the golden under -update, preserving
// the human-readable structure (comment + endpoint + variants).
func writeInstallStateGolden(t *testing.T, path string, recorded map[string]json.RawMessage) {
	t.Helper()
	out := installStateGolden{
		Comment:  "Slice 349 contract-test-tier PILOT golden. Recorded by the PROVIDER side (internal/api/install_state_contract_test.go) from the real Go handler at internal/api/install_state.go. Regenerate with `go test ./internal/api/ -run TestContract_InstallState -update`. The CONSUMER side (web/lib/contracts/install-state.contract.test.ts) asserts the Next.js BFF's assumptions against these recorded bodies.",
		Endpoint: "GET /v1/install-state",
		Variants: recorded,
	}
	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	buf = append(buf, '\n')
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
}
