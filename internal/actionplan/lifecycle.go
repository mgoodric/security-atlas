package actionplan

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the action-plan state machine as a declared transition
// table (#2146), for the export. AllowedTransition stays the check the
// store runs; TestLifecycle_MatchesAllowedTransition proves the two agree
// on every pair, and lifecycle_integration_test.go proves the table and
// action_plans_status_transition_guard agree against a real Postgres.
// Every move but verify is a status change through PATCH
// /v1/action-plans/{id}; verify has its own route (#2108).
var Lifecycle = lifecycle.Table{
	Name:   "action_plan",
	Title:  "Action plan",
	Entity: "action_plans",
	Column: "status",
	States: []string{StatusDraft, StatusInProgress, StatusBlocked, StatusCompleted, StatusVerified},
	Initial: []lifecycle.Entry{{
		State:  StatusDraft,
		Action: "create",
		Routes: []string{"POST /v1/action-plans"},
		Roles:  planRoles,
	}},
	Terminal: []string{StatusVerified},
	Transitions: []lifecycle.Transition{
		planMove(StatusDraft, StatusInProgress, "start", ""),
		planMove(StatusInProgress, StatusBlocked, "block", ""),
		planMove(StatusInProgress, StatusCompleted, "complete", ""),
		planMove(StatusBlocked, StatusInProgress, "resume", ""),
		planMove(StatusBlocked, StatusCompleted, "complete", ""),
		{
			From: StatusCompleted, To: StatusVerified, Action: "verify",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"POST /v1/action-plans/{id}/verify"},
			Roles:            planRoles,
			RequiresApprover: true,
			PeopleOnly:       true,
			SecondPerson:     true,
			Note:             "Only for a plan with no linked controls, and never by the plan's owner, whatever the self-approval setting.",
		},
		planMove(StatusCompleted, StatusInProgress, "reopen", "Reopened when verification fails."),
	},
	Trigger: &lifecycle.Trigger{
		Name: "action_plans_status_transition_guard",
		From: []string{StatusDraft, StatusInProgress, StatusBlocked, StatusCompleted, StatusVerified},
	},
}

// planRoles are the roles the authorization bundle admits on the
// action-plan write routes. The handler's own guard is looser
// (hasProgramWrite), but no role other than admin lists "action-plans" as
// writable, so the bundle admits admin alone.
var planRoles = []string{"admin"}

func planMove(from, to, action, note string) lifecycle.Transition {
	return lifecycle.Transition{
		From: from, To: to, Action: action,
		Actor:  lifecycle.ActorPerson,
		Routes: []string{"PATCH /v1/action-plans/{id}"},
		Roles:  planRoles,
		Note:   note,
	}
}
