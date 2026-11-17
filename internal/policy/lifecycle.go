package policy

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the policy lifecycle's declared transition table (#2146).
// It describes the moves Store makes; the guarded UPDATEs in
// internal/db/queries/policies.sql enforce them, and
// lifecycle_integration_test.go proves the two agree pair by pair. No
// trigger guards policies.status.
//
// Discarding a draft (DELETE /v1/policies/{id}) deletes the row rather
// than moving it, so it is not a transition. A new version of a published
// policy is a new row, created in draft with predecessor_id set.
var Lifecycle = lifecycle.Table{
	Name:        "policy",
	Title:       "Policy",
	Entity:      "policies",
	Column:      "status",
	Enforcement: lifecycle.EnforcedByStore,
	States:      []string{StateDraft, StateUnderReview, StateApproved, StatePublished, StateSuperseded},
	Initial: []lifecycle.Entry{
		{
			State:  StateDraft,
			Action: "create",
			Routes: []string{"POST /v1/policies"},
			Roles:  []string{"admin", "grc_engineer"},
		},
		{
			State:  StatePublished,
			Action: "import",
			Routes: []string{"POST /v1/admin/policies/import"},
			Roles:  []string{"admin"},
		},
	},
	Terminal: []string{StateSuperseded},
	Transitions: []lifecycle.Transition{
		{
			From: StateDraft, To: StateUnderReview, Action: "submit",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/policies/{id}/submit"},
			Roles:  []string{"admin", "grc_engineer"},
			Note:   "A policy under review whose submitter was recorded as a session id (before #1807) may be submitted again, so approval has a person to compare.",
		},
		{
			From: StateUnderReview, To: StateApproved, Action: "approve",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/policies/{id}/approve"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true, PeopleOnly: true,
			SecondPerson: true, SelfApprovalOptIn: true,
		},
		{
			From: StateApproved, To: StatePublished, Action: "publish",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"POST /v1/policies/{id}/publish"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true,
		},
		{
			From: StatePublished, To: StateSuperseded, Action: "supersede",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"POST /v1/policies/{id}/publish"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true,
			Note:             "Publishing a policy's next version supersedes it, in the same transaction.",
		},
	},
}
