package risk

import (
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/lifecycle"
)

// Treatment lifecycle states (#2147). A risk's treatment and its
// acceptance status are two columns, but only accept carries an acceptance
// status (risks_acceptance_status_iff_accept), so together they hold six
// states. TreatmentState names a risk's.
const (
	TreatmentStateUntreated      = "untreated"
	TreatmentStateMitigate       = "mitigate"
	TreatmentStateTransfer       = "transfer"
	TreatmentStateAvoid          = "avoid"
	TreatmentStateAcceptProposed = "accept_proposed"
	TreatmentStateAcceptApproved = "accept_approved"
)

// TreatmentState returns the treatment lifecycle state for a treatment and
// acceptance status: the treatment itself, or accept_<status> for accept.
// A combination the lifecycle does not name comes back as
// "<treatment>/<status>", which no table state matches.
func TreatmentState(treatment dbx.RiskTreatment, acceptance dbx.RiskAcceptanceStatus) string {
	if treatment == dbx.RiskTreatmentAccept {
		switch acceptance {
		case dbx.RiskAcceptanceStatusProposed:
			return TreatmentStateAcceptProposed
		case dbx.RiskAcceptanceStatusApproved:
			return TreatmentStateAcceptApproved
		}
	} else if acceptance == "" {
		return string(treatment)
	}
	return string(treatment) + "/" + string(acceptance)
}

// TreatmentLifecycle is the risk treatment and acceptance lifecycle as a
// declared transition table (#2147). It describes the moves in
// transitions.go. No trigger guards the treatment columns: the transition
// functions refuse every other move, and lifecycle_integration_test.go
// proves the two agree pair by pair. A closed risk takes none of these
// moves until it is reopened (RiskStatusLifecycle).
var TreatmentLifecycle = lifecycle.Table{
	Name:        "risk_treatment",
	Title:       "Risk treatment and acceptance",
	Entity:      "risks",
	Column:      "treatment, acceptance_status",
	Enforcement: lifecycle.EnforcedByStore,
	States: []string{
		TreatmentStateUntreated, TreatmentStateMitigate, TreatmentStateTransfer, TreatmentStateAvoid,
		TreatmentStateAcceptProposed, TreatmentStateAcceptApproved,
	},
	Initial: []lifecycle.Entry{
		riskCreate(TreatmentStateUntreated),
		riskCreate(TreatmentStateMitigate),
		riskCreate(TreatmentStateTransfer),
		riskCreate(TreatmentStateAvoid),
		riskCreate(TreatmentStateAcceptProposed),
	},
	Terminal:    []string{},
	Transitions: treatmentTransitions(),
}

// treatmentTargets are the treatments a person sets directly, with the
// move's action. Every one is reachable from every other and from neither
// acceptance state.
var treatmentTargets = []struct{ state, action, note string }{
	{TreatmentStateUntreated, "clear_treatment", ""},
	{TreatmentStateMitigate, "mitigate", "Needs a linked control or action plan."},
	{TreatmentStateTransfer, "transfer", "Needs an instrument reference (an insurance policy or contract)."},
	{TreatmentStateAvoid, "avoid", ""},
}

func treatmentTransitions() []lifecycle.Transition {
	var out []lifecycle.Transition
	for _, from := range treatmentTargets {
		for _, to := range treatmentTargets {
			if from.state == to.state {
				continue
			}
			out = append(out, lifecycle.Transition{
				From: from.state, To: to.state, Action: to.action,
				Actor:  lifecycle.ActorPerson,
				Routes: []string{"PATCH /v1/risks/{id}/treatment"},
				Roles:  riskWriteRoles,
				Note:   to.note,
			})
		}
	}
	for _, from := range treatmentTargets {
		out = append(out, lifecycle.Transition{
			From: from.state, To: TreatmentStateAcceptProposed, Action: "propose_acceptance",
			Actor:      lifecycle.ActorPerson,
			Routes:     []string{"PATCH /v1/risks/{id}/acceptance/propose"},
			Roles:      riskWriteRoles,
			PeopleOnly: true,
			Note:       "Needs a justification and a proposed expiry after today. A machine credential never proposes (#2623).",
		})
	}
	return append(out,
		lifecycle.Transition{
			From: TreatmentStateAcceptProposed, To: TreatmentStateAcceptApproved, Action: "approve_acceptance",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/risks/{id}/acceptance/approve"},
			Roles:            riskWriteRoles,
			RequiresApprover: true, PeopleOnly: true,
			SecondPerson: true, SelfApprovalOptIn: true,
			Note: "The approver is recorded as the accepter, with the accepted-until date.",
		},
		lifecycle.Transition{
			From: TreatmentStateAcceptProposed, To: TreatmentStateUntreated, Action: "reject_acceptance",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/risks/{id}/acceptance/reject"},
			Roles:            riskWriteRoles,
			RequiresApprover: true,
			Note:             "A rejected acceptance leaves the risk untreated until its owner decides a treatment.",
		},
		lifecycle.Transition{
			From: TreatmentStateAcceptApproved, To: TreatmentStateUntreated, Action: "revoke_acceptance",
			Actor:            lifecycle.ActorPerson,
			Routes:           []string{"PATCH /v1/risks/{id}/acceptance/revoke"},
			Roles:            riskWriteRoles,
			RequiresApprover: true,
			Note:             "Needs a reason. A revoked acceptance leaves the risk untreated.",
		},
	)
}

// StatusLifecycle is the risk open/closed lifecycle (#2107) as a declared
// transition table (#2147). It describes Close and Reopen in lifecycle.go.
// risks_closure_consistent keeps the closure fields in step with status
// but does not refuse a move, so the store is the only guard;
// lifecycle_integration_test.go proves the two agree.
var StatusLifecycle = lifecycle.Table{
	Name:        "risk_status",
	Title:       "Risk open and closed",
	Entity:      "risks",
	Column:      "status",
	Enforcement: lifecycle.EnforcedByStore,
	States:      []string{string(dbx.RiskStatusOpen), string(dbx.RiskStatusClosed)},
	Initial:     []lifecycle.Entry{riskCreate(string(dbx.RiskStatusOpen))},
	Terminal:    []string{},
	Transitions: []lifecycle.Transition{
		{
			From: string(dbx.RiskStatusOpen), To: string(dbx.RiskStatusClosed), Action: "close",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/risks/{id}/close"},
			Roles:  riskWriteRoles,
			Note:   "Needs a reason: not applicable, merged, or realised and handled. A closed risk leaves the register, heatmaps, rollups and board pack, and keeps its history.",
		},
		{
			From: string(dbx.RiskStatusClosed), To: string(dbx.RiskStatusOpen), Action: "reopen",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"PATCH /v1/risks/{id}/reopen"},
			Roles:  riskWriteRoles,
			Note:   "The closure is cleared.",
		},
	},
}

// riskWriteRoles are the roles the authorization bundle admits on the risk
// write routes.
var riskWriteRoles = []string{"admin", "grc_engineer"}

func riskCreate(state string) lifecycle.Entry {
	return lifecycle.Entry{
		State:  state,
		Action: "create",
		Routes: []string{"POST /v1/risks"},
		Roles:  riskWriteRoles,
	}
}
