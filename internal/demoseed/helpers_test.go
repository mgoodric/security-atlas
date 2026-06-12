// helpers_test.go — pure-Go helper coverage for the slice-205 demo
// seeder (slice 320 coverage lift).
//
// Load-bearing functions + branches covered:
//
//   - capitalize (fixtures.go:840) — empty input, lower-case first char,
//     non-lower first char.
//   - kindToConnector (fixtures.go:715) — happy "vendor.kind" split,
//     missing-dot fallback.
//   - riskScoreJSON (fixtures.go:834) — composes {likelihood, impact,
//     rating=L*I} JSON shape.
//   - fictionalUserEmail (fixtures.go:827) — lower-cases first name,
//     wraps idx mod len(fictionalPeople).
//   - buildEvidencePayload (fixtures.go:728) — default branch (unknown
//     kind) returns the demo-placeholder fallback. Happy paths are
//     covered by the integration test's evidence-kind iteration.
//   - withTenant + currentTenantOf (writers.go:625,629) — round-trip
//     through ctx.WithValue. Missing-value returns uuid.Nil (zero).
//   - nullableUUID (writers.go:636) — uuid.Nil → nil; non-zero → id.
//   - nonZeroOrSelf (writers.go:646) — uuid.Nil → fresh; non-zero → id.
//   - nonZeroOrTenant (writers.go:657) — actor zero → demo; actor
//     non-zero → actor.
//   - periodStatus (writers.go:665) — frozen=true → "frozen"; false →
//     "open".
//   - frozenHashOrNil (writers.go:677) — frozen=true returns 32-byte
//     sha256; frozen=false returns nil.
//   - frozenByOrNil (writers.go:687) — symmetric with frozenHashOrNil.
//   - sha256Of (writers.go:571) — emits a 32-byte digest; deterministic
//     for the same input.
//
// AC-2 honored: every test asserts a real branch; no test exists merely
// to instantiate a struct literal.
//
// AC-3 honored: every test names the load-bearing function + branch
// in its godoc; failures point at the actual code path.

