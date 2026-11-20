package control_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/eval"
)

const segmentationBundleID = "network_segmentation_default_deny"

// #2648: #2396 moved ISO A.8.22 and PCI 1.4.1 to SCF NET-06, and no stock
// control was anchored there, so a stock install reported both uncovered.
// This walks the UCF path the coverage queries take (stock control ->
// scf_anchor_id -> crosswalk edge -> requirement) over the bundled
// crosswalks and the controls/soc2/ bundles the bootstrap uploads.
func TestStockControls_CoverSegmentationRequirements(t *testing.T) {
	anchoredBy := map[string][]string{}
	forEachSOC2Bundle(t, func(_ string, b *control.Bundle) {
		anchoredBy[b.Manifest.SCFAnchorID] = append(anchoredBy[b.Manifest.SCFAnchorID], b.Manifest.BundleID)
	})

	cases := []struct {
		crosswalk   string
		requirement string
	}{
		{crosswalk: "iso27001-2022.yaml", requirement: "A.8.22"},
		{crosswalk: "pci-dss-4.0.yaml", requirement: "1.4.1"},
	}
	for _, tc := range cases {
		cw, err := soc2import.Load(filepath.Join(repoRoot(t), "data", "crosswalks", tc.crosswalk))
		if err != nil {
			t.Fatalf("load %s: %v", tc.crosswalk, err)
		}
		var reachedBy []string
		for _, m := range cw.Mappings {
			if m.RequirementCode == tc.requirement {
				reachedBy = append(reachedBy, anchoredBy[m.SCFAnchor]...)
			}
		}
		if !slices.Contains(reachedBy, segmentationBundleID) {
			t.Errorf("%s %s: reached by stock controls %v, want %s among them",
				tc.crosswalk, tc.requirement, reachedBy, segmentationBundleID)
		}
	}
}

// The segmentation control decides on k8s.networkpolicy_coverage.v1 records,
// one per namespace, each carrying the connector's default-deny verdict. One
// failing namespace fails the control.
func TestSegmentationControl_EvaluatesNamespaceCoverage(t *testing.T) {
	b, err := control.ParseDirectory(filepath.Join(repoRoot(t), "controls", "soc2", segmentationBundleID))
	if err != nil {
		t.Fatalf("parse %s: %v", segmentationBundleID, err)
	}
	if b.Manifest.SCFAnchorID != "NET-06" {
		t.Errorf("scf_anchor_id = %q, want NET-06", b.Manifest.SCFAnchorID)
	}
	var queries []eval.FixtureQuery
	for _, q := range b.Manifest.EvidenceQueries {
		if q.EvidenceKind != "k8s.networkpolicy_coverage.v1" {
			t.Errorf("query %s reads %s, want k8s.networkpolicy_coverage.v1", q.ID, q.EvidenceKind)
		}
		queries = append(queries, eval.FixtureQuery{Language: q.Language, Expression: q.Expression})
	}
	if len(queries) == 0 {
		t.Fatal("no evidence query")
	}

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ns := func(result string) eval.FixtureRecord { return eval.FixtureRecord{Result: result, ObservedAt: now} }
	cases := []struct {
		name    string
		records []eval.FixtureRecord
		want    string
	}{
		{name: "every namespace default-deny", records: []eval.FixtureRecord{ns(eval.ResultPass), ns(eval.ResultPass)}, want: eval.ResultPass},
		{name: "one namespace allow-all", records: []eval.FixtureRecord{ns(eval.ResultPass), ns(eval.ResultFail)}, want: eval.ResultFail},
		{name: "one namespace read errored", records: []eval.FixtureRecord{ns(eval.ResultPass), ns(eval.ResultInconclusive)}, want: eval.ResultPass},
		{name: "only errored reads", records: []eval.FixtureRecord{ns(eval.ResultInconclusive)}, want: eval.ResultInconclusive},
		{name: "no connector run yet", records: nil, want: eval.ResultInconclusive},
	}
	for _, tc := range cases {
		got, err := eval.EvaluateFixture(context.Background(), b.Manifest.FreshnessClass, queries, tc.records, now, nil)
		if err != nil {
			t.Errorf("%s: evaluate: %v", tc.name, err)
			continue
		}
		if got.Result != tc.want {
			t.Errorf("%s: result = %q, want %q", tc.name, got.Result, tc.want)
		}
	}
}
