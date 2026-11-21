//go:build integration

// #2852 integration tests — a synced policy is read-only in security-atlas.
// Creating its next version, changing its review date and every draft path
// answer 409 "managed in Confluence"; acknowledgements still work, and its
// control links change in place through PATCH /v1/policies/{id}/controls,
// the one in-tool change it accepts. Real Postgres under RLS through the
// full HTTP stack, as sync_integration_test.go, whose helpers these use.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// syncedPolicy syncs one page and returns its policy id.
func syncedPolicy(t *testing.T, s syncSetup, pageID string, props map[string]any) string {
	t.Helper()
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage(pageID, "Synced Policy "+pageID, "1.0", "Body.", props))
	if resp.Created != 1 {
		t.Fatalf("sync = %+v; want one created", resp)
	}
	return resp.Results[0].PolicyID
}

// wantManaged asserts a 409 whose message names Confluence.
func wantManaged(t *testing.T, s setupResult, method, path string, body []byte, bearer string) {
	t.Helper()
	raw := s.do(t, method, path, body, bearer, http.StatusConflict)
	if !strings.Contains(string(raw), "managed in Confluence") {
		t.Errorf("%s %s = %s; want a managed-in-Confluence refusal", method, path, raw)
	}
}

func draftJSON(t *testing.T, ctrl uuid.UUID, extra map[string]any) []byte {
	t.Helper()
	m := map[string]any{
		"title":              "Synced Policy draft",
		"version":            "2.0",
		"body_md":            "Edited in the tool.",
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{ctrl.String()},
	}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

// Every in-tool edit of a synced row is refused, and writes nothing.
func TestSyncLock_RefusesInToolEdits(t *testing.T) {
	s := newSyncSetup(t)
	id := syncedPolicy(t, s, "8001", map[string]any{"next_review_at": "2026-03-01"})
	before := getPolicyVersion(t, s.setupResult, id)

	// Next version.
	wantManaged(t, s.setupResult, http.MethodPost, "/v1/policies",
		draftJSON(t, s.ctrlID, map[string]any{"predecessor_id": id}), s.adminBear)
	// Review date.
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+id+"/review-date",
		[]byte(`{"next_review_at":"2027-01-01"}`), s.adminBear)
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+id+"/review-date",
		[]byte(`{"next_review_at":null}`), s.adminBear)
	// Draft paths on the synced row itself.
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+id, draftJSON(t, s.ctrlID, nil), s.adminBear)
	wantManaged(t, s.setupResult, http.MethodDelete, "/v1/policies/"+id, nil, s.adminBear)
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear)
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.approverBear)
	wantManaged(t, s.setupResult, http.MethodPost, "/v1/policies/"+id+"/publish",
		[]byte(`{"new_version":"2.0"}`), s.adminBear)

	after := getPolicyVersion(t, s.setupResult, id)
	if after.Status != "published" || after.Version != before.Version || after.BodyMd != before.BodyMd {
		t.Errorf("synced policy after refusals = %+v; want unchanged %+v", after, before)
	}
	var reviewDate string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT to_char(next_review_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM policies WHERE id = $1`, id).Scan(&reviewDate); err != nil {
		t.Fatalf("read review date: %v", err)
	}
	if reviewDate != "2026-03-01" {
		t.Errorf("next_review_at = %s; want the page's 2026-03-01", reviewDate)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != 1 {
		t.Errorf("policies = %d; want only the synced one", n)
	}
	var audit int
	if err := s.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM policy_audit_log WHERE policy_id = $1 AND action <> 'synced'`, id).Scan(&audit); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audit != 0 {
		t.Errorf("non-sync audit rows on the synced policy = %d, want 0", audit)
	}
}

