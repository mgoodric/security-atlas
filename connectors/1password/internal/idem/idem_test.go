package idem_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/idem"
)

func TestOrgPolicyKey_StableWithinHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	if a, b := idem.OrgPolicyKey("acme-corp", t1), idem.OrgPolicyKey("acme-corp", t2); a != b {
		t.Fatalf("keys differ within hour: %s vs %s", a, b)
	}
}

func TestOrgPolicyKey_RotatesOnHour(t *testing.T) {
	t1 := time.Date(2026, 5, 11, 14, 59, 59, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	if idem.OrgPolicyKey("acme-corp", t1) == idem.OrgPolicyKey("acme-corp", t2) {
		t.Fatal("keys identical across hour boundary")
	}
}

func TestOrgPolicyKey_DistinctPerOrg(t *testing.T) {
	now := time.Now().UTC()
	if idem.OrgPolicyKey("o1", now) == idem.OrgPolicyKey("o2", now) {
		t.Fatal("different orgs collided on key")
	}
}

func TestOrgPolicyKey_IsHex(t *testing.T) {
	k := idem.OrgPolicyKey("acme", time.Now())
	if len(k) != 64 {
		t.Fatalf("key length = %d; want 64 hex chars", len(k))
	}
	if strings.ContainsAny(k, "ghijklmnopqrstuvwxyz") {
		t.Fatalf("key not hex: %s", k)
	}
}
