package idem

import (
	"testing"
	"time"
)

func TestKey_SameHourSameAnchorIsStable(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 11, 14, 5, 0, 0, time.UTC)
	b := time.Date(2026, 6, 11, 14, 59, 59, 0, time.UTC)
	if Key("U123", a) != Key("U123", b) {
		t.Error("two observations of the same anchor in the same hour must collapse to one key")
	}
}

func TestKey_NewHourNewKey(t *testing.T) {
	t.Parallel()
	a := time.Date(2026, 6, 11, 14, 0, 0, 0, time.UTC)
	b := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
	if Key("U123", a) == Key("U123", b) {
		t.Error("crossing an hour boundary must produce a fresh key")
	}
}

func TestKey_DifferentAnchorDifferentKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 11, 14, 0, 0, 0, time.UTC)
	if Key("U123", now) == Key("U456", now) {
		t.Error("different anchors must produce different keys")
	}
}

func TestKey_LocalTimeNormalizedToUTC(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC+5", 5*3600)
	local := time.Date(2026, 6, 11, 19, 30, 0, 0, loc) // == 14:30 UTC
	utc := time.Date(2026, 6, 11, 14, 30, 0, 0, time.UTC)
	if Key("U123", local) != Key("U123", utc) {
		t.Error("local-time skew must be normalized to UTC before truncation")
	}
}

func TestEventKey_StableByEntryID(t *testing.T) {
	t.Parallel()
	id, date := "entry-1", int64(1700000000)
	first := EventKey(id, date)
	second := EventKey(id, date)
	if first != second {
		t.Error("same entry id + date must be stable")
	}
	if first == EventKey("entry-2", date) {
		t.Error("different entry ids must differ")
	}
}
