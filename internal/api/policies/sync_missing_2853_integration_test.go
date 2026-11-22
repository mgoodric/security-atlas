//go:build integration

// #2853 integration tests — POST /v1/policies:sync missing pages. Each run
// pushes the full set of its source's labelled pages, so a previously
// synced page absent from a run lost its label or was deleted. It is
// reported missing and gets a needs-attention item; the policy stays
// published and acknowledgeable. A later run carrying the page clears
// both. Pages of another source, or of another tenant, are never marked.
// Real Postgres under RLS through the full HTTP stack, as
// sync_integration_test.go.

package policies_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// policyMissingWire is the part of a policy the #2853 tests read.
type policyMissingWire struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	Version          string     `json:"version"`
	BodyMd           string     `json:"body_md"`
	SupersededAt     string     `json:"superseded_at"`
	SyncMissingSince *time.Time `json:"sync_missing_since"`
}

func getPolicyMissing(t *testing.T, s setupResult, id string) policyMissingWire {
	t.Helper()
	var env struct {
		Policy policyMissingWire `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &env); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return env.Policy
}

type needsAttentionItem2853 struct {
	Band   string     `json:"band"`
	Type   string     `json:"type"`
	ID     string     `json:"id"`
	Link   string     `json:"link"`
	Action string     `json:"action"`
	Since  *time.Time `json:"since"`
	Owner  *struct {
		Role string `json:"role"`
	} `json:"owner"`
}

// sourceMissingItems returns the source_missing items the admin's
// everyone view of GET /v1/needs-attention lists, by policy id.
func sourceMissingItems(t *testing.T, s setupResult) map[string]needsAttentionItem2853 {
	t.Helper()
	var na struct {
		Items  []needsAttentionItem2853 `json:"items"`
		Counts map[string]int           `json:"counts"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/needs-attention?for=all&limit=100&type=policy", nil, s.adminBear, http.StatusOK), &na); err != nil {
		t.Fatalf("decode needs-attention: %v", err)
	}
	out := map[string]needsAttentionItem2853{}
	for _, it := range na.Items {
		if it.Band == "source_missing" {
			out[it.ID] = it
		}
	}
	if na.Counts["source_missing"] != len(out) {
		t.Errorf("counts[source_missing] = %d, items = %d", na.Counts["source_missing"], len(out))
	}
	return out
}

