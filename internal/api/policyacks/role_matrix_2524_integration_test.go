//go:build integration

package policyacks_test

// #2524 — any member acknowledges a policy aimed at them, for themselves
// only. viewer, auditor and control_owner each acknowledge a policy aimed at
// their role and one aimed at everyone, see both in their pending list, and
// are refused (403) a policy aimed at another role, an untargeted one, and a
// legacy free-text one. A body naming another user records the ack for the
// caller.

import (
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func TestAcknowledge2524_RoleMatrix(t *testing.T) {
	s := setup(t)
	everyone := createAndPublishPolicy(t, s, "2524 Everyone", "1", []string{"everyone"}, s.controlID)
	untargeted := createAndPublishPolicy(t, s, "2524 Untargeted", "1", []string{}, s.controlID)
	otherRole := createAndPublishPolicy(t, s, "2524 Engineers", "1", []string{"grc_engineer"}, s.controlID)
	legacy := createAndPublishPolicy(t, s, "2524 Legacy", "1", []string{"auditor"}, s.controlID)
	// A pre-#2524 free-text audience, written as an old row would be: the
	// API no longer accepts it.
	if _, err := s.admin.Exec(t.Context(),
		`UPDATE policies SET acknowledgment_required_roles = ARRAY['employee'] WHERE id = $1`, legacy); err != nil {
		t.Fatalf("set legacy audience: %v", err)
	}
	other, _ := s.makeUserAndBearer(t, "other-2524@test", []string{"viewer"}, false)

	for _, role := range []string{"viewer", "auditor", "control_owner"} {
		t.Run(role, func(t *testing.T) {
			targeted := createAndPublishPolicy(t, s, "2524 For "+role, "1", []string{role}, s.controlID)
			user, bearer := s.makeUserAndBearer(t, role+"-2524@test", []string{role}, false)

			raw := s.getJSON(t, "/v1/me/acknowledgments", bearer, http.StatusOK)
			var pending struct {
				Pending []struct {
					PolicyID string `json:"policy_id"`
				} `json:"pending"`
			}
			if err := json.Unmarshal(raw, &pending); err != nil {
				t.Fatalf("unmarshal pending: %v body=%s", err, raw)
			}
			var got []string
			for _, p := range pending.Pending {
				got = append(got, p.PolicyID)
			}
			want := []string{everyone, targeted}
			sort.Strings(got)
			sort.Strings(want)
			if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
				t.Errorf("pending = %v; want %v (everyone + %s only)", got, want, role)
			}

			for _, id := range []string{targeted, everyone} {
				body, _ := json.Marshal(map[string]string{"user_id": other.String()})
				s.postJSON(t, "/v1/policies/"+id+"/acknowledge", body, bearer, http.StatusCreated)
				var ackUser uuid.UUID
				if err := s.admin.QueryRow(t.Context(),
					`SELECT user_id FROM policy_acknowledgments WHERE tenant_id = $1 AND policy_id = $2 ORDER BY acknowledged_at DESC LIMIT 1`,
					s.tenant, id).Scan(&ackUser); err != nil {
					t.Fatalf("read ack: %v", err)
				}
				if ackUser != user {
					t.Errorf("ack of %s recorded for %s; want the caller %s", id, ackUser, user)
				}
			}
			for _, id := range []string{otherRole, untargeted, legacy} {
				s.postJSON(t, "/v1/policies/"+id+"/acknowledge", []byte("{}"), bearer, http.StatusForbidden)
			}
		})
	}

	// The rate of an everyone policy counts every member on the roster:
	// the admin, the other viewer and the three role users; the three
	// acknowledged it.
	raw := s.getJSON(t, "/v1/policies/"+everyone+"/acknowledgment-rate", s.adminBear, http.StatusOK)
	var rate struct {
		Numerator   int64 `json:"numerator"`
		Denominator int64 `json:"denominator"`
	}
	if err := json.Unmarshal(raw, &rate); err != nil {
		t.Fatalf("unmarshal rate: %v body=%s", err, raw)
	}
	if rate.Numerator != 3 || rate.Denominator != 5 {
		t.Errorf("everyone rate = %d/%d; want 3/5", rate.Numerator, rate.Denominator)
	}
}

// TestCreatePolicy2524_AudienceEnum pins the audience enum on the write path:
// a free-text value is refused on create, and a draft keeps a value it
// already carries but cannot gain a new one.
func TestCreatePolicy2524_AudienceEnum(t *testing.T) {
	s := setup(t)
	body := func(roles []string) []byte {
		b, _ := json.Marshal(map[string]any{
			"title":                         "2524 Enum",
			"version":                       "1",
			"body_md":                       "# 2524\n\nBody.",
			"owner_role":                    "tenant_admin",
			"approver_role":                 "security_lead",
			"linked_control_ids":            []string{s.controlID.String()},
			"acknowledgment_required_roles": roles,
		})
		return b
	}
	s.postJSON(t, "/v1/policies", body([]string{"employee"}), s.adminBear, http.StatusBadRequest)
	id := s.postJSON(t, "/v1/policies", body([]string{"viewer", "everyone"}), s.adminBear, http.StatusCreated)

	if _, err := s.admin.Exec(t.Context(),
		`UPDATE policies SET acknowledgment_required_roles = ARRAY['employee'] WHERE id = $1`, id); err != nil {
		t.Fatalf("set legacy audience: %v", err)
	}
	s.patchJSON(t, "/v1/policies/"+id, body([]string{"employee", "auditor"}), s.adminBear, http.StatusOK)
	s.patchJSON(t, "/v1/policies/"+id, body([]string{"employee", "contractor"}), s.adminBear, http.StatusBadRequest)
}
