package eval

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/control/attestation"
)

// #2111: for manual.attestation evidence the latest attestation in the
// freshness window decides; automated evidence keeps "any fail fails".
func TestLatestAttestationWins(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	att := func(result string, observed, ingested time.Time, id string) inWindowRecord {
		return inWindowRecord{result: result, observedAt: observed, ingestedAt: ingested, id: id, kind: attestation.Kind}
	}
	auto := func(result string, observed time.Time) inWindowRecord {
		return inWindowRecord{result: result, observedAt: observed, ingestedAt: observed, id: "auto", kind: "sast.scan_result.v1"}
	}
	day := 24 * time.Hour

	cases := []struct {
		name string
		recs []inWindowRecord
		want string
	}{
		{
			name: "fail then pass: latest pass wins",
			recs: []inWindowRecord{att(ResultFail, t0, t0, "a"), att(ResultPass, t0.Add(day), t0.Add(day), "b")},
			want: ResultPass,
		},
		{
			name: "pass then fail: latest fail wins",
			recs: []inWindowRecord{att(ResultPass, t0, t0, "a"), att(ResultFail, t0.Add(day), t0.Add(day), "b")},
			want: ResultFail,
		},
		{
			name: "input order does not matter",
			recs: []inWindowRecord{att(ResultPass, t0.Add(day), t0.Add(day), "b"), att(ResultFail, t0, t0, "a")},
			want: ResultPass,
		},
		{
			name: "automated fail still fails over a latest pass attestation",
			recs: []inWindowRecord{auto(ResultFail, t0), att(ResultFail, t0, t0, "a"), att(ResultPass, t0.Add(day), t0.Add(day), "b")},
			want: ResultFail,
		},
		{
			name: "automated fail after the attestation fails",
			recs: []inWindowRecord{att(ResultPass, t0, t0, "a"), auto(ResultFail, t0.Add(day))},
			want: ResultFail,
		},
		{
			name: "automated pass with a latest fail attestation fails",
			recs: []inWindowRecord{auto(ResultPass, t0.Add(day)), att(ResultFail, t0, t0, "a")},
			want: ResultFail,
		},
		{
			name: "equal observed_at: later ingest wins",
			recs: []inWindowRecord{att(ResultPass, t0, t0.Add(time.Second), "a"), att(ResultFail, t0, t0, "b")},
			want: ResultPass,
		},
		{
			name: "backdated: the attestation claiming the later moment wins even when ingested first",
			recs: []inWindowRecord{att(ResultFail, t0.Add(time.Hour), t0.Add(time.Hour), "a"), att(ResultPass, t0, t0.Add(2*time.Hour), "b")},
			want: ResultFail,
		},
		{
			// #2509: a pass dated 20h ahead does not beat a fail made an
			// hour later; it counts from its ingest time.
			name: "future-dated pass loses to a later real fail",
			recs: []inWindowRecord{att(ResultPass, t0.Add(20*time.Hour), t0, "a"), att(ResultFail, t0.Add(time.Hour), t0.Add(time.Hour), "b")},
			want: ResultFail,
		},
		{
			name: "future-dated pass loses to a later real fail, reversed input",
			recs: []inWindowRecord{att(ResultFail, t0.Add(time.Hour), t0.Add(time.Hour), "b"), att(ResultPass, t0.Add(20*time.Hour), t0, "a")},
			want: ResultFail,
		},
		{
			name: "future-dated pass still wins over an earlier real fail",
			recs: []inWindowRecord{att(ResultFail, t0.Add(-time.Hour), t0.Add(-time.Hour), "a"), att(ResultPass, t0.Add(20*time.Hour), t0, "b")},
			want: ResultPass,
		},
		{
			name: "equal observed_at and ingested_at: greater id wins",
			recs: []inWindowRecord{att(ResultFail, t0, t0, "b"), att(ResultPass, t0, t0, "a")},
			want: ResultFail,
		},
		{
			name: "equal observed_at and ingested_at, reversed input: greater id still wins",
			recs: []inWindowRecord{att(ResultPass, t0, t0, "a"), att(ResultFail, t0, t0, "b")},
			want: ResultFail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := computeResult(latestAttestationWins(tc.recs)); got != tc.want {
				t.Fatalf("result = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLatestAttestationWins_PassesOtherRecordsThrough(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	recs := []inWindowRecord{
		{result: ResultFail, observedAt: t0, kind: "a.v1"},
		{result: ResultPass, observedAt: t0, kind: "b.v1"},
	}
	if got := latestAttestationWins(recs); len(got) != 2 || got[0].kind != "a.v1" || got[1].kind != "b.v1" {
		t.Fatalf("non-attestation records changed: %+v", got)
	}
	if got := latestAttestationWins(nil); len(got) != 0 {
		t.Fatalf("nil input: %+v", got)
	}
}

// The live path: computeRow reads kind / ingested_at / id off the ledger
// rows, and the in-window filter must carry them through.
func TestInWindowRecords_CarriesAttestationOrdering(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	all := []allRecord{{
		observedAt: now.Add(-time.Hour), result: ResultPass,
		kind: attestation.Kind, ingestedAt: now, id: "x",
	}}
	got := inWindowRecords(all, "monthly", now)
	if len(got) != 1 || got[0].kind != attestation.Kind || !got[0].ingestedAt.Equal(now) || got[0].id != "x" {
		t.Fatalf("in-window record lost its ordering fields: %+v", got)
	}
}
