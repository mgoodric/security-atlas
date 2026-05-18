package unifiedlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
)

func TestIsCanonical_AllNineKindsAccepted(t *testing.T) {
	t.Parallel()
	for _, k := range unifiedlog.AllKinds {
		if !unifiedlog.IsCanonical(k) {
			t.Errorf("AllKinds member %q is not canonical", k)
		}
	}
}

func TestIsCanonical_RejectsUnknown(t *testing.T) {
	t.Parallel()
	cases := []unifiedlog.Kind{"", "unknown", "DECISION", "audit_period_audit_log"}
	for _, k := range cases {
		k := k
		t.Run(string(k), func(t *testing.T) {
			t.Parallel()
			if unifiedlog.IsCanonical(k) {
				t.Errorf("Kind %q is unexpectedly canonical", k)
			}
		})
	}
}

func TestAllKinds_NineExactly(t *testing.T) {
	t.Parallel()
	// Slice 124 wires exactly nine per-domain audit-log tables. A change
	// to this count must come with a slice (extension or retirement).
	if got, want := len(unifiedlog.AllKinds), 9; got != want {
		t.Errorf("AllKinds len = %d; want %d", got, want)
	}
}

func TestQuery_RejectsZeroFromOrTo(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name   string
		params unifiedlog.QueryParams
	}{
		{"zero-from", unifiedlog.QueryParams{To: now, Limit: 10}},
		{"zero-to", unifiedlog.QueryParams{From: now.Add(-time.Hour), Limit: 10}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := unifiedlog.Query(context.Background(), nil, tc.params); err == nil {
				t.Fatal("expected error for missing from/to")
			}
		})
	}
}

func TestQuery_RejectsInvertedRange(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	_, _, err := unifiedlog.Query(context.Background(), nil, unifiedlog.QueryParams{
		From:  now,
		To:    now.Add(-time.Hour),
		Limit: 10,
	})
	if err == nil {
		t.Fatal("expected error for inverted from/to")
	}
}

func TestQuery_RejectsNonPositiveLimit(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []int{0, -1, -1000}
	for _, lim := range cases {
		lim := lim
		t.Run("", func(t *testing.T) {
			t.Parallel()
			_, _, err := unifiedlog.Query(context.Background(), nil, unifiedlog.QueryParams{
				From:  now.Add(-time.Hour),
				To:    now,
				Limit: lim,
			})
			if err == nil {
				t.Fatalf("expected error for limit=%d", lim)
			}
		})
	}
}

func TestQuery_RejectsNilQueries(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	_, _, err := unifiedlog.Query(context.Background(), nil, unifiedlog.QueryParams{
		From:  now.Add(-time.Hour),
		To:    now,
		Limit: 10,
	})
	if err == nil {
		t.Fatal("expected error for nil queries")
	}
}
