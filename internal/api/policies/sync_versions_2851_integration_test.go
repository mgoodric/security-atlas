//go:build integration

// #2851 integration tests — POST /v1/policies:sync version chaining and
// drift. A change in a page's declared version publishes the next version
// of its policy, chained to and superseding the previous synced row; a
// body change without a bump writes no version and is recorded as drift
// on the policy until the body matches again or a new version arrives.
// Real Postgres under RLS through the full HTTP stack, as
// sync_integration_test.go.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// policyVersionWire is the part of a policy the #2851 tests read.
type policyVersionWire struct {
	ID               string   `json:"id"`
	PredecessorID    string   `json:"predecessor_id"`
	Version          string   `json:"version"`
	BodyMd           string   `json:"body_md"`
	Status           string   `json:"status"`
	LinkedControlIDs []string `json:"linked_control_ids"`
	SupersededAt     string   `json:"superseded_at"`
	SyncDrift        *struct {
		DetectedAt       time.Time `json:"detected_at"`
		SourceBodySHA256 string    `json:"source_body_sha256"`
	} `json:"sync_drift"`
}

func getPolicyVersion(t *testing.T, s setupResult, id string) policyVersionWire {
	t.Helper()
	var env struct {
		Policy policyVersionWire `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &env); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return env.Policy
}

// listedPolicy returns the policy id as GET /v1/policies lists it, with
// or without ?include=ack_rate (two row shapes on the server).
func listedPolicy(t *testing.T, s setupResult, path, id string) policyVersionWire {
	t.Helper()
	var list struct {
		Policies []policyVersionWire `json:"policies"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, path, nil, s.adminBear, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, p := range list.Policies {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("%s does not list %s", path, id)
	return policyVersionWire{}
}

// A version bump publishes the next version: chained to the previous
// synced row, which is superseded, with its control links carried over
// and one more 'synced' audit row. Re-pushing the new version is a no-op.
func TestSync_VersionBumpChainsAndSupersedes(t *testing.T) {
	s := newSyncSetup(t)
	keyID, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const v1Body = "# Access Control\n\nEveryone uses MFA."
	first := postSync(t, s.setupResult, key, http.StatusOK, syncPage("1001", "Access Control Policy", "1.0", v1Body, nil))
	if first.Created != 1 {
		t.Fatalf("first sync = %+v", first)
	}
	v1 := first.Results[0].PolicyID

	// Control links are kept in security-atlas; give the first version one.
	control := seedControl(t, s.admin, s.tenant)
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET linked_control_ids = ARRAY[$1::uuid] WHERE tenant_id = $2 AND id = $3`,
		control, s.tenant, v1); err != nil {
		t.Fatalf("link control: %v", err)
	}

	const v2Body = v1Body + "\n\nHardware keys for admins."
	bumped := syncPage("1001", "Access Control Policy", "1.1", v2Body, nil)
	resp := postSync(t, s.setupResult, key, http.StatusOK, bumped)
	if resp.NewVersion != 1 || resp.Created != 0 || resp.Unchanged != 0 || resp.Drift != 0 || resp.Skipped != 0 || len(resp.Results) != 1 {
		t.Fatalf("bumped sync = %+v", resp)
	}
	res := resp.Results[0]
	if res.Status != "new_version" || res.PolicyID == "" || res.PolicyID == v1 || res.Version != "1.1" || res.PredecessorID != v1 {
		t.Fatalf("bumped result = %+v; want new_version chained to %s", res, v1)
	}
	v2 := res.PolicyID

	old := getPolicyVersion(t, s.setupResult, v1)
	if old.Status != "superseded" || old.SupersededAt == "" || old.Version != "1.0" || old.BodyMd != v1Body {
		t.Errorf("previous version = %+v; want superseded 1.0 with its body", old)
	}
	next := getPolicyVersion(t, s.setupResult, v2)
	if next.Status != "published" || next.PredecessorID != v1 || next.Version != "1.1" || next.BodyMd != v2Body {
		t.Errorf("new version = %+v; want published 1.1 chained to %s", next, v1)
	}
	if len(next.LinkedControlIDs) != 1 || next.LinkedControlIDs[0] != control.String() {
		t.Errorf("new version links = %v; want [%s] carried over", next.LinkedControlIDs, control)
	}

	audit := syncedAuditRows(t, s.setupResult, s.tenant)
	if len(audit) != 2 || audit[1].version != "1.1" || audit[1].hash != sha(v2Body) || audit[1].actor != keyID {
		t.Errorf("synced audit rows = %+v; want a second row for 1.1 by %s", audit, keyID)
	}

	// Re-pushing the new version changes nothing.
	before := countPolicies(t, s.setupResult, s.tenant)
	again := postSync(t, s.setupResult, key, http.StatusOK, bumped)
	if again.Unchanged != 1 || again.Results[0].PolicyID != v2 {
		t.Errorf("re-push of 1.1 = %+v; want unchanged %s", again, v2)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != before {
		t.Errorf("policies after re-push = %d, want %d", n, before)
	}
}

// Acknowledgements belong to a version: after a bump the staff who
// acknowledged the previous version are asked again, the superseded
// version can no longer be acknowledged, and the new one can.
func TestSync_NewVersionNeedsFreshAcknowledgement(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-2851@sync.test", []string{"grc_engineer"})
	props := map[string]any{"acknowledgment_required_roles": []string{"grc_engineer"}}
	v1 := postSync(t, s.setupResult, key, http.StatusOK, syncPage("2001", "Engineering Policy", "1.0", "Body.", props)).Results[0].PolicyID
	s.do(t, http.MethodPost, "/v1/policies/"+v1+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)
	if pendingFor(t, s.setupResult, engineer, v1) {
		t.Fatalf("v1 still pending after the engineer acknowledged it")
	}

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("2001", "Engineering Policy", "2.0", "Body, revised.", props))
	if resp.NewVersion != 1 {
		t.Fatalf("bumped sync = %+v", resp)
	}
	v2 := resp.Results[0].PolicyID
	if !pendingFor(t, s.setupResult, engineer, v2) {
		t.Errorf("GET /v1/me/acknowledgments does not ask the engineer to acknowledge the new version %s", v2)
	}
	if pendingFor(t, s.setupResult, engineer, v1) {
		t.Errorf("the superseded version %s is still pending", v1)
	}
	s.do(t, http.MethodPost, "/v1/policies/"+v1+"/acknowledge", []byte("{}"), engineer, http.StatusConflict)
	s.do(t, http.MethodPost, "/v1/policies/"+v2+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)
	if pendingFor(t, s.setupResult, engineer, v2) {
		t.Errorf("v2 still pending after the engineer acknowledged it")
	}
}

func pendingFor(t *testing.T, s setupResult, bearer, policyID string) bool {
	t.Helper()
	var mine struct {
		Pending []struct {
			PolicyID string `json:"policy_id"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/me/acknowledgments", nil, bearer, http.StatusOK), &mine); err != nil {
		t.Fatalf("decode acknowledgments: %v", err)
	}
	return containsID(len(mine.Pending), func(i int) string { return mine.Pending[i].PolicyID }, policyID)
}

// A body change without a version bump writes no version and no audit
// row; it is reported as drift and recorded on the policy (detected at,
// the source's current body hash), visible on GET and in the list. A
// second unbumped edit keeps the first detection time and updates the
// hash. Reverting the body clears the drift.
func TestSync_BodyChangeWithoutBumpIsDrift(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const body = "# Data Retention\n\nKeep logs for a year."
	page := syncPage("3001", "Data Retention Policy", "4.0", body, nil)
	id := postSync(t, s.setupResult, key, http.StatusOK, page).Results[0].PolicyID
	if d := getPolicyVersion(t, s.setupResult, id).SyncDrift; d != nil {
		t.Fatalf("a fresh synced policy has drift %+v", d)
	}
	policies := countPolicies(t, s.setupResult, s.tenant)

	const edited = body + "\n\nKeep backups for two."
	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("3001", "Data Retention Policy", "4.0", edited, nil))
	if resp.Drift != 1 || resp.Created != 0 || resp.NewVersion != 0 || resp.Unchanged != 0 || resp.Skipped != 0 {
		t.Fatalf("edited sync = %+v", resp)
	}
	res := resp.Results[0]
	if res.Status != "drift" || res.PolicyID != id || res.Version != "4.0" || res.Reason == "" || res.DriftDetectedAt == nil {
		t.Fatalf("drift result = %+v", res)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies after drift = %d, want %d (no version written)", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != 1 {
		t.Errorf("synced audit rows after drift = %d, want 1", n)
	}
	got := getPolicyVersion(t, s.setupResult, id)
	if got.Status != "published" || got.BodyMd != body || got.Version != "4.0" {
		t.Errorf("drifted policy = %+v; want the published text untouched", got)
	}
	if got.SyncDrift == nil || got.SyncDrift.SourceBodySHA256 != sha(edited) || !got.SyncDrift.DetectedAt.Equal(*res.DriftDetectedAt) {
		t.Fatalf("sync_drift = %+v; want hash %s detected at %v", got.SyncDrift, sha(edited), res.DriftDetectedAt)
	}
	for _, path := range []string{"/v1/policies", "/v1/policies?include=ack_rate"} {
		if d := listedPolicy(t, s.setupResult, path, id).SyncDrift; d == nil || d.SourceBodySHA256 != sha(edited) {
			t.Errorf("%s sync_drift = %+v; want hash %s", path, d, sha(edited))
		}
	}
	firstSeen := got.SyncDrift.DetectedAt

	// A second unbumped edit: still drift, first detection time kept.
	const editedAgain = edited + "\n\nAnd audit trails for seven."
	again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("3001", "Data Retention Policy", "4.0", editedAgain, nil))
	if again.Drift != 1 {
		t.Fatalf("second edit = %+v", again)
	}
	got = getPolicyVersion(t, s.setupResult, id)
	if got.SyncDrift == nil || got.SyncDrift.SourceBodySHA256 != sha(editedAgain) || !got.SyncDrift.DetectedAt.Equal(firstSeen) {
		t.Errorf("sync_drift after a second edit = %+v; want hash %s, detected at %v", got.SyncDrift, sha(editedAgain), firstSeen)
	}

	// Reverting the edit clears the drift.
	reverted := postSync(t, s.setupResult, key, http.StatusOK, page)
	if reverted.Unchanged != 1 || reverted.Drift != 0 || reverted.Results[0].PolicyID != id {
		t.Fatalf("reverted sync = %+v", reverted)
	}
	if d := getPolicyVersion(t, s.setupResult, id).SyncDrift; d != nil {
		t.Errorf("sync_drift after revert = %+v; want none", d)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies after revert = %d, want %d", n, policies)
	}
}

