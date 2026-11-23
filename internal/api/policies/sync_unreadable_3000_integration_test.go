//go:build integration

// #3000 integration tests — POST /v1/policies:sync unreadable pages. A
// pusher that cannot read a page's body sends the page as unreadable with
// a reason instead of leaving it out. The page is present, so it is not
// marked missing, and its policy is unchanged: same body, hash, version
// and drift, no new row and no synced audit row. The policy gets a
// source_unreadable needs-attention item until a run carries the page
// readable. A page with no policy yet gets none. Real Postgres under RLS
// through the full HTTP stack, as sync_integration_test.go.

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

type syncResp3000 struct {
	Created    int `json:"created"`
	Unchanged  int `json:"unchanged"`
	Drift      int `json:"drift"`
	Skipped    int `json:"skipped"`
	Missing    int `json:"missing"`
	Unreadable int `json:"unreadable"`
	Results    []struct {
		PageID          string     `json:"page_id"`
		Title           string     `json:"title"`
		Status          string     `json:"status"`
		Reason          string     `json:"reason"`
		PolicyID        string     `json:"policy_id"`
		Version         string     `json:"version"`
		UnreadableSince *time.Time `json:"unreadable_since"`
		MissingSince    *time.Time `json:"missing_since"`
	} `json:"results"`
	Warnings []string `json:"warnings"`
}

func postSync3000(t *testing.T, s setupResult, key string, pages ...map[string]any) syncResp3000 {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"source": map[string]string{"site": "acme.atlassian.net", "space": "SEC"},
		"pages":  pages,
	})
	var out syncResp3000
	raw := s.do(t, http.MethodPost, "/v1/policies:sync", body, key, http.StatusOK)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode sync response: %v (%s)", err, raw)
	}
	return out
}

func unreadablePage(id, title, reason string) map[string]any {
	return map[string]any{"id": id, "title": title, "unreadable": true, "unreadable_reason": reason}
}

type policyUnreadableWire struct {
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	Version          string     `json:"version"`
	BodyMd           string     `json:"body_md"`
	SyncMissingSince *time.Time `json:"sync_missing_since"`
	SyncDrift        *struct {
		DetectedAt       time.Time `json:"detected_at"`
		SourceBodySHA256 string    `json:"source_body_sha256"`
	} `json:"sync_drift"`
	SyncUnreadable *struct {
		Since  time.Time `json:"since"`
		Reason string    `json:"reason"`
	} `json:"sync_unreadable"`
}

