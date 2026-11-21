//go:build integration

// #2854 integration tests — POST /v1/policies:sync takes over imported
// policies on a page's first sync. A published imported policy with the
// page's title and version becomes the page's synced policy in place; one
// with another version is superseded by the synced version as its next
// version; no match starts a new policy; and a policy authored in
// security-atlas is never taken over. Real Postgres under RLS through the
// full HTTP stack, as sync_integration_test.go.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// importPolicy imports one published policy through the import endpoint,
// acknowledged by grc_engineer, and returns its id and stored body.
func importPolicy(t *testing.T, s syncSetup, title, version string) (string, string) {
	t.Helper()
	resp := postImport(t, s.setupResult, s.adminBear, false, http.StatusOK,
		policyImportFile(title, version, "acknowledgment_required_roles: [grc_engineer]\n"))
	if resp.Imported != 1 || resp.Results[0].Policy == nil {
		t.Fatalf("import %q %s = %+v", title, version, resp)
	}
	id := resp.Results[0].Policy.ID
	var body string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT body_md FROM policies WHERE tenant_id = $1 AND id = $2`, s.tenant, id).Scan(&body); err != nil {
		t.Fatalf("read imported body: %v", err)
	}
	return id, body
}

type takeoverAuditRow struct {
	policyID, actor, sourceFile, sourceURL, version, previous string
}

func takeoverAuditRows(t *testing.T, s setupResult) []takeoverAuditRow {
	t.Helper()
	rows, err := s.admin.Query(context.Background(), `
		SELECT policy_id::text, actor, source_file, COALESCE(source_url, ''), COALESCE(policy_version, ''),
		       COALESCE(previous_source_attribution, '')
		FROM policy_audit_log WHERE tenant_id = $1 AND action = 'sync_takeover' ORDER BY created_at, id`, s.tenant)
	if err != nil {
		t.Fatalf("read takeover audit: %v", err)
	}
	defer rows.Close()
	var out []takeoverAuditRow
	for rows.Next() {
		var r takeoverAuditRow
		if err := rows.Scan(&r.policyID, &r.actor, &r.sourceFile, &r.sourceURL, &r.version, &r.previous); err != nil {
			t.Fatalf("scan takeover audit: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func getSyncedPolicy(t *testing.T, s setupResult, id string) policySyncedWire {
	t.Helper()
	var env struct {
		Policy policySyncedWire `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &env); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return env.Policy
}

func ackCount(t *testing.T, s setupResult, policyID string) int {
	t.Helper()
	var n int
	if err := s.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM policy_acknowledgments WHERE tenant_id = $1 AND policy_version_id = $2`,
		s.tenant, policyID).Scan(&n); err != nil {
		t.Fatalf("count acknowledgments: %v", err)
	}
	return n
}

// The same version: the imported row becomes the page's synced row in
// place. No new row; acknowledgements and control links stay on it; one
// takeover audit row names 'imported' and the key; re-pushing is a no-op.
func TestSync2854_TakeOverSameVersionInPlace(t *testing.T) {
	s := newSyncSetup(t)
	keyID, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-2854@sync.test", []string{"grc_engineer"})
	imported, body := importPolicy(t, s, "Access Control Policy", "1.0")
	control := seedControl(t, s.admin, s.tenant)
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET linked_control_ids = ARRAY[$1::uuid] WHERE tenant_id = $2 AND id = $3`,
		control, s.tenant, imported); err != nil {
		t.Fatalf("link control: %v", err)
	}
	s.do(t, http.MethodPost, "/v1/policies/"+imported+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)
	before := countPolicies(t, s.setupResult, s.tenant)

	page := syncPage("5001", "Access Control Policy", "1.0", body, nil)
	resp := postSync(t, s.setupResult, key, http.StatusOK, page)
	if resp.TakenOver != 1 || resp.Created != 0 || resp.NewVersion != 0 || resp.Unchanged != 0 || len(resp.Results) != 1 {
		t.Fatalf("first sync = %+v; want one taken_over", resp)
	}
	res := resp.Results[0]
	if res.Status != "taken_over" || res.PolicyID != imported || res.Version != "1.0" || res.PredecessorID != "" || res.DriftDetectedAt != nil {
		t.Fatalf("result = %+v; want taken_over of %s in place", res, imported)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != before {
		t.Errorf("policies after takeover = %d, want %d (no new row)", n, before)
	}

	got := getSyncedPolicy(t, s.setupResult, imported)
	pageURL := "https://acme.atlassian.net/wiki/spaces/SEC/pages/5001"
	if got.SourceAttribution != "synced" || got.Status != "published" || got.SourceURL != pageURL || got.BodyMd != body || got.HistoricalApproverName != "Jane Doe" {
		t.Errorf("taken-over policy = %+v; want published synced, source %s, body and approver kept", got, pageURL)
	}
	if v := getPolicyVersion(t, s.setupResult, imported); len(v.LinkedControlIDs) != 1 || v.LinkedControlIDs[0] != control.String() {
		t.Errorf("links after takeover = %v; want [%s]", v.LinkedControlIDs, control)
	}
	if n := ackCount(t, s.setupResult, imported); n != 1 {
		t.Errorf("acknowledgements of the taken-over version = %d, want 1 kept", n)
	}
	if pendingFor(t, s.setupResult, engineer, imported) {
		t.Errorf("the engineer is asked to acknowledge the taken-over policy again")
	}

	audit := takeoverAuditRows(t, s.setupResult)
	if len(audit) != 1 {
		t.Fatalf("takeover audit rows = %+v; want 1", audit)
	}
	if a := audit[0]; a.policyID != imported || a.actor != keyID || a.sourceFile != "5001" || a.sourceURL != pageURL || a.previous != "imported" || a.version != "1.0" {
		t.Errorf("takeover audit = %+v; want on %s by %s, previous imported", a, imported, keyID)
	}
	if rows := syncedAuditRows(t, s.setupResult, s.tenant); len(rows) != 0 {
		t.Errorf("synced audit rows = %+v; an in-place takeover writes none", rows)
	}

	again := postSync(t, s.setupResult, key, http.StatusOK, page)
	if again.Unchanged != 1 || again.TakenOver != 0 || again.Results[0].PolicyID != imported {
		t.Errorf("re-push = %+v; want unchanged %s", again, imported)
	}
	if n := len(takeoverAuditRows(t, s.setupResult)); n != 1 {
		t.Errorf("takeover audit rows after re-push = %d, want 1", n)
	}
}

