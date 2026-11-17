package eval

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the evidence -> evaluation -> control state flow as a
// declared transition table (#2147), simplified: one scope cell, no
// declared evidence queries, freshness folded into "current evidence".
//
// It is derived, not enforced. control_evaluations is append-only: each
// evaluation computes the result afresh from the evidence in the
// freshness window and the exceptions in force (computeRow), and nothing
// compares it with the previous one. So every result can follow every
// other, and each move is named for the input that produces its to-state:
//
//   - pass: the current evidence passes; for attestations only the latest
//     counts, so a later passing attestation supersedes an earlier fail;
//   - fail: any current automated evidence fails, or the latest attestation
//     does;
//   - na: every current record is not applicable;
//   - inconclusive: no evidence is current (none yet, or it aged out of the
//     window), or it is all inconclusive;
//   - excepted: an active exception in force covers the cell, whatever the
//     evidence says. It is never a pass; when the exception ends, the next
//     evaluation is the evidence's result again.
//
// lifecycle_test.go drives computeRow through every pair.
var Lifecycle = lifecycle.Table{
	Name:        "control_state",
	Title:       "Control state",
	Entity:      "control_evaluations",
	Column:      "result",
	Enforcement: lifecycle.EnforcedByDerivation,
	States:      resultStates,
	Initial:     resultEntries(),
	Terminal:    []string{},
	Transitions: resultTransitions(),
}

// resultStates are the evaluation results, in the order the help draws
// them.
var resultStates = []string{ResultInconclusive, ResultPass, ResultFail, ResultNA, ResultExcepted}

// resultCauses names, for each result, the input that produces it.
var resultCauses = map[string]struct{ action, note string }{
	ResultPass: {"evidence_passes",
		"Current evidence passes. Only the latest attestation counts, so a passing attestation supersedes an earlier failing one."},
	ResultFail: {"evidence_fails",
		"Any current automated evidence fails, or the latest attestation fails."},
	ResultNA: {"evidence_not_applicable",
		"Every current evidence record is not applicable."},
	ResultInconclusive: {"no_current_evidence",
		"No evidence is in the freshness window (none yet, or it aged out), or all of it is inconclusive. Absence of evidence is not a failure."},
	ResultExcepted: {"exception_in_force",
		"An active exception covers the scope cell, whatever the evidence says. Never a pass; when the exception ends, the next evaluation is the evidence's result again."},
}

func resultEntries() []lifecycle.Entry {
	out := make([]lifecycle.Entry, 0, len(resultStates))
	for _, s := range resultStates {
		out = append(out, lifecycle.Entry{State: s, Action: "first_evaluation", Roles: []string{}})
	}
	return out
}

func resultTransitions() []lifecycle.Transition {
	var out []lifecycle.Transition
	for _, from := range resultStates {
		for _, to := range resultStates {
			if from == to {
				continue
			}
			c := resultCauses[to]
			out = append(out, lifecycle.Transition{
				From: from, To: to, Action: c.action,
				Actor: lifecycle.ActorSystem,
				Note:  c.note,
			})
		}
	}
	return out
}
