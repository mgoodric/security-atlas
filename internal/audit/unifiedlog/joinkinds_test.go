// Unit tests for slice 318: in-package coverage of the joinKinds CSV
// serializer + the Kind constants' ordering contract.
//
// Load-bearing functions + branches covered:
//
//   - joinKinds: empty slice -> empty string (match-all branch in SQL).
//   - joinKinds: single element -> bare value (no leading or trailing
//     comma).
//   - joinKinds: multiple elements -> comma-joined values, declaration
//     order preserved.
//   - AllKinds: declaration order matches the slice-124 / 180 contract
//     (decision, evidence, exception, sample, audit_period,
//     aggregation_rule, feature_flag, me, walkthrough). A reorder is a
//     wire-shape change that must NOT happen without a slice update.
//
// All branches are pure-Go. The DB-bound Query happy paths are in
// integration_test.go (build tag: integration).

package unifiedlog

import (
	"reflect"
	"testing"
)

func TestJoinKinds_Empty(t *testing.T) {
	t.Parallel()
	if got := joinKinds(nil); got != "" {
		t.Errorf("joinKinds(nil) = %q; want %q", got, "")
	}
	if got := joinKinds([]Kind{}); got != "" {
		t.Errorf("joinKinds([]) = %q; want %q", got, "")
	}
}

func TestJoinKinds_Single(t *testing.T) {
	t.Parallel()
	if got := joinKinds([]Kind{KindSample}); got != "sample" {
		t.Errorf("joinKinds([sample]) = %q; want %q", got, "sample")
	}
}

func TestJoinKinds_Multiple(t *testing.T) {
	t.Parallel()
	got := joinKinds([]Kind{KindMe, KindSample, KindEvidence})
	want := "me,sample,evidence"
	if got != want {
		t.Errorf("joinKinds = %q; want %q", got, want)
	}
}

func TestJoinKinds_PreservesAllNine(t *testing.T) {
	t.Parallel()
	got := joinKinds(AllKinds)
	want := "decision,evidence,exception,sample,audit_period,aggregation_rule,feature_flag,me,walkthrough"
	if got != want {
		t.Errorf("joinKinds(AllKinds) = %q; want %q", got, want)
	}
}

func TestAllKinds_DeclarationOrderIsContractual(t *testing.T) {
	t.Parallel()
	// The slice-124 unified-log SQL projects each branch in this order.
	// A reorder here is a wire-shape change: callers (the handler, the
	// frontend cursor-walker) rely on the declared order for stable
	// pagination tiebreaks. If you NEED to add a new kind, append it
	// AT THE END and ship the slice that extends the SQL UNION too.
	want := []Kind{
		KindDecision,
		KindEvidence,
		KindException,
		KindSample,
		KindAuditPeriod,
		KindAggregationRule,
		KindFeatureFlag,
		KindMe,
		KindWalkthrough,
	}
	if !reflect.DeepEqual(AllKinds, want) {
		t.Errorf("AllKinds = %v; want %v", AllKinds, want)
	}
}
