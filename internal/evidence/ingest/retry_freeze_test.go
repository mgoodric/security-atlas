package ingest_test

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

func TestRetryConcurrentFreeze(t *testing.T) {
	t.Parallel()
	other := errors.New("other")
	freeze := ingest.ErrConcurrentFreeze
	cases := []struct {
		name      string
		errs      []error // attempt n returns errs[n-1]
		wantCalls int
		want      error
	}{
		{"success first", []error{nil}, 1, nil},
		{"freeze then success", []error{freeze, nil}, 2, nil},
		{"other error is not retried", []error{other}, 1, other},
		{"freeze every attempt stops at the bound", []error{freeze, freeze, freeze, nil}, ingest.MaxFreezeAttempts, freeze},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			err := ingest.RetryConcurrentFreeze(func(n int) error {
				calls++
				if n != calls {
					t.Errorf("attempt numbered %d, want %d", n, calls)
				}
				return tc.errs[n-1]
			})
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestDecisionRejectedConcurrentFreezeString(t *testing.T) {
	t.Parallel()
	if got := ingest.DecisionRejectedConcurrentFreeze.String(); got != "rejected_concurrent_freeze" {
		t.Fatalf("String() = %q, want rejected_concurrent_freeze", got)
	}
}
