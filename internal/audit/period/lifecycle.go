package period

import "github.com/mgoodric/security-atlas/internal/lifecycle"

// Lifecycle is the audit-period lifecycle's declared transition table
// (#2146). FreezeAuditPeriod's `WHERE status = 'open'` guard enforces it,
// and lifecycle_integration_test.go proves the two agree. No trigger
// guards audit_periods.status.
var Lifecycle = lifecycle.Table{
	Name:   "audit_period",
	Title:  "Audit period",
	Entity: "audit_periods",
	Column: "status",
	States: []string{string(StatusOpen), string(StatusFrozen)},
	Initial: []lifecycle.Entry{{
		State:  string(StatusOpen),
		Action: "create",
		Routes: []string{"POST /v1/audit-periods"},
		Roles:  []string{"admin"},
	}},
	Terminal: []string{string(StatusFrozen)},
	Transitions: []lifecycle.Transition{
		{
			From: string(StatusOpen), To: string(StatusFrozen), Action: "freeze",
			Actor:  lifecycle.ActorPerson,
			Routes: []string{"POST /v1/audit-periods/{id}/freeze"},
			Roles:  []string{"admin"},
			Note:   "Freezing is final: sample populations then draw only on evidence observed at or before frozen_at.",
		},
	},
}
