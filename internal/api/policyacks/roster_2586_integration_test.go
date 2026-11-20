//go:build integration

package policyacks_test

// #2586 — the ack-rate roster is the live user_roles set, the same set the
// authz engine resolves and the acknowledge gate admits. For each of an
// "everyone" policy and a role-targeted one, every candidate tries to
// acknowledge, and the rate's denominator must equal the number of them
// whose ack was accepted.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

func TestAcknowledgmentRate2586_RosterIsWhoMayAcknowledge(t *testing.T) {
	s := setup(t)
	everyone := createAndPublishPolicy(t, s, "2586 Everyone", "1", []string{"everyone"}, s.controlID)
	viewers := createAndPublishPolicy(t, s, "2586 Viewers", "1", []string{"viewer"}, s.controlID)
	tenantUUID := uuid.MustParse(s.tenant)

	type candidate struct {
		name   string
		bearer string
		// mayAck is whether this candidate is in each policy's audience.
		everyone, viewers bool
	}
	jwtFor := func(user uuid.UUID, roles []string) string {
		claims := testjwt.OwnerFor(tenantUUID, roles)
		claims.Subject = user.String()
		return s.apiServer.IssueTestJWT(t, claims)
	}
	var cands []candidate

	// A JWT-only viewer: a live role, no api_keys row. The old api_keys
	// roster missed them although they could acknowledge.
	jwtOnly := seedUser(t, s.admin, s.tenant, "jwt-only-2586@test")
	seedLiveRoles(t, s.admin, s.tenant, jwtOnly, []string{"viewer"})
	cands = append(cands, candidate{"jwt-only viewer", jwtFor(jwtOnly, []string{"viewer"}), true, true})

	// A JWT-only auditor: on the everyone roster, not the viewer one.
	auditor := seedUser(t, s.admin, s.tenant, "auditor-2586@test")
	seedLiveRoles(t, s.admin, s.tenant, auditor, []string{"auditor"})
	cands = append(cands, candidate{"jwt-only auditor", jwtFor(auditor, []string{"auditor"}), true, false})

	// A revoked viewer whose key was not revoked: an unrevoked api_keys row
	// labelled viewer, no user_roles row. The old roster counted them; they
	// cannot acknowledge, because their session carries no live role.
	revoked := seedUser(t, s.admin, s.tenant, "revoked-2586@test")
	seedAPIKeyForUser(t, s.admin, s.tenant, revoked, false, false, []string{"viewer"})
	cands = append(cands, candidate{"revoked viewer with live key", jwtFor(revoked, []string{"viewer"}), false, false})

	// A disabled user who still holds a viewer row: the authz resolver
	// gives them no roles, so they are on neither roster.
	disabled := seedUser(t, s.admin, s.tenant, "disabled-2586@test")
	seedLiveRoles(t, s.admin, s.tenant, disabled, []string{"viewer"})
	if _, err := s.admin.Exec(t.Context(), `UPDATE users SET status = 'disabled' WHERE id = $1`, disabled); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	cands = append(cands, candidate{"disabled viewer", jwtFor(disabled, []string{"viewer"}), false, false})

	for _, pol := range []struct {
		id   string
		name string
		in   func(candidate) bool
	}{
		{everyone, "everyone", func(c candidate) bool { return c.everyone }},
		{viewers, "viewer", func(c candidate) bool { return c.viewers }},
	} {
		var accepted int64
		for _, c := range cands {
			want := http.StatusForbidden
			if pol.in(c) {
				want = http.StatusCreated
				accepted++
			}
			s.postJSON(t, "/v1/policies/"+pol.id+"/acknowledge", []byte("{}"), c.bearer, want)
		}
		raw := s.getJSON(t, "/v1/policies/"+pol.id+"/acknowledgment-rate", s.adminBear, http.StatusOK)
		var rate struct {
			Numerator   int64 `json:"numerator"`
			Denominator int64 `json:"denominator"`
		}
		if err := json.Unmarshal(raw, &rate); err != nil {
			t.Fatalf("unmarshal rate: %v body=%s", err, raw)
		}
		if rate.Denominator != accepted || rate.Numerator != accepted {
			t.Errorf("%s rate = %d/%d; want %d/%d (the roster is exactly who may acknowledge)",
				pol.name, rate.Numerator, rate.Denominator, accepted, accepted)
		}
	}
}
