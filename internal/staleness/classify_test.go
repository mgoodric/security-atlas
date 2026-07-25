package staleness

import (
	"testing"
	"time"
)

// AC-13: pure-Go band classification without a DB. Table-driven, parallel.
func TestClassify_Bands(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	window := DefaultApproachingWindow

	ptr := func(d time.Duration) *time.Time {
		ts := now.Add(d)
		return &ts
	}

	cases := []struct {
		name string
		cell Cell
		want Band
	}{
		{
			name: "read model says stale -> BandStale",
			cell: Cell{ValidUntil: ptr(-1 * time.Hour), IsStale: true},
			want: BandStale,
		},
		{
			name: "no evidence (nil valid_until) -> BandStale",
			cell: Cell{ValidUntil: nil, IsStale: true},
			want: BandStale,
		},
		{
			name: "nil valid_until even if read model not stale -> BandStale (no horizon)",
			cell: Cell{ValidUntil: nil, IsStale: false},
			want: BandStale,
		},
		{
			name: "expires inside the approaching window -> BandApproaching",
			cell: Cell{ValidUntil: ptr(7 * 24 * time.Hour), IsStale: false},
			want: BandApproaching,
		},
		{
			name: "expires exactly at the window edge -> BandApproaching (inclusive)",
			cell: Cell{ValidUntil: ptr(window), IsStale: false},
			want: BandApproaching,
		},
		{
			name: "expires comfortably beyond the window -> BandFresh",
			cell: Cell{ValidUntil: ptr(60 * 24 * time.Hour), IsStale: false},
			want: BandFresh,
		},
		{
			name: "expires one second past the window -> BandFresh",
			cell: Cell{ValidUntil: ptr(window + time.Second), IsStale: false},
			want: BandFresh,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Classify(tc.cell, now, window)
			if got != tc.want {
				t.Fatalf("Classify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBand_String(t *testing.T) {
	t.Parallel()
	cases := map[Band]string{
		BandFresh:       "fresh",
		BandApproaching: "approaching",
		BandStale:       "stale",
		Band(99):        "fresh", // unknown falls back to the safe default token
	}
	for b, want := range cases {
		if got := b.String(); got != want {
			t.Errorf("Band(%d).String() = %q, want %q", b, got, want)
		}
	}
}
