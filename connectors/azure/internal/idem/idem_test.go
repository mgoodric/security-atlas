package idem_test

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/idem"
)

func TestEntraRoleAssignmentKey_StableWithinHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if idem.EntraRoleAssignmentKey("assign-1", a) != idem.EntraRoleAssignmentKey("assign-1", b) {
		t.Error("same hour should yield same key")
	}
}

func TestEntraRoleAssignmentKey_DiffersAcrossHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	c := time.Date(2026, 6, 7, 13, 5, 0, 0, time.UTC)
	if idem.EntraRoleAssignmentKey("assign-1", a) == idem.EntraRoleAssignmentKey("assign-1", c) {
		t.Error("different hour should yield different key")
	}
}

func TestStorageAccountKey_StableWithinHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if idem.StorageAccountKey("acct-1", a) != idem.StorageAccountKey("acct-1", b) {
		t.Error("same hour should yield same key")
	}
}

func TestAKSClusterConfigKey_StableWithinHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if idem.AKSClusterConfigKey("clu-1", a) != idem.AKSClusterConfigKey("clu-1", b) {
		t.Error("same hour should yield same key")
	}
}

func TestAKSClusterConfigKey_DiffersAcrossHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	c := time.Date(2026, 6, 7, 13, 5, 0, 0, time.UTC)
	if idem.AKSClusterConfigKey("clu-1", a) == idem.AKSClusterConfigKey("clu-1", c) {
		t.Error("different hour should yield different key")
	}
}

func TestNSGRulesKey_StableWithinHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 7, 12, 55, 0, 0, time.UTC)
	if idem.NSGRulesKey("nsg-1", a) != idem.NSGRulesKey("nsg-1", b) {
		t.Error("same hour should yield same key")
	}
}

func TestNSGRulesKey_DiffersAcrossHour(t *testing.T) {
	a := time.Date(2026, 6, 7, 12, 5, 0, 0, time.UTC)
	c := time.Date(2026, 6, 7, 13, 5, 0, 0, time.UTC)
	if idem.NSGRulesKey("nsg-1", a) == idem.NSGRulesKey("nsg-1", c) {
		t.Error("different hour should yield different key")
	}
}

func TestKeys_DistinctAcrossKinds(t *testing.T) {
	at := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	// Same id string, different kind prefix → different key.
	if idem.EntraRoleAssignmentKey("x", at) == idem.StorageAccountKey("x", at) {
		t.Error("entra vs storage must not collide on the same id")
	}
	if idem.AKSClusterConfigKey("x", at) == idem.StorageAccountKey("x", at) {
		t.Error("aks vs storage must not collide on the same id")
	}
	if idem.AKSClusterConfigKey("x", at) == idem.EntraRoleAssignmentKey("x", at) {
		t.Error("aks vs entra must not collide on the same id")
	}
	if idem.NSGRulesKey("x", at) == idem.AKSClusterConfigKey("x", at) {
		t.Error("nsg vs aks must not collide on the same id")
	}
	if idem.NSGRulesKey("x", at) == idem.StorageAccountKey("x", at) {
		t.Error("nsg vs storage must not collide on the same id")
	}
}

func TestKeys_NonEmpty(t *testing.T) {
	at := time.Now()
	if idem.EntraRoleAssignmentKey("a", at) == "" || idem.StorageAccountKey("b", at) == "" ||
		idem.AKSClusterConfigKey("c", at) == "" || idem.NSGRulesKey("d", at) == "" {
		t.Fatal("idempotency keys must be non-empty")
	}
}
