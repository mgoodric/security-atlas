// Slice 392 — contract-test-tier ROLLOUT (shared provider-side helper).
//
// The slice-349 pilot (install_state_contract_test.go) inlined its
// canonicalize / read-golden / diff / -update plumbing. This slice rolls
// the tier out to more endpoints, so that plumbing is factored here to
// be shared by the new recorders in this package (version) without
// copy-paste. The pilot file is intentionally left untouched (it is the
// reference); the `me` and `admindemo` packages carry their own copies
// of this helper because Go test files cannot cross a package boundary.
//
// Pattern (identical to the pilot, ADR-0007 option 1):
//
//	provider test:  exercise the real handler -> canonicalize the body ->
//	                diff against the committed golden under
//	                web/lib/contracts/<endpoint>.golden.json. Mismatch =
//	                the Go shape changed without `-update`.
//	consumer test:  read the same golden -> assert the BFF's passthrough +
//	                field assumptions hold against the recorded truth.
//
// Regenerate any golden after an intentional shape change:
//
//	go test ./internal/... -run TestContract -update
//
// Runs on the plain `go test ./...` unit surface (no DB) — the covered
// handlers (version, me-synthetic, demo/status) read no Postgres on the
// recorded happy paths. That is what keeps the tier zero-new-gate
// (ADR-0007 (d): rides the Go-unit surface, no fifth CI job).

package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// contractUpdateFlag is registered lazily so it composes with whatever
// flag set the surrounding `go test` invocation uses (and with the
// pilot's own `-update` lookup in install_state_contract_test.go)
// without a duplicate-flag panic.
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

// canonicalizeJSON re-marshals a body through a generic map so the
// golden is byte-stable regardless of struct field order (encoding/json
// sorts map keys). nil/array bodies are passed through unchanged.
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

// assertContractGolden is the shared compare-or-update core. recorded is
// the map of canonicalized variant bodies the provider just produced;
// path is the golden file; comment/endpoint head the written golden.
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
		// Canonicalize the golden variant the same way so the compare is
		// shape-equivalence, not byte-equivalence (tolerates the golden's
		// pretty-printing).
		wantCanon := canonicalizeJSON(t, wantRaw)
		if !bytes.Equal(gotBody, wantCanon) {
			t.Errorf("variant %q wire shape drifted from golden:\n  handler: %s\n  golden:  %s\nrun `go test ./internal/... -run TestContract -update` if the change is intentional",
				name, gotBody, wantCanon)
		}
	}
	// Symmetric check: a variant in the golden the handler no longer
	// emits is also drift (a dropped variant).
	for name := range golden.Variants {
		if _, ok := recorded[name]; !ok {
			t.Errorf("variant %q present in golden but missing from handler output; run -update", name)
		}
	}
}
