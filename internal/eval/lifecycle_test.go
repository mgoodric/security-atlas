package eval

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control/attestation"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// TestLifecycle_StatesAreEveryResult: the control-state table names every
// value of evidence_result, the enum control_evaluations.result uses. A
// result added to the enum fails here until the table draws it (#2147).
func TestLifecycle_StatesAreEveryResult(t *testing.T) {
	t.Parallel()
	if err := Lifecycle.Validate(); err != nil {
		t.Fatal(err)
	}
	var enum []string
	for _, v := range dbx.AllEvidenceResultValues() {
		enum = append(enum, string(v))
	}
	got := slices.Clone(Lifecycle.States)
	slices.Sort(got)
	slices.Sort(enum)
	if !slices.Equal(got, enum) {
		t.Fatalf("table states %v, evidence_result %v", got, enum)
	}
	for _, s := range Lifecycle.States {
		if _, ok := resultCauses[s]; !ok {
			t.Errorf("state %s has no cause", s)
		}
	}
}

// stateScenario is one cell's inputs over time: its ledger, the exceptions
// on its control, and the evaluation clock.
type stateScenario struct {
	records    []allRecord
	exceptions []activeException
	now        time.Time
	seq        int
}

func (s *stateScenario) add(result, kind string) {
	s.seq++
	s.records = append(s.records, allRecord{
		observedAt: s.now, ingestedAt: s.now, result: result, kind: kind,
		id: fmt.Sprintf("r%03d", s.seq),
	})
}

// endExceptions ends every exception at the current clock.
func (s *stateScenario) endExceptions() {
	for i := range s.exceptions {
		if s.exceptions[i].expiresAt.After(s.now) {
			s.exceptions[i].expiresAt = s.now
		}
	}
}

// stateSteps are the inputs each table action stands for. Every step first
// moves the clock on an hour, so a new record is the latest. The evidence
// steps also end any exception in force: an exception overrides evidence,
// so the evidence's result shows only once it has ended.
var stateSteps = map[string]func(s *stateScenario){
	"evidence_passes": func(s *stateScenario) {
		s.endExceptions()
		s.add(ResultPass, attestation.Kind)
	},
	"evidence_fails": func(s *stateScenario) {
		s.endExceptions()
		s.add(ResultFail, attestation.Kind)
	},
	"evidence_not_applicable": func(s *stateScenario) {
		s.endExceptions()
		s.now = s.now.Add(365 * 24 * time.Hour)
		s.add(ResultNA, "aws.config.rule.v1")
	},
	"no_current_evidence": func(s *stateScenario) {
		s.endExceptions()
		s.now = s.now.Add(365 * 24 * time.Hour)
	},
	"exception_in_force": func(s *stateScenario) {
		from := s.now
		s.exceptions = append(s.exceptions, activeException{
			predicate: []byte(`{}`), effectiveFrom: &from, expiresAt: s.now.Add(365 * 24 * time.Hour),
		})
	},
}

// evaluate runs the engine's own row computation over the scenario: the
// freshness window, latest attestation wins, the result rollup, the
// no-evidence rule and the exception overlay.
func (s *stateScenario) evaluate(t *testing.T) string {
	t.Helper()
	meta := controlMeta{id: uuid.New(), freshnessClass: "monthly"}
	row, err := (&Engine{}).computeRow(context.Background(), nil, meta, s.records, s.exceptions, nil, uuid.New(), "test", s.now)
	if err != nil {
		t.Fatalf("computeRow: %v", err)
	}
	return row.result
}

func (s *stateScenario) step(t *testing.T, action string) string {
	t.Helper()
	s.now = s.now.Add(time.Hour)
	stateSteps[action](s)
	return s.evaluate(t)
}

// TestLifecycle_EveryPairFromComputeRow: for every result and every input
// the table names, the engine's evaluation lands where the table says: in
// the move's to-state, or where it was when the table lists no move. The
// pairs reached are exactly the table's (#2147).
//
// fail -> pass and pass -> fail are both attestations, so they hold only
// because the latest attestation wins (#2111); the excepted moves hold
// only because the exception overlay is applied last (#2112).
func TestLifecycle_EveryPairFromComputeRow(t *testing.T) {
	t.Parallel()
	actions := Lifecycle.Actions()
	for _, a := range actions {
		if stateSteps[a] == nil {
			t.Fatalf("no step for action %s", a)
		}
	}
	if len(stateSteps) != len(actions) {
		t.Fatalf("steps %d, table actions %d", len(stateSteps), len(actions))
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	// seed reaches a state through the action the table names for it.
	seed := func(t *testing.T, state string) *stateScenario {
		s := &stateScenario{now: start}
		if state != ResultInconclusive {
			if got := s.step(t, resultCauses[state].action); got != state {
				t.Fatalf("seed %s: got %s", state, got)
			}
		} else if got := s.evaluate(t); got != state {
			t.Fatalf("seed %s: got %s", state, got)
		}
		return s
	}

	reached := map[[2]string]bool{}
	for _, from := range Lifecycle.States {
		for _, action := range actions {
			got := seed(t, from).step(t, action)
			want := from
			if tr, ok := Lifecycle.Move(from, action); ok {
				want = tr.To
			}
			if got != want {
				t.Errorf("%s out of %s: engine says %s, table says %s", action, from, got, want)
			}
			if got != from {
				reached[[2]string{from, got}] = true
			}
		}
	}
	for _, from := range Lifecycle.States {
		for _, to := range Lifecycle.States {
			if from != to && Lifecycle.Allows(from, to) != reached[[2]string{from, to}] {
				t.Errorf("%s -> %s: table allows=%v, an input reached it=%v", from, to, Lifecycle.Allows(from, to), reached[[2]string{from, to}])
			}
		}
	}
}

// TestLifecycle_ExceptedOverridesFailingEvidence: a failing attestation
// under an exception in force is still excepted, and the result returns to
// fail once the exception ends.
func TestLifecycle_ExceptedOverridesFailingEvidence(t *testing.T) {
	t.Parallel()
	s := &stateScenario{now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	s.step(t, "exception_in_force")
	s.add(ResultFail, attestation.Kind)
	if got := s.evaluate(t); got != ResultExcepted {
		t.Fatalf("fail under an exception: got %s", got)
	}
	s.now = s.now.Add(time.Hour)
	s.endExceptions()
	if got := s.evaluate(t); got != ResultFail {
		t.Fatalf("after the exception ends: got %s", got)
	}
}