// Drift clears when the page arrives with a new version: the new version
// is published with no drift, and the superseded row's drift is cleared.
func TestSync_DriftClearsOnBump(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	v1 := postSync(t, s.setupResult, key, http.StatusOK, syncPage("4001", "Vendor Policy", "1.0", "Body.", nil)).Results[0].PolicyID
	const edited = "Body, edited."
	if resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("4001", "Vendor Policy", "1.0", edited, nil)); resp.Drift != 1 {
		t.Fatalf("edited sync = %+v", resp)
	}

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("4001", "Vendor Policy", "1.1", edited, nil))
	if resp.NewVersion != 1 || resp.Drift != 0 || resp.Results[0].PredecessorID != v1 {
		t.Fatalf("bumped sync = %+v", resp)
	}
	v2 := resp.Results[0].PolicyID
	if p := getPolicyVersion(t, s.setupResult, v2); p.SyncDrift != nil || p.BodyMd != edited || p.Status != "published" {
		t.Errorf("new version = %+v; want published with the edited body and no drift", p)
	}
	if p := getPolicyVersion(t, s.setupResult, v1); p.SyncDrift != nil || p.Status != "superseded" {
		t.Errorf("superseded version = %+v; want superseded with no drift", p)
	}
}

// The drift columns are rejected on a row that is not synced, and must
// come together (20261281000000_policy_sync_drift).
func TestSync_DriftOnlyOnSyncedRows(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	synced := postSync(t, s.setupResult, key, http.StatusOK, syncPage("6001", "Synced", "1.0", "Body.", nil)).Results[0].PolicyID
	authored := createDraftPolicy(t, s.setupResult, "Authored-2851", "1.0", nil)
	hash := sha("other")
	for name, c := range map[string]struct {
		id, set string
	}{
		"authored row":        {authored, `sync_drift_detected_at = now(), sync_drift_body_sha256 = '` + hash + `'`},
		"time without hash":   {synced, `sync_drift_detected_at = now()`},
		"hash without time":   {synced, `sync_drift_body_sha256 = '` + hash + `'`},
		"hash not sha256 hex": {synced, `sync_drift_detected_at = now(), sync_drift_body_sha256 = 'nope'`},
	} {
		if _, err := s.admin.Exec(context.Background(),
			`UPDATE policies SET `+c.set+` WHERE tenant_id = $1 AND id = $2`, s.tenant, uuid.MustParse(c.id)); err == nil {
			t.Errorf("%s: drift columns accepted", name)
		}
	}
}
