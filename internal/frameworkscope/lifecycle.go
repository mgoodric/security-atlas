package frameworkscope

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the framework-scope lifecycle's declared transition table
// (#2146). It describes the moves Store and the database make. The
// guarded UPDATEs in internal/db/queries/framework_scopes.sql enforce the
// forward moves; two triggers make the return to draft
// (framework_scopes_bounce_on_predicate_change and
// framework_scope_applicability_reset_approval); and
// framework_scopes_predicate_frozen refuses every move out of an activated
// or superseded revision except activated -> superseded (#1965).
// lifecycle_integration_test.go proves the table agrees with the store
// and with that trigger, pair by pair.
var Lifecycle = lifecycle.Table{
	Name:   "framework_scope",
	Title:  "Framework scope",
	Entity: "framework_scopes",
	Column: "state",
	States: []string{StateDraft, StateReview, StateApproved, StateActivated, StateSuperseded},
	Initial: []lifecycle.Entry{{
		State:  StateDraft,
		Action: "create",
		Routes: []string{"POST /v1/framework-scopes"},
		Roles:  []string{"admin", "grc_engineer"},
	}},
	Terminal: []string{StateSuperseded},
	Transitions: []lifecycle.Transition{
		{
			From: StateDraft, To: StateReview, Action: "submit",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/framework-scopes/{id}/submit"},
			Roles:  []string{"admin", "grc_engineer"},
			Note:   "A scope in review with no recorded submitter, or one recorded as a session id, may be submitted again, so approval has a person to compare.",
		},
		{
			From: StateReview, To: StateApproved, Action: "approve",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/framework-scopes/{id}/approve"},
			Roles:            []string{"admin", "grc_engineer"},
			RequiresApprover: true, PeopleOnly: true,
			SecondPerson: true, SelfApprovalOptIn: true,
		},
		{
			From: StateReview, To: StateDraft, Action: "edit",
			Actor:  lifecycle.ActorPerson,
			Routes: editRoutes,
			Roles:  []string{"admin", "grc_engineer"},
			Note:   editNote,
		},
		{
			From: StateApproved, To: StateDraft, Action: "edit",
			Actor:  lifecycle.ActorPerson,
			Routes: editRoutes,
			Roles:  []string{"admin", "grc_engineer"},
			Note:   editNote,
		},
		{
			From: StateApproved, To: StateActivated, Action: "activate",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/framework-scopes/{id}/activate"},
			Roles:  []string{"admin", "grc_engineer"},
		},
		{
			From: StateActivated, To: StateSuperseded, Action: "supersede",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/framework-scopes/{id}/activate"},
			Roles:  []string{"admin", "grc_engineer"},
			Note:   "Activating another revision for the same framework version supersedes the activated one, in the same transaction.",
		},
	},
	Trigger: &lifecycle.Trigger{
		Name: "framework_scopes_predicate_frozen",
		From: []string{StateActivated, StateSuperseded},
	},
}

// editRoutes are the routes that change a scope's predicate or its
// applicability decisions.
var editRoutes = []string{
	"PATCH /v1/framework-scopes/{id}",
	"PUT /v1/framework-scopes/{id}/applicability/{requirement_id}",
	"POST /v1/framework-scopes/{id}/applicability/bulk",
	"POST /v1/framework-scopes/{id}/applicability/import",
	"POST /v1/framework-scopes/{id}/applicability:carry-over",
}

const editNote = "Changing the predicate or an applicability decision returns the scope to draft and clears its approval (database triggers); it must be submitted and approved again."
