package idem

import (
	"testing"
	"time"
)

func TestKey_StableWithinHour(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 12, 14, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 12, 14, 55, 0, 0, time.UTC)
	if Key("anchor", a) != Key("anchor", b) {
		t.Error("keys within the same hour must match")
	}
}

func TestKey_DiffersAcrossHour(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 12, 14, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 12, 15, 5, 0, 0, time.UTC)
	if Key("anchor", a) == Key("anchor", b) {
		t.Error("keys across an hour boundary must differ")
	}
}

func TestKey_DiffersByAnchor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 12, 14, 5, 0, 0, time.UTC)
	if Key("anchor-a", now) == Key("anchor-b", now) {
		t.Error("different anchors must produce different keys")
	}
}

func TestKey_TimezoneNormalized(t *testing.T) {
	t.Parallel()
	utc := time.Date(2026, 6, 12, 14, 5, 0, 0, time.UTC)
	loc := time.FixedZone("X", 3*3600)
	local := utc.In(loc) // same instant, different zone
	if Key("anchor", utc) != Key("anchor", local) {
		t.Error("the same instant in different zones must produce the same key")
	}
}