// The same version with a different body: taken over in place, its text
// not rewritten, and the page's body recorded as drift.
func TestSync2854_TakeOverSameVersionDifferentBodyIsDrift(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, body := importPolicy(t, s, "Data Retention Policy", "3.0")
	const edited = "# Data Retention Policy\n\nConverted from Confluence, slightly differently."

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5101", "Data Retention Policy", "3.0", edited, nil))
	res := resp.Results[0]
	if resp.TakenOver != 1 || res.Status != "taken_over" || res.PolicyID != imported || res.DriftDetectedAt == nil || !strings.Contains(res.Reason, "drift") {
		t.Fatalf("sync = %+v; want taken_over of %s with drift", resp, imported)
	}
	v := getPolicyVersion(t, s.setupResult, imported)
	if v.BodyMd != body || v.SyncDrift == nil || v.SyncDrift.SourceBodySHA256 != sha(edited) {
		t.Errorf("policy = %+v; want the imported body kept and drift to %s", v, sha(edited))
	}
	// The page's next run with the same body is drift, not a takeover.
	again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5101", "Data Retention Policy", "3.0", edited, nil))
	if again.Drift != 1 || again.TakenOver != 0 {
		t.Errorf("re-push = %+v; want drift", again)
	}
}

// Another version: the synced version is published as the imported
// policy's next version, chained to it and superseding it. The imported
// row keeps its acknowledgements and its attribution.
func TestSync2854_TakeOverDifferentVersionChains(t *testing.T) {
	s := newSyncSetup(t)
	keyID, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-2854b@sync.test", []string{"grc_engineer"})
	imported, _ := importPolicy(t, s, "Incident Response Policy", "1.0")
	control := seedControl(t, s.admin, s.tenant)
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET linked_control_ids = ARRAY[$1::uuid] WHERE tenant_id = $2 AND id = $3`,
		control, s.tenant, imported); err != nil {
		t.Fatalf("link control: %v", err)
	}
	s.do(t, http.MethodPost, "/v1/policies/"+imported+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)

	props := map[string]any{"acknowledgment_required_roles": []string{"grc_engineer"}}
	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5201", "Incident Response Policy", "2.0", "Revised.", props))
	if resp.TakenOver != 1 || resp.Created != 0 || resp.NewVersion != 0 || len(resp.Results) != 1 {
		t.Fatalf("sync = %+v; want one taken_over", resp)
	}
	res := resp.Results[0]
	if res.Status != "taken_over" || res.PolicyID == imported || res.Version != "2.0" || res.PredecessorID != imported {
		t.Fatalf("result = %+v; want taken_over as the next version of %s", res, imported)
	}
	old := getSyncedPolicy(t, s.setupResult, imported)
	if old.Status != "superseded" || old.SourceAttribution != "imported" || old.Version != "1.0" {
		t.Errorf("imported row = %+v; want superseded, still imported", old)
	}
	next := getPolicyVersion(t, s.setupResult, res.PolicyID)
	if next.Status != "published" || next.PredecessorID != imported || len(next.LinkedControlIDs) != 1 || next.LinkedControlIDs[0] != control.String() {
		t.Errorf("synced version = %+v; want published, chained to %s, links carried", next, imported)
	}
	if n := ackCount(t, s.setupResult, imported); n != 1 {
		t.Errorf("acknowledgements of the superseded imported version = %d, want 1 kept", n)
	}
	if !pendingFor(t, s.setupResult, engineer, res.PolicyID) {
		t.Errorf("the engineer is not asked to acknowledge the new version")
	}

	audit := takeoverAuditRows(t, s.setupResult)
	if len(audit) != 1 || audit[0].policyID != imported || audit[0].actor != keyID || audit[0].previous != "imported" || audit[0].sourceFile != "5201" {
		t.Errorf("takeover audit = %+v; want one on %s by %s, previous imported", audit, imported, keyID)
	}
	if rows := syncedAuditRows(t, s.setupResult, s.tenant); len(rows) != 1 || rows[0].version != "2.0" || rows[0].actor != keyID {
		t.Errorf("synced audit rows = %+v; want one for 2.0", rows)
	}

	again := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5201", "Incident Response Policy", "2.0", "Revised.", props))
	if again.Unchanged != 1 || again.Results[0].PolicyID != res.PolicyID {
		t.Errorf("re-push = %+v; want unchanged", again)
	}
}

// No imported policy with the title: a new policy starts, with nothing
// to say about it.
func TestSync2854_NoMatchCreates(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, _ := importPolicy(t, s, "Vendor Policy", "1.0")

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5301", "Vendor Management Policy", "1.0", "Body.", nil))
	res := resp.Results[0]
	if resp.Created != 1 || resp.TakenOver != 0 || res.Status != "created" || res.PolicyID == imported || res.Reason != "" {
		t.Fatalf("sync = %+v; want created with no reason", resp)
	}
	if got := getSyncedPolicy(t, s.setupResult, imported); got.SourceAttribution != "imported" || got.Status != "published" {
		t.Errorf("unrelated imported policy = %+v; want untouched", got)
	}
	if n := len(takeoverAuditRows(t, s.setupResult)); n != 0 {
		t.Errorf("takeover audit rows = %d, want 0", n)
	}
}

// A policy authored in security-atlas with the page's title is never
// taken over: the page starts a new policy and the report says so.
func TestSync2854_AuthoredPolicyNeverTakenOver(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	authored := createAndPublishPolicy(t, s.setupResult, "Acceptable Use Policy", "1.0", nil)

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5401", "Acceptable Use Policy", "1.0", "Body.", nil))
	res := resp.Results[0]
	if resp.Created != 1 || resp.TakenOver != 0 || res.Status != "created" || res.PolicyID == authored {
		t.Fatalf("sync = %+v; want a new policy beside %s", resp, authored)
	}
	if !strings.Contains(res.Reason, authored) || !strings.Contains(res.Reason, "not taken over") {
		t.Errorf("reason = %q; want it to name %s as not taken over", res.Reason, authored)
	}
	if got := getSyncedPolicy(t, s.setupResult, authored); got.SourceAttribution != "tenant_authored" || got.Status != "published" {
		t.Errorf("authored policy = %+v; want untouched", got)
	}
	if n := len(takeoverAuditRows(t, s.setupResult)); n != 0 {
		t.Errorf("takeover audit rows = %d, want 0", n)
	}
}

// Another tenant's imported policy with the same title is never taken
// over.
func TestSync2854_OtherTenantsImportNotTakenOver(t *testing.T) {
	s := newSyncSetup(t)
	other := freshTenant(t, s.admin)
	_, key := s.issueKey(t, other, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	imported, _ := importPolicy(t, s, "Encryption Policy", "1.0")

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("5501", "Encryption Policy", "1.0", "Body.", nil))
	if resp.Created != 1 || resp.TakenOver != 0 {
		t.Fatalf("other tenant's sync = %+v; want created", resp)
	}
	if got := getSyncedPolicy(t, s.setupResult, imported); got.SourceAttribution != "imported" {
		t.Errorf("imported policy = %+v; want untouched by another tenant's sync", got)
	}
}