package demoseed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestCapitalize_Branches verifies capitalize covers the empty-string
// fast-path, the lower-case branch, and the already-non-lower fall-through.
func TestCapitalize_Branches(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"a", "A"},
		{"abc", "Abc"},
		{"demo-tenant", "Demo-tenant"},
		{"Abc", "Abc"},   // already capital → unchanged
		{"1abc", "1abc"}, // first char not lower → unchanged
	}
	for _, c := range cases {
		got := capitalize(c.in)
		if got != c.want {
			t.Errorf("capitalize(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestKindToConnector_Branches verifies the happy "vendor.kind" split
// returns "vendor" + the no-dot fallback returns the whole string (since
// SplitN(s, ".", 2) returns the original string when no separator is
// present).
func TestKindToConnector_Branches(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"aws.s3.bucket_encryption_state", "aws"},
		{"github.repo_protection", "github"},
		{"okta.mfa_policy", "okta"},
		{"manual.attestation", "manual"},
		{"nodot", "nodot"}, // SplitN returns [s]; len>0 so we hit parts[0]
	}
	for _, c := range cases {
		got := kindToConnector(c.in)
		if got != c.want {
			t.Errorf("kindToConnector(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestRiskScoreJSON_Shape verifies riskScoreJSON emits the
// {likelihood,impact,rating} shape with rating = L * I.
func TestRiskScoreJSON_Shape(t *testing.T) {
	out := riskScoreJSON(3, 5)
	var parsed struct {
		Likelihood int `json:"likelihood"`
		Impact     int `json:"impact"`
		Rating     int `json:"rating"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("riskScoreJSON output not valid JSON: %v (%s)", err, out)
	}
	if parsed.Likelihood != 3 || parsed.Impact != 5 || parsed.Rating != 15 {
		t.Errorf("riskScoreJSON(3,5) = %s; want likelihood=3, impact=5, rating=15", out)
	}
}

// TestFictionalUserEmail_Shape verifies the helper lower-cases the
// first name and wraps `idx % len(fictionalPeople)`.
func TestFictionalUserEmail_Shape(t *testing.T) {
	if len(fictionalPeople) == 0 {
		t.Fatal("fictionalPeople is empty; cannot run this test")
	}
	for idx := 0; idx < len(fictionalPeople)*2+1; idx++ {
		got := fictionalUserEmail(idx)
		if !strings.Contains(got, "@") {
			t.Errorf("fictionalUserEmail(%d) = %q; missing '@'", idx, got)
		}
		if got != strings.ToLower(got) {
			t.Errorf("fictionalUserEmail(%d) = %q; not all lower case", idx, got)
		}
		// Must contain the domain.
		if !strings.HasSuffix(got, "@"+personEmailDomain) {
			t.Errorf("fictionalUserEmail(%d) = %q; want suffix @%s", idx, got, personEmailDomain)
		}
	}
}

// TestBuildEvidencePayload_Default verifies the switch's default branch
// (unknown evidence_kind) returns the demo-placeholder fallback.
// The happy-path cases are covered by the integration test's
// evidence-kind iteration (writeEvidence walks every kind).
func TestBuildEvidencePayload_Default(t *testing.T) {
	payload := buildEvidencePayload("totally.unknown.kind", 0)
	if payload == nil {
		t.Fatal("buildEvidencePayload default returned nil")
	}
	note, ok := payload["note"].(string)
	if !ok || note != "demo placeholder" {
		t.Errorf("default branch payload = %+v; want note=demo placeholder", payload)
	}
}

// TestBuildEvidencePayload_KnownKindNonNil verifies every known kind
// returns a non-nil payload (defense against future maintainer breaking
// a known-kind branch). Does NOT assert payload contents — those are
// schema-level concerns covered by the integration test.
func TestBuildEvidencePayload_KnownKindNonNil(t *testing.T) {
	for _, kind := range evidenceKindsPool {
		got := buildEvidencePayload(kind, 1)
		if got == nil {
			t.Errorf("buildEvidencePayload(%q) returned nil; every known kind must produce a payload", kind)
		}
	}
}

// TestWithTenant_Roundtrip verifies withTenant stores + currentTenantOf
// retrieves the tenant uuid through ctx.WithValue.
func TestWithTenant_Roundtrip(t *testing.T) {
	id := uuid.New()
	ctx := withTenant(context.Background(), id)
	got := currentTenantOf(ctx)
	if got != id {
		t.Errorf("currentTenantOf returned %v; want %v", got, id)
	}
}

// TestCurrentTenantOf_Missing verifies the zero-value return when no
// tenant was stashed on the context.
func TestCurrentTenantOf_Missing(t *testing.T) {
	got := currentTenantOf(context.Background())
	if got != uuid.Nil {
		t.Errorf("currentTenantOf on bare ctx = %v; want uuid.Nil", got)
	}
}

// TestNullableUUID_Branches verifies nil → nil; non-zero → id (returned
// as the `any` interface for direct INSERT arg use).
func TestNullableUUID_Branches(t *testing.T) {
	if got := nullableUUID(uuid.Nil); got != nil {
		t.Errorf("nullableUUID(uuid.Nil) = %v; want nil", got)
	}
	id := uuid.New()
	got := nullableUUID(id)
	if got == nil {
		t.Errorf("nullableUUID(non-zero) = nil; want %v", id)
	}
	// Should be the same uuid (boxed).
	if u, ok := got.(uuid.UUID); !ok || u != id {
		t.Errorf("nullableUUID(non-zero) = %v (%T); want %v", got, got, id)
	}
}

// TestNonZeroOrSelf_Branches verifies uuid.Nil → fresh non-zero uuid;
// non-zero → returned unchanged.
func TestNonZeroOrSelf_Branches(t *testing.T) {
	out := nonZeroOrSelf(uuid.Nil)
	if out == uuid.Nil {
		t.Error("nonZeroOrSelf(uuid.Nil) returned uuid.Nil; want fresh UUID")
	}
	id := uuid.New()
	if got := nonZeroOrSelf(id); got != id {
		t.Errorf("nonZeroOrSelf(non-zero) = %v; want %v", got, id)
	}
}

// TestNonZeroOrTenant_Branches verifies actor-zero → returns demo;
// actor-non-zero → returns actor.
func TestNonZeroOrTenant_Branches(t *testing.T) {
	demo := uuid.New()
	if got := nonZeroOrTenant(uuid.Nil, demo); got != demo {
		t.Errorf("nonZeroOrTenant(Nil, demo) = %v; want %v", got, demo)
	}
	actor := uuid.New()
	if got := nonZeroOrTenant(actor, demo); got != actor {
		t.Errorf("nonZeroOrTenant(actor, demo) = %v; want %v", got, actor)
	}
}

// TestPeriodStatus_Branches verifies frozen=true → "frozen"; false →
// "open" (used as the audit_periods.status column value).
func TestPeriodStatus_Branches(t *testing.T) {
	if got := periodStatus(true); got != "frozen" {
		t.Errorf("periodStatus(true) = %q; want frozen", got)
	}
	if got := periodStatus(false); got != "open" {
		t.Errorf("periodStatus(false) = %q; want open", got)
	}
}

// TestFrozenHashOrNil_Branches verifies frozen=true returns a 32-byte
// sha256 digest; frozen=false returns nil. Schema invariant: the
// audit_periods.frozen_hash column has CHECK octet_length=32 when
// status='frozen' and NULL otherwise.
func TestFrozenHashOrNil_Branches(t *testing.T) {
	openP := &auditPeriodFixture{Frozen: false}
	if got := frozenHashOrNil(openP); got != nil {
		t.Errorf("frozenHashOrNil(open) = %v; want nil", got)
	}
	frozenP := &auditPeriodFixture{Frozen: true, ID: uuid.New(), Name: "Q1"}
	got := frozenHashOrNil(frozenP)
	if len(got) != 32 {
		t.Errorf("frozenHashOrNil(frozen) returned %d bytes; want 32 (sha256)", len(got))
	}
}

// TestFrozenByOrNil_Branches verifies frozen=true returns the FrozenBy
// string (typed as `any` for direct INSERT-arg use); frozen=false
// returns nil.
func TestFrozenByOrNil_Branches(t *testing.T) {
	openP := &auditPeriodFixture{Frozen: false, FrozenBy: "ignored"}
	if got := frozenByOrNil(openP); got != nil {
		t.Errorf("frozenByOrNil(open) = %v; want nil", got)
	}
	frozenP := &auditPeriodFixture{Frozen: true, FrozenBy: "system:demo"}
	got := frozenByOrNil(frozenP)
	if got != "system:demo" {
		t.Errorf("frozenByOrNil(frozen) = %v; want system:demo", got)
	}
}

// TestSha256Of_Length verifies sha256Of returns a 32-byte digest +
// deterministic output for the same input.
func TestSha256Of_Length(t *testing.T) {
	out1 := sha256Of("hello")
	if len(out1) != 32 {
		t.Errorf("sha256Of returned %d bytes; want 32", len(out1))
	}
	out2 := sha256Of("hello")
	if string(out1) != string(out2) {
		t.Error("sha256Of is non-deterministic for the same input")
	}
	out3 := sha256Of("world")
	if string(out1) == string(out3) {
		t.Error("sha256Of returned identical output for different inputs")
	}
}

// TestDemoSCFAnchors_Invariants verifies the slice-682 posture-spine anchor
// fixture honors invariant #7 (anchor to REAL SCF codes, never a parallel
// taxonomy): every entry has a non-empty SCF code + family + title, the codes
// match the SCF FAM-NN shape, and there are no duplicate codes (the
// scf_anchors (framework_version_id, scf_id) UNIQUE would reject duplicates
// at the DB layer; this catches it before the round-trip). At least 3 anchors
// must exist so the demo spine produces a legible, partially-covered tile.
func TestDemoSCFAnchors_Invariants(t *testing.T) {
	if len(demoSCFAnchors) < 3 {
		t.Fatalf("demoSCFAnchors has %d entries; want >= 3 for a legible posture tile", len(demoSCFAnchors))
	}
	seen := make(map[string]bool, len(demoSCFAnchors))
	for i, a := range demoSCFAnchors {
		if a.SCFID == "" || a.Family == "" || a.Title == "" {
			t.Errorf("demoSCFAnchors[%d] has an empty field: %+v", i, a)
		}
		// SCF codes are FAM-NN (e.g. IAC-06): a 3-letter family, a hyphen,
		// then digits. We assert the hyphen + a digit tail rather than a full
		// regex to stay dependency-free.
		dash := strings.IndexByte(a.SCFID, '-')
		if dash <= 0 || dash == len(a.SCFID)-1 {
			t.Errorf("demoSCFAnchors[%d] scf_id %q is not FAM-NN shaped", i, a.SCFID)
		}
		if seen[a.SCFID] {
			t.Errorf("demoSCFAnchors has duplicate scf_id %q; the (fv, scf_id) UNIQUE would reject it", a.SCFID)
		}
		seen[a.SCFID] = true
	}
}
