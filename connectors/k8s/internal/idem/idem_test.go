package idem

import (
	"testing"
	"time"
)

func TestRBACBindingKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if RBACBindingKey("cluster", "", "admins", a) != RBACBindingKey("cluster", "", "admins", b) {
		t.Error("same binding within the hour should share a key")
	}
}

func TestRBACBindingKey_DiffersAcrossHour(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	if RBACBindingKey("cluster", "", "admins", a) == RBACBindingKey("cluster", "", "admins", b) {
		t.Error("different hour should differ")
	}
}

func TestRBACBindingKey_DiffersByIdentity(t *testing.T) {
	t.Parallel()
	now := time.Now()
	if RBACBindingKey("namespace", "default", "a", now) == RBACBindingKey("namespace", "default", "b", now) {
		t.Error("different binding names should differ")
	}
	if RBACBindingKey("cluster", "", "x", now) == RBACBindingKey("namespace", "default", "x", now) {
		t.Error("different scope/namespace should differ")
	}
}

func TestWorkloadKey_StableAndDistinct(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if WorkloadKey("Deployment", "prod", "api", a) != WorkloadKey("Deployment", "prod", "api", b) {
		t.Error("same workload within the hour should share a key")
	}
	if WorkloadKey("Deployment", "prod", "api", a) == WorkloadKey("DaemonSet", "prod", "api", a) {
		t.Error("different kind should differ")
	}
}

func TestKeys_AreHex64(t *testing.T) {
	t.Parallel()
	for _, k := range []string{
		RBACBindingKey("cluster", "", "x", time.Now()),
		WorkloadKey("Deployment", "n", "x", time.Now()),
	} {
		if len(k) != 64 {
			t.Errorf("key %q len = %d; want 64 (sha256 hex)", k, len(k))
		}
	}
}