// A draft of a synced chain (a next version started in the tool before its
// chain was synced) cannot be edited, submitted, approved or published.
// It can still be discarded, which frees the successor slot the page's
// next version needs.
func TestSyncLock_DraftOfSyncedChain(t *testing.T) {
	s := newSyncSetup(t)
	id := syncedPolicy(t, s, "8101", nil)
	var created struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodPost, "/v1/policies", draftJSON(t, s.ctrlID, nil), s.adminBear, http.StatusCreated), &created); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	draft := created.Policy.ID
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET predecessor_id = $1 WHERE id = $2`, id, draft); err != nil {
		t.Fatalf("chain draft: %v", err)
	}

	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+draft, draftJSON(t, s.ctrlID, nil), s.adminBear)
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+draft+"/submit", nil, s.adminBear)

	// A draft already past review when its chain was synced.
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET status = 'under_review', submitted_by = 'someone-else', submitted_at = now() WHERE id = $1`, draft); err != nil {
		t.Fatalf("move draft to review: %v", err)
	}
	wantManaged(t, s.setupResult, http.MethodPatch, "/v1/policies/"+draft+"/approve", nil, s.approverBear)
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET status = 'approved', approved_at = now() WHERE id = $1`, draft); err != nil {
		t.Fatalf("move draft to approved: %v", err)
	}
	wantManaged(t, s.setupResult, http.MethodPost, "/v1/policies/"+draft+"/publish",
		[]byte(`{"new_version":"2.0"}`), s.adminBear)
	if p := getPolicyVersion(t, s.setupResult, id); p.Status != "published" {
		t.Errorf("synced policy = %+v; want still published", p)
	}

	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET status = 'draft', submitted_by = NULL, submitted_at = NULL, approved_at = NULL WHERE id = $1`, draft); err != nil {
		t.Fatalf("return to draft: %v", err)
	}
	s.do(t, http.MethodDelete, "/v1/policies/"+draft, nil, s.adminBear, http.StatusNoContent)
}

// Acknowledgements of a synced policy work as for any other policy.
func TestSyncLock_AcknowledgementStillWorks(t *testing.T) {
	s := newSyncSetup(t)
	_, engineer := s.issueRoledUserBearer(t, "engineer-2852@sync.test", []string{"grc_engineer"})
	id := syncedPolicy(t, s, "8201", map[string]any{"acknowledgment_required_roles": []string{"grc_engineer"}})
	if !pendingFor(t, s.setupResult, engineer, id) {
		t.Fatalf("synced policy %s is not pending for the engineer", id)
	}
	s.do(t, http.MethodPost, "/v1/policies/"+id+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)
	if pendingFor(t, s.setupResult, engineer, id) {
		t.Errorf("synced policy %s still pending after acknowledgement", id)
	}
}

type controlsAuditRow struct {
	actor      string
	prev, next []string
}

