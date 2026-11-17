package notify

import (
	"context"

	"github.com/google/uuid"
)

// Module switches and module access in the digest (#2175). A digest
// counts a notification kind only when the module (or feature) that owns
// it is switched on for the tenant and the recipient can work in that
// module, so a switched-off module, or one outside the recipient's
// access, sends nothing. The notifications themselves are kept: switching
// the module back on, or widening access, counts them again.

// kindModule maps a notification `type` to the registry key
// (featureflag.Modules) of the optional module or feature that owns it. A
// kind absent from this map belongs to a core module and is always
// counted: audit_note.reply, control.drift, evidence.staleness,
// audit_period_assignment, access_review_due, security_training.overdue
// and backup.failure.
var kindModule = map[string]string{
	"policy_ack_due":                         "policy_acknowledgments",
	"risk_review_overdue":                    "risk",
	"decision.overdue":                       "decision_log",
	"personnel_security.offboarding_overdue": "personnel_security",
}

// Reacher answers whether a recipient may be told about work in a module
// or feature: it is switched on for the tenant and the recipient can work
// in its module. moduleaccess.Views satisfies it.
type Reacher interface {
	Reaches(ctx context.Context, tenantID uuid.UUID, userID, key string) (bool, error)
}

// HiddenKinds returns the notification kinds a recipient's digest leaves
// out. A nil Reacher hides nothing. An error is returned as is: the
// caller sends no digest rather than one that may name a hidden module.
func HiddenKinds(ctx context.Context, r Reacher, tenantID uuid.UUID, userID string) (map[string]bool, error) {
	hidden := map[string]bool{}
	if r == nil {
		return hidden, nil
	}
	for kind, key := range kindModule {
		ok, err := r.Reaches(ctx, tenantID, userID, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			hidden[kind] = true
		}
	}
	return hidden, nil
}

// DropKinds removes hidden kinds from a digest's counts and returns the
// counts left and their total.
func DropKinds(counts map[string]int, hidden map[string]bool) (map[string]int, int) {
	out := make(map[string]int, len(counts))
	total := 0
	for kind, n := range counts {
		if n <= 0 || hidden[kind] {
			continue
		}
		out[kind] = n
		total += n
	}
	return out, total
}