func getPolicyUnreadable(t *testing.T, s setupResult, id string) policyUnreadableWire {
	t.Helper()
	var env struct {
		Policy policyUnreadableWire `json:"policy"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &env); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	return env.Policy
}

// sourceUnreadableItems returns the source_unreadable items the admin's
// everyone view of GET /v1/needs-attention lists, by policy id.
func sourceUnreadableItems(t *testing.T, s setupResult) map[string]needsAttentionItem2853 {
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
		if it.Band == "source_unreadable" {
			out[it.ID] = it
		}
	}
	if na.Counts["source_unreadable"] != len(out) {
		t.Errorf("counts[source_unreadable] = %d, items = %d", na.Counts["source_unreadable"], len(out))
	}
	return out
}

// rowState is what an unreadable page must leave alone on its row.
type rowState struct {
	body, hash, driftHash, version, status string
	updatedAt                              time.Time
}

func readRowState(t *testing.T, s setupResult, id string) rowState {
	t.Helper()
	var r rowState
	if err := s.admin.QueryRow(context.Background(), `
		SELECT body_md, COALESCE(synced_body_sha256, ''), COALESCE(sync_drift_body_sha256, ''), version, status::text, updated_at
		FROM policies WHERE id = $1`, id).Scan(&r.body, &r.hash, &r.driftHash, &r.version, &r.status, &r.updatedAt); err != nil {
		t.Fatalf("read row %s: %v", id, err)
	}
	return r
}

// An unreadable page keeps its policy exactly as it was, drift included,
// is not missing, and gets a needs-attention item until a run carries it
// readable again. A page found missing and then carried unreadable is no
// longer missing.
func TestSync_UnreadablePageIsPresentAndUnchanged(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const body = "# Access Control\n\nEveryone uses MFA."
	props := map[string]any{"owner_role": "security_lead"}
	page := syncPage("7001", "Access Control Policy", "1.0", body, props)
	other := syncPage("7002", "Data Retention Policy", "1.0", "Keep logs.", nil)
	first := postSync3000(t, s.setupResult, key, page, other)
	if first.Created != 2 {
		t.Fatalf("first sync = %+v", first)
	}
	id, otherID := first.Results[0].PolicyID, first.Results[1].PolicyID

	// Drift first, so the unreadable run is seen to leave it alone.
	drifted := syncPage("7001", "Access Control Policy", "1.0", body+"\n\nEdited.", props)
	if r := postSync3000(t, s.setupResult, key, drifted, other); r.Drift != 1 {
		t.Fatalf("drift sync = %+v", r)
	}
	before := readRowState(t, s.setupResult, id)
	policies := countPolicies(t, s.setupResult, s.tenant)
	audit := len(syncedAuditRows(t, s.setupResult, s.tenant))

	const reason = "body not converted: storagemd: body nested too deeply: limit 512 <script>x</script>"
	resp := postSync3000(t, s.setupResult, key, unreadablePage("7001", "Access Control Policy", reason), other)
	if resp.Unreadable != 1 || resp.Missing != 0 || resp.Unchanged != 1 || resp.Created != 0 || len(resp.Results) != 2 || len(resp.Warnings) != 0 {
		t.Fatalf("unreadable sync = %+v", resp)
	}
	res := resp.Results[1]
	if res.Status != "unreadable" || res.PageID != "7001" || res.PolicyID != id || res.Version != "1.0" ||
		res.Reason != reason || res.UnreadableSince == nil {
		t.Fatalf("unreadable result = %+v", res)
	}
	since := *res.UnreadableSince

	after := readRowState(t, s.setupResult, id)
	if after.body != before.body || after.hash != before.hash || after.driftHash != before.driftHash ||
		after.version != before.version || after.status != "published" {
		t.Errorf("row after unreadable = %+v; want body, hash, drift and version as %+v", after, before)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies = %d, want %d", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != audit {
		t.Errorf("synced audit rows = %d, want %d", n, audit)
	}
	p := getPolicyUnreadable(t, s.setupResult, id)
	if p.SyncUnreadable == nil || p.SyncUnreadable.Reason != reason || !p.SyncUnreadable.Since.Equal(since) {
		t.Errorf("sync_unreadable = %+v, want %q since %v", p.SyncUnreadable, reason, since)
	}
	if p.SyncMissingSince != nil || p.SyncDrift == nil || p.BodyMd != body {
		t.Errorf("policy = %+v; want not missing, drift kept, body unchanged", p)
	}
	items := sourceUnreadableItems(t, s.setupResult)
	if it, ok := items[id]; !ok || len(items) != 1 || it.Type != "policy" || it.Since == nil || !it.Since.Equal(since) ||
		it.Owner == nil || it.Owner.Role != "security_lead" || it.Action == "" {
		t.Fatalf("source_unreadable items = %+v; want one for %s", items, id)
	}
	if m := sourceMissingItems(t, s.setupResult); len(m) != 0 {
		t.Errorf("source_missing items = %+v; want none", m)
	}

	// The same reason again writes nothing; a new reason keeps the time.
	mid := readRowState(t, s.setupResult, id)
	again := postSync3000(t, s.setupResult, key, unreadablePage("7001", "Access Control Policy", reason), other)
	if r := again.Results[1]; r.Status != "unreadable" || r.UnreadableSince == nil || !r.UnreadableSince.Equal(since) {
		t.Errorf("repeat unreadable result = %+v; want since %v", r, since)
	}
	if got := readRowState(t, s.setupResult, id).updatedAt; !got.Equal(mid.updatedAt) {
		t.Errorf("updated_at moved on a repeat: %v -> %v", mid.updatedAt, got)
	}
	postSync3000(t, s.setupResult, key, unreadablePage("7001", "Access Control Policy", "body too large"), other)
	p = getPolicyUnreadable(t, s.setupResult, id)
	if p.SyncUnreadable == nil || p.SyncUnreadable.Reason != "body too large" || !p.SyncUnreadable.Since.Equal(since) {
		t.Errorf("sync_unreadable after a new reason = %+v; want since %v", p.SyncUnreadable, since)
	}

	// Readable again: the state and the item clear; drift is as before.
	back := postSync3000(t, s.setupResult, key, drifted, other)
	if back.Unreadable != 0 || back.Drift != 1 {
		t.Fatalf("readable again = %+v", back)
	}
	if p := getPolicyUnreadable(t, s.setupResult, id); p.SyncUnreadable != nil {
		t.Errorf("sync_unreadable after the page was readable = %+v", p.SyncUnreadable)
	}
	if items := sourceUnreadableItems(t, s.setupResult); len(items) != 0 {
		t.Errorf("source_unreadable items after the page was readable = %+v", items)
	}

	// Missing, then unreadable: present, so no longer missing.
	if r := postSync3000(t, s.setupResult, key, drifted); r.Missing != 1 {
		t.Fatalf("sync without 7002 = %+v", r)
	}
	if p := getPolicyUnreadable(t, s.setupResult, otherID); p.SyncMissingSince == nil {
		t.Fatalf("7002 not missing")
	}
	r := postSync3000(t, s.setupResult, key, drifted, unreadablePage("7002", "", "body too large"))
	if r.Missing != 0 || r.Unreadable != 1 || r.Results[1].Title != "Data Retention Policy" {
		t.Fatalf("7002 unreadable = %+v", r)
	}
	if p := getPolicyUnreadable(t, s.setupResult, otherID); p.SyncMissingSince != nil || p.SyncUnreadable == nil {
		t.Errorf("7002 = %+v; want unreadable and not missing", p)
	}

	// Absent: missing, and no longer unreadable.
	postSync3000(t, s.setupResult, key, drifted)
	if p := getPolicyUnreadable(t, s.setupResult, otherID); p.SyncMissingSince == nil || p.SyncUnreadable != nil {
		t.Errorf("7002 absent = %+v; want missing and not unreadable", p)
	}
}

// A page with no policy yet arriving unreadable creates none, even when a
// published imported policy has its title. A push of only unreadable
// pages is not an empty push. An id carried both readable and unreadable
// syncs readable and skips the extra entry.
func TestSync_UnreadableNewPageAndDuplicates(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	first := postSync3000(t, s.setupResult, key, syncPage("7101", "Backup Policy", "1.0", "Back up nightly.", nil))
	if first.Created != 1 {
		t.Fatalf("first sync = %+v", first)
	}
	policies := countPolicies(t, s.setupResult, s.tenant)
	audit := len(syncedAuditRows(t, s.setupResult, s.tenant))

	resp := postSync3000(t, s.setupResult, key,
		unreadablePage("7101", "Backup Policy", "too deep"),
		unreadablePage("7102", "Incident Response Policy", "too deep"),
		unreadablePage("7102", "Incident Response Policy", "too deep"),
	)
	if resp.Unreadable != 2 || resp.Skipped != 1 || resp.Created != 0 || resp.Missing != 0 {
		t.Fatalf("unreadable-only sync = %+v", resp)
	}
	var fresh, dup bool
	for _, r := range resp.Results {
		switch {
		case r.PageID == "7102" && r.Status == "unreadable":
			fresh = r.PolicyID == "" && r.UnreadableSince == nil && strings.Contains(r.Reason, "too deep; no policy exists for this page yet")
		case r.PageID == "7102" && r.Status == "skipped":
			dup = strings.Contains(r.Reason, "more than once")
		}
	}
	if !fresh || !dup {
		t.Errorf("results = %+v; want 7102 unreadable with no policy, and its repeat skipped", resp.Results)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != policies {
		t.Errorf("policies = %d, want %d: an unreadable new page created one", n, policies)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != audit {
		t.Errorf("synced audit rows = %d, want %d", n, audit)
	}

	both := postSync3000(t, s.setupResult, key,
		syncPage("7101", "Backup Policy", "1.0", "Back up nightly.", nil),
		unreadablePage("7101", "Backup Policy", "too deep"),
	)
	if both.Unchanged != 1 || both.Skipped != 1 || both.Unreadable != 0 {
		t.Fatalf("readable and unreadable 7101 = %+v", both)
	}
	if p := getPolicyUnreadable(t, s.setupResult, first.Results[0].PolicyID); p.SyncUnreadable != nil {
		t.Errorf("7101 synced readable but still unreadable: %+v", p.SyncUnreadable)
	}
}

// The unreadable columns are allowed only on synced rows, only together,
// and hold a reason of 1 to 500 characters.
func TestSync_UnreadableColumnsCheck(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	synced := postSync3000(t, s.setupResult, key, syncPage("7201", "Backup Policy", "1.0", "Back up nightly.", nil)).Results[0].PolicyID
	authored := createDraftPolicy(t, s.setupResult, "Authored-3000", "1.0", nil)
	for name, c := range map[string]struct {
		id, set string
		args    []any
	}{
		"authored row":     {authored, `sync_unreadable_since = now(), sync_unreadable_reason = 'x'`, nil},
		"since, no reason": {synced, `sync_unreadable_since = now()`, nil},
		"reason, no since": {synced, `sync_unreadable_reason = 'x'`, nil},
		"empty reason":     {synced, `sync_unreadable_since = now(), sync_unreadable_reason = ''`, nil},
		"long reason":      {synced, `sync_unreadable_since = now(), sync_unreadable_reason = $3`, []any{strings.Repeat("é", 501)}},
	} {
		args := append([]any{s.tenant, c.id}, c.args...)
		if _, err := s.admin.Exec(t.Context(), `UPDATE policies SET `+c.set+` WHERE tenant_id = $1 AND id = $2`, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := s.admin.Exec(t.Context(), `UPDATE policies SET sync_unreadable_since = now(), sync_unreadable_reason = $3 WHERE tenant_id = $1 AND id = $2`,
		s.tenant, synced, strings.Repeat("é", 500)); err != nil {
		t.Errorf("500-character reason on a synced row refused: %v", err)
	}
}
