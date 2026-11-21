package eval

import (
	"slices"
	"testing"
)

// #2817: only a kind in UnboundEvidenceKinds widens a control's ledger read,
// and the parameter is never nil.
func TestUnboundKinds(t *testing.T) {
	t.Parallel()
	ack := "policy.acknowledgment.v1"
	cases := []struct {
		name    string
		queries []evidenceQueryManifest
		want    []string
	}{
		{name: "no queries", queries: nil, want: []string{}},
		{name: "undeclared kind", queries: []evidenceQueryManifest{{Language: "rego"}}, want: []string{}},
		{name: "other kind", queries: []evidenceQueryManifest{{EvidenceKind: "aws.iam.mfa_status.v1"}}, want: []string{}},
		{name: "ack kind", queries: []evidenceQueryManifest{{EvidenceKind: ack}}, want: []string{ack}},
		{name: "ack kind twice", queries: []evidenceQueryManifest{
			{EvidenceKind: ack}, {EvidenceKind: "aws.iam.mfa_status.v1"}, {EvidenceKind: ack},
		}, want: []string{ack}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := unboundKinds(tc.queries)
			if got == nil || !slices.Equal(got, tc.want) {
				t.Fatalf("unboundKinds = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The stored evidence_queries JSON carries evidence_kind; parsing keeps it.
func TestParseEvidenceQueriesKeepsEvidenceKind(t *testing.T) {
	t.Parallel()
	qs, err := parseEvidenceQueries([]byte(`[{"id":"q","language":"rego","evidence_kind":"policy.acknowledgment.v1","expression":"x"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0].EvidenceKind != "policy.acknowledgment.v1" {
		t.Fatalf("parsed %#v", qs)
	}
}
