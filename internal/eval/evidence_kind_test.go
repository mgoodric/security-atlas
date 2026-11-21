package eval

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #2818 (ADR 0028 decision 5): each evidence query sees only the records of
// the evidence_kind its manifest declares.

const (
	kindMFA      = "okta.mfa_policy.v1"
	kindPosture  = "osquery.host_posture.v1"
	queryMFA     = "$.applies_to_everyone"
	queryPosture = "$.firewall_enabled"
)

// mixedKindRecords holds one passing record of each kind. Each query's
// JSON-path reads a field only its own kind carries, so a query handed the
// other kind's record finds no match and fails it.
func mixedKindRecords(now time.Time) []inWindowRecord {
	return []inWindowRecord{
		{result: ResultPass, observedAt: now, kind: kindMFA, payload: []byte(`{"applies_to_everyone": true}`)},
		{result: ResultPass, observedAt: now, kind: kindPosture, payload: []byte(`{"firewall_enabled": true}`)},
	}
}

func TestRecordsOfKind(t *testing.T) {
	t.Parallel()
	recs := []inWindowRecord{{id: "a", kind: "k1"}, {id: "b", kind: "k2"}, {id: "c"}, {id: "d", kind: "k1"}}
	ids := func(rs []inWindowRecord) string {
		s := ""
		for _, r := range rs {
			s += r.id
		}
		return s
	}
	cases := []struct {
		name          string
		kind          string
		matchUnkinded bool
		want          string
	}{
		{"no declared kind sees every record", "", false, "abcd"},
		{"declared kind sees only its records, in order", "k1", false, "ad"},
		{"a record with no kind is excluded live", "k2", false, "b"},
		{"fixture: a record with no kind matches", "k2", true, "bc"},
		{"unmatched kind sees nothing", "k3", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ids(recordsOfKind(recs, tc.kind, tc.matchUnkinded)); got != tc.want {
				t.Fatalf("recordsOfKind(%q, %v) = %q, want %q", tc.kind, tc.matchUnkinded, got, tc.want)
			}
		})
	}
}

// A control with two queries over two kinds evaluates each query on its own
// records, with records of both kinds present.
func TestEvalQueries_TwoQueriesTwoKinds(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	meta := controlMeta{id: uuid.New(), queries: []evidenceQueryManifest{
		{Language: "jsonpath", Expression: queryMFA, EvidenceKind: kindMFA},
		{Language: "jsonpath", Expression: queryPosture, EvidenceKind: kindPosture},
	}}
	got, err := (&Engine{}).evalQueries(context.Background(), nil, meta, mixedKindRecords(now))
	if err != nil {
		t.Fatal(err)
	}
	if got != ResultPass {
		t.Fatalf("result = %q, want pass: each query should see only its own kind's record", got)
	}

	// A failing record of one kind fails only through its own query.
	recs := append(mixedKindRecords(now), inWindowRecord{result: ResultPass, observedAt: now, kind: kindPosture, payload: []byte(`{"firewall_enabled": false}`)})
	got, err = (&Engine{}).evalQueries(context.Background(), nil, meta, recs)
	if err != nil {
		t.Fatal(err)
	}
	if got != ResultFail {
		t.Fatalf("result = %q, want fail from the posture query", got)
	}
}

// A query that declares no kind keeps the pre-#2818 behaviour: it sees every
// record the control receives, so here it reads the other kind's record and
// fails it.
func TestEvalQueries_NoDeclaredKindSeesEveryRecord(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	meta := controlMeta{id: uuid.New(), queries: []evidenceQueryManifest{
		{Language: "jsonpath", Expression: queryMFA},
	}}
	got, err := (&Engine{}).evalQueries(context.Background(), nil, meta, mixedKindRecords(now))
	if err != nil {
		t.Fatal(err)
	}
	if got != ResultFail {
		t.Fatalf("result = %q, want fail: a kindless query reads the posture record too", got)
	}
}

// A query whose kind has no record in the window sees none, and its roll-up
// over nothing is inconclusive, as for an empty ledger.
func TestEvalQueries_DeclaredKindWithNoRecords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	meta := controlMeta{id: uuid.New(), queries: []evidenceQueryManifest{
		{Language: "jsonpath", Expression: queryMFA, EvidenceKind: "jira.ticket_evidence.v1"},
	}}
	got, err := (&Engine{}).evalQueries(context.Background(), nil, meta, mixedKindRecords(now))
	if err != nil {
		t.Fatal(err)
	}
	if got != ResultInconclusive {
		t.Fatalf("result = %q, want inconclusive", got)
	}
}

func TestParseEvidenceQueries_ReadsEvidenceKind(t *testing.T) {
	t.Parallel()
	qs, err := parseEvidenceQueries([]byte(`[{"id":"a","language":"rego","expression":"x","evidence_kind":"okta.mfa_policy.v1"},{"id":"b","language":"sql","expression":"y"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 || qs[0].EvidenceKind != kindMFA || qs[1].EvidenceKind != "" {
		t.Fatalf("parsed = %+v", qs)
	}
}

// The fixture path applies the same filter, and a fixture record that omits
// evidence_kind stands for a record of whatever kind each query reads.
func TestEvaluateFixture_FiltersByEvidenceKind(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	queries := []FixtureQuery{
		{Language: "jsonpath", Expression: queryMFA, EvidenceKind: kindMFA},
		{Language: "jsonpath", Expression: queryPosture, EvidenceKind: kindPosture},
	}
	mixed := []FixtureRecord{
		{Result: ResultPass, ObservedAt: now, EvidenceKind: kindMFA, Payload: []byte(`{"applies_to_everyone": true}`)},
		{Result: ResultPass, ObservedAt: now, EvidenceKind: kindPosture, Payload: []byte(`{"firewall_enabled": true}`)},
	}
	res, err := EvaluateFixture(context.Background(), "monthly", queries, mixed, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != ResultPass {
		t.Fatalf("mixed kinds: result = %q, want pass", res.Result)
	}

	unkinded := []FixtureRecord{{Result: ResultPass, ObservedAt: now, Payload: []byte(`{"applies_to_everyone": true, "firewall_enabled": true}`)}}
	res, err = EvaluateFixture(context.Background(), "monthly", queries, unkinded, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != ResultPass {
		t.Fatalf("unkinded fixture record: result = %q, want pass (it matches both queries)", res.Result)
	}
}
