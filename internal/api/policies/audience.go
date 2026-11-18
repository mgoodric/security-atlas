package policies

import (
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/policy"
)

// AudienceValues is the set a policy's acknowledgment_required_roles is
// chosen from (#2524): the five canonical roles, which a member holds as a
// live user_roles row, plus policy.AudienceEveryone. A free-text value such
// as "employee" never matched a user's session, because a session's roles
// are the canonical enum.
func AudienceValues() []string {
	out := make([]string, 0, len(authz.CanonicalRoles())+1)
	for _, r := range authz.CanonicalRoles() {
		out = append(out, string(r))
	}
	return append(out, policy.AudienceEveryone)
}

// validateAudience refuses an acknowledgment_required_roles value outside
// AudienceValues, unless it is in keep: the values the draft being edited
// already carries. A draft written before #2524 with a free-text audience
// stays editable without first changing that audience; a new policy, or a
// new value on a draft, must name a real role or everyone.
func validateAudience(roles, keep []string) error {
	known := map[string]bool{}
	for _, v := range AudienceValues() {
		known[v] = true
	}
	for _, v := range keep {
		known[v] = true
	}
	for _, r := range roles {
		if !known[r] {
			return fmt.Errorf("acknowledgment_required_roles: %q is not an audience; choose from %s", r, strings.Join(AudienceValues(), ", "))
		}
	}
	return nil
}