// A synced policy whose page is absent from a run is reported missing and
// gets a needs-attention item. The policy is unchanged: still published,
// same version and body, not superseded, and staff can still acknowledge
// it. A page the run carries is present even when it is skipped for a
// bad property. A second run without the page keeps the time it was first
// found missing.
func TestSync_AbsentPageIsMissing(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-2853@sync.test", []string{"grc_engineer"})
	props := map[string]any{"acknowledgment_required_roles": []string{"grc_engineer"}, "owner_role": "security_lead"}
	const goneBody = "# Access Control\n\nEveryone uses MFA."
	gone := syncPage("6001", "Access Control Policy", "1.0", goneBody, props)
	kept := syncPage("6002", "Data Retention Policy", "1.0", "Keep logs.", nil)
	badButPresent := syncPage("6003", "Backup Policy", "1.0", "Back up nightly.", nil)
	first := postSync(t, s.setupResult, key, http.StatusOK, gone, kept, badButPresent)
	if first.Created != 3 || first.Missing != 0 {
		t.Fatalf("first sync = %+v", first)
	}
	goneID, keptID, badID := first.Results[0].PolicyID, first.Results[1].PolicyID, first.Results[2].PolicyID
	policies := countPolicies(t, s.setupResult, s.tenant)
	audit := len(syncedAuditRows(t, s.setupResult, s.tenant))

	// 6001 is gone; 6003 arrives with a missing required property.
	invalid := syncPage("6003", "Backup Policy", "1.0", "Back up nightly.", map[string]any{"approved_by_name": nil})
	resp := postSync(t, s.setupResult, key, http.StatusOK, kept, invalid)
	if resp.Missing != 1 || resp.Unchanged != 1 || resp.Skipped != 1 || resp.Created != 0 || resp.NewVersion != 0 || len(resp.Results) != 3 {
		t.Fatalf("sync without 6001 = %+v", resp)
	}
	res := resp.Results[len(resp.Results)-1]
	if res.Status != "missing" || res.PageID != "6001" || res.PolicyID != goneID || res.Title != "Access Control Policy" ||
		res.Version != "1.0" || res.Reason == "" || res.MissingSince == nil {
		t.Fatalf("missing result = %+v", res)
	}
	since := *res.MissingSince

	// The policy is unchanged and stays in force.
	p := getPolicyMissing(t, s.setupResult, goneID)
	if p.Status != "published" || p.SupersededAt != "" || p.Version != "1.0" || p.BodyMd != goneBody {
		t.Errorf("missing policy = %+v; want it published and unchanged", p)
	}
	if p.SyncMissingSince == nil || !p.SyncMissingSince.Equal(since) {
		t.Errorf("sync_missing_since = %v, want %v", p.SyncMissingSince, since)
	}
	for _, id := range []string{keptID, badID} {
		if m := getPolicyMissing(t, s.setupResult, id).SyncMissingSince; m != nil {
			t.Errorf("present page's policy %s has sync_missing_since %v", id, m)
		}
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies = %d, want %d", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != audit {
		t.Errorf("synced audit rows = %d, want %d", n, audit)
	}
	if !pendingFor(t, s.setupResult, engineer, goneID) {
		t.Errorf("the missing page's policy is no longer asked for acknowledgement")
	}
	s.do(t, http.MethodPost, "/v1/policies/"+goneID+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)

	items := sourceMissingItems(t, s.setupResult)
	it, ok := items[goneID]
	if !ok || len(items) != 1 {
		t.Fatalf("source_missing items = %+v; want only %s", items, goneID)
	}
	if it.Type != "policy" || it.Link != "/policies/"+goneID || it.Action == "" || it.Since == nil || !it.Since.Equal(since) ||
		it.Owner == nil || it.Owner.Role != "security_lead" {
		t.Errorf("source_missing item = %+v", it)
	}

	// Still absent: still missing, since the first sighting.
	again := postSync(t, s.setupResult, key, http.StatusOK, kept, badButPresent)
	last := again.Results[len(again.Results)-1]
	if again.Missing != 1 || last.MissingSince == nil || !last.MissingSince.Equal(since) {
		t.Errorf("second sync without 6001 = %+v; want missing since %v", again, since)
	}
}

// The missing state and the needs-attention item clear when the page
// reappears in a later run, whether unchanged or with a new version.
func TestSync_ReappearingPageClearsMissing(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	page := syncPage("7001", "Vendor Policy", "2.0", "Review vendors yearly.", nil)
	id := postSync(t, s.setupResult, key, http.StatusOK, page).Results[0].PolicyID

	if resp := postEmptySync(t, s.setupResult, key, "SEC", true, http.StatusOK); resp.Missing != 1 || resp.Results[0].PolicyID != id {
		t.Fatalf("empty sync = %+v; want %s missing", resp, id)
	}
	if _, ok := sourceMissingItems(t, s.setupResult)[id]; !ok {
		t.Fatalf("no source_missing item for %s", id)
	}

	back := postSync(t, s.setupResult, key, http.StatusOK, page)
	if back.Missing != 0 || back.Unchanged != 1 || len(back.Results) != 1 || back.Results[0].PolicyID != id {
		t.Fatalf("sync with 7001 back = %+v", back)
	}
	if m := getPolicyMissing(t, s.setupResult, id).SyncMissingSince; m != nil {
		t.Errorf("sync_missing_since = %v after the page reappeared", m)
	}
	if items := sourceMissingItems(t, s.setupResult); len(items) != 0 {
		t.Errorf("source_missing items after the page reappeared = %+v", items)
	}

	// Gone again, then back with a new version: the new version starts in
	// step, the superseded row carries no missing state, and the item
	// clears.
	postEmptySync(t, s.setupResult, key, "SEC", true, http.StatusOK)
	bumped := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7001", "Vendor Policy", "2.1", "Review vendors twice a year.", nil))
	if bumped.NewVersion != 1 || bumped.Missing != 0 {
		t.Fatalf("bumped sync = %+v", bumped)
	}
	if m := getPolicyMissing(t, s.setupResult, bumped.Results[0].PolicyID).SyncMissingSince; m != nil {
		t.Errorf("new version has sync_missing_since %v", m)
	}
	if m := getPolicyMissing(t, s.setupResult, id).SyncMissingSince; m != nil {
		t.Errorf("superseded version has sync_missing_since %v", m)
	}
	if items := sourceMissingItems(t, s.setupResult); len(items) != 0 {
		t.Errorf("source_missing items after the new version = %+v", items)
	}
}

