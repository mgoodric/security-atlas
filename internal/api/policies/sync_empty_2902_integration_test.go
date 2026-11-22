//go:build integration

// #2902 integration tests — POST /v1/policies:sync empty pushes. A push is
// the source's full set, so a connector bug that sends `pages: []` would
// mark every published synced policy of the source missing. Such a push
// is refused with 422 and changes nothing unless it sets allow_empty, and
// a run that leaves more than half of a source's policies missing carries
// a warning. Real Postgres under RLS through the full HTTP stack, as
// sync_integration_test.go.

package policies_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

// postEmptySync pushes no pages for one space of the test site, with
// allow_empty as given.
func postEmptySync(t *testing.T, s setupResult, key, space string, allowEmpty bool, want int) syncResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"source":      map[string]string{"site": "acme.atlassian.net", "space": space},
		"pages":       []map[string]any{},
		"allow_empty": allowEmpty,
	})
	raw := s.do(t, http.MethodPost, "/v1/policies:sync", body, key, want)
	var out syncResp
	if want == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode sync response: %v (%s)", err, raw)
		}
	}
	return out
}

// An empty push without allow_empty for a source with published synced
// policies is refused and changes nothing; with allow_empty it marks every
// one missing and warns.
func TestSync_EmptyPushRefusedWithoutAllowEmpty(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	first := postSync(t, s.setupResult, key, http.StatusOK,
		syncPage("2902-1", "Access Control Policy", "1.0", "Use MFA.", nil),
		syncPage("2902-2", "Data Retention Policy", "1.0", "Keep logs.", nil))
	if first.Created != 2 || len(first.Warnings) != 0 {
		t.Fatalf("first sync = %+v", first)
	}
	ids := []string{first.Results[0].PolicyID, first.Results[1].PolicyID}
	policies := countPolicies(t, s.setupResult, s.tenant)
	audit := len(syncedAuditRows(t, s.setupResult, s.tenant))

	raw := s.do(t, http.MethodPost, "/v1/policies:sync",
		[]byte(`{"source":{"site":"acme.atlassian.net","space":"SEC"},"pages":[]}`), key, http.StatusUnprocessableEntity)
	if !strings.Contains(string(raw), "allow_empty") {
		t.Errorf("422 body = %s; want it to name allow_empty", raw)
	}
	postEmptySync(t, s.setupResult, key, "SEC", false, http.StatusUnprocessableEntity)
	for _, id := range ids {
		if m := getPolicyMissing(t, s.setupResult, id).SyncMissingSince; m != nil {
			t.Errorf("refused empty push marked %s missing (%v)", id, m)
		}
	}
	if items := sourceMissingItems(t, s.setupResult); len(items) != 0 {
		t.Errorf("source_missing items after a refused push = %+v", items)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies = %d, want %d", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != audit {
		t.Errorf("synced audit rows = %d, want %d", n, audit)
	}

	resp := postEmptySync(t, s.setupResult, key, "SEC", true, http.StatusOK)
	if resp.Missing != 2 || len(resp.Results) != 2 {
		t.Fatalf("allow_empty sync = %+v; want both pages missing", resp)
	}
	for _, id := range ids {
		if m := getPolicyMissing(t, s.setupResult, id).SyncMissingSince; m == nil {
			t.Errorf("allow_empty push left %s unmarked", id)
		}
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "2 of this source's 2") {
		t.Errorf("warnings = %q; want one naming 2 of 2 missing", resp.Warnings)
	}
}

// An empty push for a source with no published synced policies has
// nothing to mark and is accepted without allow_empty.
func TestSync_EmptyPushForEmptySourceAccepted(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	postSyncFrom(t, s.setupResult, key, "SEC", http.StatusOK, syncPage("2902-3", "Security Policy", "1.0", "Body.", nil))

	resp := postEmptySync(t, s.setupResult, key, "NEW", false, http.StatusOK)
	if resp.Missing != 0 || len(resp.Results) != 0 || len(resp.Warnings) != 0 {
		t.Errorf("empty sync of an unsynced space = %+v; want an empty report", resp)
	}
}

// A run that leaves more than half of the source's policies missing
// carries a warning; half or fewer does not.
func TestSync_WarnsWhenMostPoliciesMissing(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	a := syncPage("2902-a", "Policy A", "1.0", "A.", nil)
	b := syncPage("2902-b", "Policy B", "1.0", "B.", nil)
	c := syncPage("2902-c", "Policy C", "1.0", "C.", nil)
	d := syncPage("2902-d", "Policy D", "1.0", "D.", nil)
	postSync(t, s.setupResult, key, http.StatusOK, a, b, c, d)

	half := postSync(t, s.setupResult, key, http.StatusOK, a, b)
	if half.Missing != 2 || len(half.Warnings) != 0 {
		t.Errorf("2 of 4 missing = %+v; want no warning", half)
	}
	most := postSync(t, s.setupResult, key, http.StatusOK, a)
	if most.Missing != 3 || len(most.Warnings) != 1 || !strings.Contains(most.Warnings[0], "3 of this source's 4") {
		t.Errorf("3 of 4 missing = %+v; want one warning naming 3 of 4", most)
	}
}
