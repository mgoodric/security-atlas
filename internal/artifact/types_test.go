package artifact_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/artifact"
)

// ISC-19 — StorageKeyForTenant must be two flat UUID segments with no
// user-controlled component. The tests below pin the canonical shape so
// any future regression that introduces a third segment, separator
// change, or user-supplied filename fragment fails fast.
func TestStorageKeyForTenant_Shape(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	art := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	got := artifact.StorageKeyForTenant(tenant, art)
	want := "tenant-11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222"
	if got != want {
		t.Fatalf("StorageKeyForTenant shape drift: got %q want %q", got, want)
	}
	if strings.Count(got, "/") != 1 {
		t.Fatalf("StorageKeyForTenant must have exactly one separator; got %q", got)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("StorageKeyForTenant must never contain `..`; got %q", got)
	}
}

// ISC-19 guard — different tenants get distinct prefixes. Concatenation
// alone is not enough; the prefix must isolate.
func TestStorageKeyForTenant_DifferentTenantsDistinctPrefix(t *testing.T) {
	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	art := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	keyA := artifact.StorageKeyForTenant(a, art)
	keyB := artifact.StorageKeyForTenant(b, art)
	if keyA == keyB {
		t.Fatalf("distinct tenants must produce distinct keys; got identical %q", keyA)
	}
	if !strings.HasPrefix(keyA, "tenant-"+a.String()+"/") {
		t.Fatalf("tenant A key missing prefix: %q", keyA)
	}
	if !strings.HasPrefix(keyB, "tenant-"+b.String()+"/") {
		t.Fatalf("tenant B key missing prefix: %q", keyB)
	}
}

// ISC-A2 / ISC-13 — ClampTTL must enforce the [DefaultDownloadTTL,
// MaxDownloadTTL] bound. Zero / negative → default; over-max → max;
// in-range → echoed back. This is the *only* code path that decides
// signed-URL TTL; tampering with the handler can't bypass it.
func TestClampTTL_Bounds(t *testing.T) {
	cases := []struct {
		name   string
		input  time.Duration
		expect time.Duration
	}{
		{"zero -> default", 0, artifact.DefaultDownloadTTL},
		{"negative -> default", -time.Hour, artifact.DefaultDownloadTTL},
		{"under max -> echoed", 10 * time.Minute, 10 * time.Minute},
		{"exact max -> max", artifact.MaxDownloadTTL, artifact.MaxDownloadTTL},
		{"over max -> clamped", 24 * time.Hour, artifact.MaxDownloadTTL},
	}
	for _, tc := range cases {
		got := artifact.ClampTTL(tc.input)
		if got != tc.expect {
			t.Errorf("%s: ClampTTL(%v) = %v want %v", tc.name, tc.input, got, tc.expect)
		}
	}
}

// ISC-13 guard — the constants themselves must satisfy
// DefaultDownloadTTL ≤ MaxDownloadTTL ≤ 1h. Pin the relationship so a
// careless edit can't widen the bound.
func TestDownloadTTLConstants(t *testing.T) {
	if artifact.MaxDownloadTTL > time.Hour {
		t.Fatalf("MaxDownloadTTL must not exceed 1h; got %v", artifact.MaxDownloadTTL)
	}
	if artifact.DefaultDownloadTTL > artifact.MaxDownloadTTL {
		t.Fatalf("DefaultDownloadTTL must be <= MaxDownloadTTL; got %v vs %v",
			artifact.DefaultDownloadTTL, artifact.MaxDownloadTTL)
	}
	if artifact.DefaultDownloadTTL <= 0 {
		t.Fatalf("DefaultDownloadTTL must be positive; got %v", artifact.DefaultDownloadTTL)
	}
}

// PayloadURI shape pin.
func TestPayloadURI(t *testing.T) {
	a := artifact.Artifact{
		StorageKey: "tenant-aaaa/bbbb",
	}
	got := a.PayloadURI("atlas-artifacts")
	want := "s3://atlas-artifacts/tenant-aaaa/bbbb"
	if got != want {
		t.Fatalf("PayloadURI shape: got %q want %q", got, want)
	}
}