// A run is the full set of its own source only: policies synced from
// another space are never marked missing by it.
func TestSync_MissingIsPerSource(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	sec := postSyncFrom(t, s.setupResult, key, "SEC", http.StatusOK, syncPage("8001", "Security Policy", "1.0", "Body.", nil)).Results[0].PolicyID
	ops := postSyncFrom(t, s.setupResult, key, "OPS", http.StatusOK, syncPage("8002", "Operations Policy", "1.0", "Body.", nil)).Results[0].PolicyID

	// The same page id in another space is another page.
	other := postSyncFrom(t, s.setupResult, key, "OPS", http.StatusOK, syncPage("8001", "Ops Page 8001", "1.0", "Body.", nil))
	if other.Created != 1 || other.Missing != 1 || len(other.Results) != 2 || other.Results[1].PolicyID != ops {
		t.Fatalf("OPS sync = %+v; want 8001 created in OPS and only %s missing", other, ops)
	}
	if m := getPolicyMissing(t, s.setupResult, sec).SyncMissingSince; m != nil {
		t.Errorf("SEC policy marked missing by an OPS run: %v", m)
	}

	empty := postEmptySync(t, s.setupResult, key, "OPS", true, http.StatusOK)
	if empty.Missing != 2 {
		t.Fatalf("empty OPS sync = %+v; want the two OPS pages missing", empty)
	}
	for _, r := range empty.Results {
		if r.PolicyID == sec {
			t.Errorf("empty OPS run reports the SEC policy: %+v", r)
		}
	}
	items := sourceMissingItems(t, s.setupResult)
	if _, ok := items[sec]; ok || len(items) != 2 {
		t.Errorf("source_missing items = %+v; want the two OPS policies only", items)
	}
}

// Another tenant's run never marks this tenant's policies missing, and
// this tenant's needs-attention never lists the other tenant's.
func TestSync_MissingIsPerTenant(t *testing.T) {
	s := newSyncSetup(t)
	tenantB := freshTenant(t, s.admin)
	_, keyA := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, keyB := s.issueKey(t, tenantB, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	page := syncPage("9001", "Shared Page Policy", "1.0", "Body.", nil)
	a := postSync(t, s.setupResult, keyA, http.StatusOK, page).Results[0].PolicyID
	b := postSync(t, s.setupResult, keyB, http.StatusOK, page).Results[0].PolicyID

	resp := postEmptySync(t, s.setupResult, keyB, "SEC", true, http.StatusOK)
	if resp.Missing != 1 || len(resp.Results) != 1 || resp.Results[0].PolicyID != b {
		t.Fatalf("tenant B empty sync = %+v; want only %s missing", resp, b)
	}
	if m := getPolicyMissing(t, s.setupResult, a).SyncMissingSince; m != nil {
		t.Errorf("tenant A policy marked missing by tenant B's run: %v", m)
	}
	if items := sourceMissingItems(t, s.setupResult); len(items) != 0 {
		t.Errorf("tenant A source_missing items = %+v; want none", items)
	}
	var missing bool
	if err := s.admin.QueryRow(t.Context(),
		`SELECT sync_missing_since IS NOT NULL FROM policies WHERE tenant_id = $1 AND id = $2`, tenantB, b).Scan(&missing); err != nil {
		t.Fatalf("read tenant B row: %v", err)
	}
	if !missing {
		t.Errorf("tenant B policy %s not marked missing", b)
	}
}

// sync_missing_since is rejected on a row that is not synced
// (20261287000000_policy_sync_missing).
func TestSync_MissingOnlyOnSyncedRows(t *testing.T) {
	s := newSyncSetup(t)
	authored := createDraftPolicy(t, s.setupResult, "Authored-2853", "1.0", nil)
	if _, err := s.admin.Exec(t.Context(),
		`UPDATE policies SET sync_missing_since = now() WHERE tenant_id = $1 AND id = $2`, s.tenant, authored); err == nil {
		t.Errorf("sync_missing_since accepted on an authored row")
	}
}
