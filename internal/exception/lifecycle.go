package exception

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the exception lifecycle's declared transition table
// (#2146). It describes the moves Store and Expirer make; the guarded
// UPDATEs in internal/db/queries/exceptions.sql enforce them, and
// lifecycle_integration_test.go proves the two agree pair by pair. No
// trigger guards exceptions.status.
var Lifecycle = lifecycle.Table{
	Name:   "exception",
	Title:  "Exception",
	Entity: "exceptions",
	Column: "status",
	States: []string{StateRequested, StateApproved, StateDenied, StateActive, StateExpired, StateRevoked},
	Initial: []lifecycle.Entry{{
		State:  StateRequested,
		Action: "request",
		Routes: []string{"POST /v1/exceptions"},
		Roles:  []string{"admin", "grc_engineer"},
	}},
	Terminal: []string{StateDenied, StateExpired, StateRevoked},
	Transitions: []lifecycle.Transition{
		{
			From: StateRequested, To: StateApproved, Action: "approve",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/exceptions/{id}/approve"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true, PeopleOnly: true,
			SecondPerson: true, SelfApprovalOptIn: true,
		},
		{
			From: StateRequested, To: StateDenied, Action: "deny",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/exceptions/{id}/deny"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true, SecondPerson: true,
			Note: "Allow self-approval does not extend to denial: the requester never denies their own exception.",
		},
		{
			From: StateApproved, To: StateActive, Action: "activate",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/exceptions/{id}/activate"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true,
		},
		{
			From: StateActive, To: StateExpired, Action: "expire",
			Actor: lifecycle.ActorSystem,
			Note:  "The expiry sweep (daily by default) expires an active exception once its expires_at has passed, then re-evaluates its control (#2269).",
		},
		{
			From: StateActive, To: StateRevoked, Action: "revoke",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/exceptions/{id}/revoke"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true,
			Note:             "An approver ends an active exception before it expires, with a reason (#2113). The control is re-evaluated as soon as the revocation commits (#2269).",
		},
	},
}