func controlsAuditRows(t *testing.T, s setupResult, policyID string) []controlsAuditRow {
	t.Helper()
	rows, err := s.admin.Query(context.Background(), `
		SELECT actor, previous_linked_control_ids::text[], new_linked_control_ids::text[]
		FROM policy_audit_log WHERE policy_id = $1 AND action = 'controls_updated' ORDER BY created_at, id`, policyID)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []controlsAuditRow
	for rows.Next() {
		var r controlsAuditRow
		if err := rows.Scan(&r.actor, &r.prev, &r.next); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	return out
}

func controlsBody(ids ...string) []byte {
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(map[string]any{"linked_control_ids": ids})
	return b
}

// PATCH /v1/policies/{id}/controls replaces a synced policy's links in
// place: no new version, one controls_updated audit row with the links
// before and after, and the control lists the policy. The same set again
// writes nothing; duplicates count once.
func TestSyncLock_ControlLinksEditable(t *testing.T) {
	s := newSyncSetup(t)
	id := syncedPolicy(t, s, "8301", nil)
	ctrl := s.ctrlID.String()

	var env struct {
		Policy policyVersionWire `json:"policy"`
	}
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", controlsBody(ctrl, ctrl), s.adminBear, http.StatusOK)
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Policy.ID != id || len(env.Policy.LinkedControlIDs) != 1 || env.Policy.LinkedControlIDs[0] != ctrl ||
		env.Policy.Status != "published" || env.Policy.Version != "1.0" {
		t.Fatalf("after link = %+v; want %s published 1.0 linked to %s", env.Policy, id, ctrl)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != 1 {
		t.Errorf("policies = %d; want no new version", n)
	}

	audit := controlsAuditRows(t, s.setupResult, id)
	if len(audit) != 1 || len(audit[0].prev) != 0 || len(audit[0].next) != 1 || audit[0].next[0] != ctrl || audit[0].actor == "" {
		t.Fatalf("controls_updated rows = %+v; want one, [] -> [%s]", audit, ctrl)
	}

	var listed struct {
		Policies []struct {
			PolicyID string `json:"policy_id"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/controls/"+ctrl+"/policies", nil, s.adminBear, http.StatusOK), &listed); err != nil {
		t.Fatalf("decode control policies: %v", err)
	}
	if !containsID(len(listed.Policies), func(i int) string { return listed.Policies[i].PolicyID }, id) {
		t.Errorf("GET /v1/controls/%s/policies = %+v; want it to list %s", ctrl, listed, id)
	}

	// The same set again is a no-op.
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", controlsBody(ctrl), s.adminBear, http.StatusOK)
	if n := len(controlsAuditRows(t, s.setupResult, id)); n != 1 {
		t.Errorf("controls_updated rows after a no-op = %d, want 1", n)
	}

	// Removing every link is a change.
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", controlsBody(), s.adminBear, http.StatusOK)
	audit = controlsAuditRows(t, s.setupResult, id)
	if len(audit) != 2 || len(audit[1].prev) != 1 || len(audit[1].next) != 0 {
		t.Errorf("controls_updated rows after unlinking = %+v; want a second, [%s] -> []", audit, ctrl)
	}
}

// PATCH /v1/policies/{id}/controls refuses what is not a published synced
// policy (409), a control in another tenant (422), a viewer (403), and a
// body without linked_control_ids (400).
func TestSyncLock_ControlLinksRefusals(t *testing.T) {
	s := newSyncSetup(t)
	id := syncedPolicy(t, s, "8401", nil)

	// A tenant-authored draft changes its links in the draft itself.
	var created struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodPost, "/v1/policies", draftJSON(t, s.ctrlID, nil), s.adminBear, http.StatusCreated), &created); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	raw := s.do(t, http.MethodPatch, "/v1/policies/"+created.Policy.ID+"/controls", controlsBody(s.ctrlID.String()), s.adminBear, http.StatusConflict)
	if !strings.Contains(string(raw), "new version") {
		t.Errorf("non-synced refusal = %s; want it to point at a draft or new version", raw)
	}

	tenantB := freshTenant(t, s.admin)
	foreign := seedControl(t, s.admin, tenantB)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", controlsBody(foreign.String()), s.adminBear, http.StatusUnprocessableEntity)

	_, viewer := s.issueRoledUserBearer(t, "viewer-2852@sync.test", []string{"viewer"})
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", controlsBody(s.ctrlID.String()), viewer, http.StatusForbidden)

	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/controls", []byte(`{}`), s.adminBear, http.StatusBadRequest)
	s.do(t, http.MethodPatch, "/v1/policies/"+uuid.NewString()+"/controls", controlsBody(), s.adminBear, http.StatusNotFound)

	if p := getPolicyVersion(t, s.setupResult, id); len(p.LinkedControlIDs) != 0 {
		t.Errorf("links after refusals = %v; want none", p.LinkedControlIDs)
	}
	if n := len(controlsAuditRows(t, s.setupResult, id)); n != 0 {
		t.Errorf("controls_updated rows after refusals = %d, want 0", n)
	}
}
