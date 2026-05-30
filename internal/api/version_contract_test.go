// Slice 392 — contract-test-tier ROLLOUT (provider side: GET /v1/version).
//
// Pins the PROVIDER half of the BFF<->atlas wire contract for the public
// version endpoint (slice 072). The recorded golden lives at
// web/lib/contracts/version.golden.json and is asserted by the CONSUMER
// half (web/lib/contracts/version.contract.test.ts) against the BFF at
// web/app/api/version/route.ts, which is a verbatim passthrough.
//
// Why this endpoint: slice 072 documents the four-field shape
// (version/commit/build_time/go_version) as a breaking-change boundary
// for both the BFF proxy and the VersionFooter component, but nothing
// tied the literal shape in the consumer's mocks to the Go struct — the
// exact gap ADR-0007 closes. Pure unit surface: VersionHandler takes a
// fieldsFn callback, so no DB / auth / tenancy is needed.
//
// Regenerate after an intentional shape change:
//
//	go test ./internal/api/ -run TestContract_Version -update

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const versionGoldenRelPath = "../../web/lib/contracts/version.golden.json"

// recordVersionVariant drives the real VersionHandler with the given
// field tuple and returns the canonicalized response body.
func recordVersionVariant(t *testing.T, fields VersionFields) json.RawMessage {
	t.Helper()
	h := NewVersionHandler(func() VersionFields { return fields })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("version variant returned status %d; want 200", rec.Code)
	}
	return canonicalizeJSON(t, rec.Body.Bytes())
}

func TestContract_Version(t *testing.T) {
	// The wire-shape variants the BFF must tolerate. `release` is a
	// fully-stamped build; `dev_build` is what an un-stamped `go build`
	// emits (sparse metadata) — both still carry all four fields because
	// VersionFields are non-pointer strings (empty, never absent).
	variants := map[string]VersionFields{
		"release": {
			Version:   "v1.5.0",
			Commit:    "abc1234",
			BuildTime: "2026-05-15T15:00:00Z",
			GoVersion: "go1.26.1",
		},
		"dev_build": {
			Version:   "dev",
			Commit:    "",
			BuildTime: "",
			GoVersion: "go1.26.1",
		},
	}

	recorded := make(map[string]json.RawMessage, len(variants))
	for name, f := range variants {
		recorded[name] = recordVersionVariant(t, f)
	}

	assertContractGolden(t,
		filepath.Clean(versionGoldenRelPath),
		"Slice 392 contract-test-tier ROLLOUT golden. Recorded by the PROVIDER side (internal/api/version_contract_test.go) from the real Go handler at internal/api/version.go (VersionHandler). Regenerate with `go test ./internal/api/ -run TestContract_Version -update`. The CONSUMER side (web/lib/contracts/version.contract.test.ts) asserts the Next.js BFF (web/app/api/version/route.ts) against these recorded bodies.",
		"GET /v1/version",
		recorded,
	)
}
