// Slice 687 — contract-test-tier rollout to the control-detail coverage tail
// route served by this package:
//
//	GET /v1/controls/{id}/coverage -> control-coverage.golden.json
//
// This is the slice-392 / slice-409 / slice-410 / slice-411 / slice-412
// shared-recorder helper copied into this package because Go test files cannot
// cross a package boundary (the same reason the sibling copies exist in
// internal/api/controlstate, internal/api/controldetail, internal/api/auditperiods,
// internal/api/risks, internal/api/dashboard, …).
//
// Pattern (ADR-0007 option 1, slice-412 per-route read seam, slice-412 D5
// rationale for a thin read-model seam over the tx-orchestrating assembler):
//
//	provider test:  construct the real Handler over an injected fixed-view
//	                single-method coverageAssembler stub (no pgx pool, no
//	                eval/scope/framework_scope stores) -> drive the real
//	                ControlCoverage handler -> canonicalize the body -> diff
//	                against the committed golden under web/lib/contracts/.
//	consumer test:  read the same golden -> assert the BFF passthrough holds
//	                against the recorded upstream truth. The coverage BFF
//	                (web/app/api/controls/[id]/coverage/route.ts) is a VERBATIM
//	                passthrough (NextResponse.json(coverage) over res.json()
//	                unchanged), so the consumer assert is toEqual(golden) like
//	                the slice-411 control-detail tabs.
//
// Regenerate the golden after an intentional shape change:
//
//	go test ./internal/api/ucfcoverage/ -run TestContract -update
//
// Runs on the plain `go test ./...` unit surface (no DB): the ControlCoverage
// path reads through the unexported single-method coverageAssembler seam
// (control_coverage.go), which the recorder satisfies with a fixed-view stub.
// That is what keeps the tier zero-new-gate (ADR-0007 (d): rides the Go-unit
// surface, no fifth CI job; slice 409 P0-409-1: no recorder on the integration
// surface).

package ucfcoverage

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// contractUpdateFlag is registered lazily so it composes with whatever flag
// set the surrounding `go test` invocation uses without a duplicate-flag
// panic (mirrors slice 392/409/410/411/412's lazy lookup).
var contractUpdateFlag = func() *bool {
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

// contractGolden mirrors the committed golden JSON. The variant keys are
// stable contract identifiers shared verbatim with the consumer test.
type contractGolden struct {
	Comment  string                     `json:"_comment"`
	Endpoint string                     `json:"endpoint"`
	Variants map[string]json.RawMessage `json:"variants"`
}

// canonicalizeJSON re-marshals a body through a generic value so the golden is
// byte-stable regardless of struct/map field order.
func canonicalizeJSON(t *testing.T, raw []byte) json.RawMessage {
	t.Helper()
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("canonicalize: decode body: %v; body=%q", err, raw)
	}
	out, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("canonicalize: re-marshal: %v", err)
	}
	return out
}

// assertContractGolden is the shared compare-or-update core.
func assertContractGolden(t *testing.T, path, comment, endpoint string, recorded map[string]json.RawMessage) {
	t.Helper()

	if contractUpdateFlag != nil && *contractUpdateFlag {
		out := contractGolden{Comment: comment, Endpoint: endpoint, Variants: recorded}
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
	var golden contractGolden
	if err := json.Unmarshal(rawGolden, &golden); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}
	if golden.Endpoint != endpoint {
		t.Errorf("golden endpoint = %q; recorder endpoint = %q (run -update)", golden.Endpoint, endpoint)
	}
	for name, gotBody := range recorded {
		wantRaw, ok := golden.Variants[name]
		if !ok {
			t.Errorf("variant %q present in handler output but missing from golden; run -update", name)
			continue
		}
		wantCanon := canonicalizeJSON(t, wantRaw)
		if !bytes.Equal(gotBody, wantCanon) {
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/api/ucfcoverage/ -run TestContract -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q present in golden but missing from handler output; run -update", name)
		}
	}
}
