//go:build integration

// #2851 integration tests, continued: the edges of version chaining and
// drift on POST /v1/policies:sync. A bump skipped while an in-tool draft
// holds the successor slot, a version that moves backwards, and a
// repeated drift that writes nothing. Helpers are in
// sync_integration_test.go and sync_versions_2851_integration_test.go.

package policies_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// A version chain is linear. While an in-tool draft already holds a
// synced row's successor slot, a bump of that page is skipped with a
// reason; the run still syncs its other pages, and the synced row stays
// published. Once the draft is discarded the bump syncs.
func TestSync_BumpSkippedWhileInToolSuccessorExists(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	v1 := postSync(t, s.setupResult, key, http.StatusOK, syncPage("7001", "Incident Policy", "1.0", "Body.", nil)).Results[0].PolicyID

	draftBody, _ := json.Marshal(map[string]any{
		"title":              "Incident Policy",
		"version":            "1.1-draft",
		"body_md":            "Edited in the tool.",
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{s.ctrlID.String()},
		"source_attribution": "tenant_authored",
	})
	var created struct {
		Policy struct {
			ID string `json:"id"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodPost, "/v1/policies", draftBody, s.adminBear, http.StatusCreated), &created); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	draft := created.Policy.ID
	// Since #2852 the API refuses a next version of a synced policy, so
	// the draft is chained to v1 directly: a next version started in the
	// tool before its chain was synced.
	if _, err := s.admin.Exec(context.Background(),
		`UPDATE policies SET predecessor_id = $1 WHERE id = $2`, v1, draft); err != nil {
		t.Fatalf("chain draft to v1: %v", err)
	}

	bumped := syncPage("7001", "Incident Policy", "1.1", "Body, revised.", nil)
	resp := postSync(t, s.setupResult, key, http.StatusOK, bumped, syncPage("7002", "Other Policy", "1.0", "Other.", nil))
	if resp.Skipped != 1 || resp.Created != 1 || resp.NewVersion != 0 {
		t.Fatalf("sync with a taken successor = %+v", resp)
	}
	for _, r := range resp.Results {
		if r.PageID == "7001" && (r.Status != "skipped" || r.PolicyID != v1 || !strings.Contains(r.Reason, draft)) {
			t.Errorf("bumped page = %+v; want skipped naming draft %s", r, draft)
		}
	}
	if p := getPolicyVersion(t, s.setupResult, v1); p.Status != "published" || p.Version != "1.0" {
		t.Errorf("synced row = %+v; want still published 1.0", p)
	}

	s.do(t, http.MethodDelete, "/v1/policies/"+draft, nil, s.adminBear, http.StatusNoContent)
	after := postSync(t, s.setupResult, key, http.StatusOK, bumped)
	if after.NewVersion != 1 || after.Results[0].PredecessorID != v1 {
		t.Errorf("bump after discarding the draft = %+v; want new_version chained to %s", after, v1)
	}
}

// A version that moves backwards (1.1 -> 1.0) is still a change in the
// declared version: it publishes a new version chained to 1.1, and staff
// acknowledge again.
func TestSync_VersionRevertIsANewVersion(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-revert-2851@sync.test", []string{"grc_engineer"})
	props := map[string]any{"acknowledgment_required_roles": []string{"grc_engineer"}}
	postSync(t, s.setupResult, key, http.StatusOK, syncPage("8001", "Backup Policy", "1.0", "Body.", props))
	v11 := postSync(t, s.setupResult, key, http.StatusOK, syncPage("8001", "Backup Policy", "1.1", "Body, revised.", props)).Results[0].PolicyID
	s.do(t, http.MethodPost, "/v1/policies/"+v11+"/acknowledge", []byte("{}"), engineer, http.StatusCreated)

	resp := postSync(t, s.setupResult, key, http.StatusOK, syncPage("8001", "Backup Policy", "1.0", "Body.", props))
	if resp.NewVersion != 1 || resp.Results[0].Version != "1.0" || resp.Results[0].PredecessorID != v11 {
		t.Fatalf("reverted version = %+v; want new_version 1.0 chained to %s", resp, v11)
	}
	reverted := resp.Results[0].PolicyID
	if p := getPolicyVersion(t, s.setupResult, v11); p.Status != "superseded" {
		t.Errorf("1.1 = %+v; want superseded", p)
	}
	if !pendingFor(t, s.setupResult, engineer, reverted) {
		t.Errorf("the engineer is not asked to acknowledge the reverted version %s", reverted)
	}
}

// Re-pushing a body that is already recorded as drift writes nothing:
// the row's updated_at and drift stay as they were.
func TestSync_RepeatedDriftWritesNothing(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	id := postSync(t, s.setupResult, key, http.StatusOK, syncPage("9001", "Asset Policy", "1.0", "Body.", nil)).Results[0].PolicyID
	edited := syncPage("9001", "Asset Policy", "1.0", "Body, edited.", nil)
	if resp := postSync(t, s.setupResult, key, http.StatusOK, edited); resp.Drift != 1 {
		t.Fatalf("edited sync = %+v", resp)
	}
	updatedAt := func() time.Time {
		var at time.Time
		if err := s.admin.QueryRow(context.Background(),
			`SELECT updated_at FROM policies WHERE tenant_id = $1 AND id = $2`, s.tenant, id).Scan(&at); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		return at
	}
	before := updatedAt()
	again := postSync(t, s.setupResult, key, http.StatusOK, edited)
	if again.Drift != 1 || again.Results[0].DriftDetectedAt == nil {
		t.Fatalf("repeated drift = %+v; want drift still reported", again)
	}
	if after := updatedAt(); !after.Equal(before) {
		t.Errorf("updated_at moved from %v to %v on a repeated drift", before, after)
	}
}
