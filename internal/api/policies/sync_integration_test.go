//go:build integration

// #2850 integration tests — POST /v1/policies:sync. Real Postgres under
// RLS, through the full HTTP stack: the policy-sync gate, tenancy, the
// module gate and OPA with live roles. Prior art: import_integration_test.go.

package policies_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// syncSetup is the harness with the API-key store wired in, and a keys
// handle to mint keys in any tenant.
type syncSetup struct {
	setupResult
	keys *apikeystore.Store
}

func newSyncSetup(t *testing.T) syncSetup {
	t.Helper()
	hasher, err := bearer.NewHasher([]byte("test-bearer-hash-key-at-least-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	keys := apikeystore.NewStore(dbtest.NewAppPool(t), dbtest.NewMigratePool(t), hasher, 0)
	s := setupWith(t, true, func(srv *api.Server) { srv.AttachAPIKeyStore(keys) })
	return syncSetup{setupResult: s, keys: keys}
}

// issueKey mints a key in tenant and returns its id and bearer.
func (s syncSetup) issueKey(t *testing.T, tenant string, in apikeystore.IssueInput) (string, string) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("tenant ctx: %v", err)
	}
	cred, tok, err := s.keys.Issue(ctx, tenant, in, apikeystore.Actor{})
	if err != nil {
		t.Fatalf("issue key: %v", err)
	}
	return cred.ID, tok
}

func syncPage(id, title, version, body string, props map[string]any) map[string]any {
	p := map[string]any{
		"version":          version,
		"approved_by_name": "Jane Doe",
		"approved_at":      "2025-03-01",
		"effective_date":   "2025-03-15",
	}
	for k, v := range props {
		if v == nil {
			delete(p, k)
			continue
		}
		p[k] = v
	}
	return map[string]any{
		"id":         id,
		"title":      title,
		"url":        "https://acme.atlassian.net/wiki/spaces/SEC/pages/" + id,
		"body_md":    body,
		"properties": p,
	}
}

type syncResp struct {
	Created    int `json:"created"`
	NewVersion int `json:"new_version"`
	Unchanged  int `json:"unchanged"`
	Drift      int `json:"drift"`
	Skipped    int `json:"skipped"`
	Results    []struct {
		PageID          string     `json:"page_id"`
		Title           string     `json:"title"`
		Status          string     `json:"status"`
		Reason          string     `json:"reason"`
		PolicyID        string     `json:"policy_id"`
		Version         string     `json:"version"`
		PredecessorID   string     `json:"predecessor_id"`
		DriftDetectedAt *time.Time `json:"drift_detected_at"`
	} `json:"results"`
}

func postSync(t *testing.T, s setupResult, key string, want int, pages ...map[string]any) syncResp {
	t.Helper()
	if pages == nil {
		pages = []map[string]any{}
	}
	body, _ := json.Marshal(map[string]any{
		"source": map[string]string{"site": "acme.atlassian.net", "space": "SEC"},
		"pages":  pages,
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

type syncedAuditRow struct {
	actor, sourceFile, title, version, hash, sourceURL, approver string
}

func syncedAuditRows(t *testing.T, s setupResult, tenant string) []syncedAuditRow {
	t.Helper()
	rows, err := s.admin.Query(context.Background(), `
		SELECT actor, source_file, policy_title, policy_version, body_sha256, source_url, historical_approver_name
		FROM policy_audit_log WHERE tenant_id = $1 AND action = 'synced' ORDER BY created_at, id`, tenant)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []syncedAuditRow
	for rows.Next() {
		var r syncedAuditRow
		if err := rows.Scan(&r.actor, &r.sourceFile, &r.title, &r.version, &r.hash, &r.sourceURL, &r.approver); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func countPolicies(t *testing.T, s setupResult, tenant string) int {
	t.Helper()
	var n int
	if err := s.admin.QueryRow(context.Background(), `SELECT count(*) FROM policies WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// policySyncedWire is the part of the GET /v1/policies/{id} policy the
// sync tests read.
type policySyncedWire struct {
	Title                  string `json:"title"`
	Version                string `json:"version"`
	BodyMd                 string `json:"body_md"`
	Status                 string `json:"status"`
	SourceAttribution      string `json:"source_attribution"`
	OwnerRole              string `json:"owner_role"`
	ApproverRole           string `json:"approver_role"`
	HistoricalApproverName string `json:"historical_approver_name"`
	HistoricalApprovedAt   string `json:"historical_approved_at"`
	EffectiveDate          string `json:"effective_date"`
	NextReviewAt           string `json:"next_review_at"`
	SourceURL              string `json:"source_url"`
}

func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// A new page becomes a published synced policy with its historical
// approver, source URL and body hash, and one 'synced' audit row naming
// the key. Re-pushing it unchanged writes nothing.
func TestSync_CreatesThenUnchanged(t *testing.T) {
	s := newSyncSetup(t)
	keyID, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	const body = "# Access Control\n\nEveryone uses MFA."
	page := syncPage("1001", "Access Control Policy", "3.2", body, map[string]any{
		"next_review_at": "2026-03-01",
		"owner_role":     "security_lead",
	})

	resp := postSync(t, s.setupResult, key, http.StatusOK, page)
	if resp.Created != 1 || resp.Unchanged != 0 || resp.Skipped != 0 || len(resp.Results) != 1 {
		t.Fatalf("first sync = %+v", resp)
	}
	res := resp.Results[0]
	if res.PageID != "1001" || res.Status != "created" || res.PolicyID == "" || res.Version != "3.2" {
		t.Fatalf("result = %+v", res)
	}

	raw := s.do(t, http.MethodGet, "/v1/policies/"+res.PolicyID, nil, s.adminBear, http.StatusOK)
	var env struct {
		Policy policySyncedWire `json:"policy"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	p := env.Policy
	if p.Title != "Access Control Policy" || p.Version != "3.2" || p.BodyMd != body ||
		p.Status != "published" || p.SourceAttribution != "synced" ||
		p.OwnerRole != "security_lead" || p.ApproverRole != "unassigned" ||
		p.HistoricalApproverName != "Jane Doe" || p.HistoricalApprovedAt != "2025-03-01" ||
		p.EffectiveDate != "2025-03-15" ||
		p.SourceURL != "https://acme.atlassian.net/wiki/spaces/SEC/pages/1001" {
		t.Errorf("synced policy = %+v", p)
	}
	if nr, err := time.Parse(time.RFC3339, p.NextReviewAt); err != nil || !nr.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("next_review_at = %q", p.NextReviewAt)
	}

	var hash, syncedBy, site, space, pageID string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT synced_body_sha256, synced_by, sync_site, sync_space, sync_page_id FROM policies WHERE id = $1`,
		res.PolicyID).Scan(&hash, &syncedBy, &site, &space, &pageID); err != nil {
		t.Fatalf("read sync columns: %v", err)
	}
	if hash != sha(body) || syncedBy != keyID || site != "acme.atlassian.net" || space != "SEC" || pageID != "1001" {
		t.Errorf("sync columns = %s %s %s %s %s; want hash %s, key %s", hash, syncedBy, site, space, pageID, sha(body), keyID)
	}

	audit := syncedAuditRows(t, s.setupResult, s.tenant)
	want := syncedAuditRow{
		actor: keyID, sourceFile: "1001", title: "Access Control Policy", version: "3.2",
		hash: sha(body), sourceURL: "https://acme.atlassian.net/wiki/spaces/SEC/pages/1001", approver: "Jane Doe",
	}
	if len(audit) != 1 || audit[0] != want {
		t.Fatalf("synced audit rows = %+v; want [%+v]", audit, want)
	}

	// An unchanged re-push is a no-op.
	before := countPolicies(t, s.setupResult, s.tenant)
	again := postSync(t, s.setupResult, key, http.StatusOK, page)
	if again.Created != 0 || again.Unchanged != 1 || len(again.Results) != 1 ||
		again.Results[0].Status != "unchanged" || again.Results[0].PolicyID != res.PolicyID {
		t.Fatalf("re-push = %+v", again)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != before {
		t.Errorf("policies after re-push = %d, want %d", n, before)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != 1 {
		t.Errorf("synced audit rows after re-push = %d, want 1", n)
	}
}

// A page with a missing or invalid property is reported and not synced;
// the valid pages beside it are.
func TestSync_InvalidPropertiesSkipped(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})

	resp := postSync(t, s.setupResult, key, http.StatusOK,
		syncPage("2001", "Valid Policy", "1.0", "Body.", map[string]any{"acknowledgment_required_roles": []string{"everyone"}}),
		syncPage("2002", "No Approval Date", "1.0", "Body.", map[string]any{"approved_at": nil}),
		syncPage("2003", "Bad Date", "1.0", "Body.", map[string]any{"effective_date": "15/03/2025"}),
		syncPage("2004", "Free-text Audience", "1.0", "Body.", map[string]any{"acknowledgment_required_roles": []string{"employee"}}),
		syncPage("2005", "Unknown Property", "1.0", "Body.", map[string]any{"review_owner": "1.0"}),
		syncPage("2006", "Empty Body", "1.0", "  ", nil),
		syncPage("2001", "Valid Policy Again", "1.0", "Body.", nil),
	)
	want := map[string]string{
		"2002": `missing required page property "approved_at"`,
		"2003": `effective_date "15/03/2025" is not a date (want YYYY-MM-DD)`,
		"2004": `acknowledgment_required_roles: unrecognised audience "employee"`,
		"2005": `page properties: json: unknown field "review_owner"`,
		"2006": "page body is empty",
	}
	if resp.Created != 1 || resp.Skipped != 6 {
		t.Fatalf("resp = %+v", resp)
	}
	created, dupSkipped := 0, false
	for _, r := range resp.Results {
		switch {
		case r.Status == "created":
			created++
			if r.PageID != "2001" {
				t.Errorf("created %s, want only 2001", r.PageID)
			}
		case r.PageID == "2001":
			dupSkipped = r.Status == "skipped"
		default:
			w, ok := want[r.PageID]
			if !ok || r.Status != "skipped" || len(r.Reason) < len(w) || r.Reason[:len(w)] != w {
				t.Errorf("%s: %s %q; want skipped starting %q", r.PageID, r.Status, r.Reason, w)
			}
		}
	}
	if created != 1 || !dupSkipped {
		t.Errorf("created %d (want 1); repeated page id skipped = %v", created, dupSkipped)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != 1 {
		t.Errorf("policies = %d, want 1", n)
	}
	if n := len(syncedAuditRows(t, s.setupResult, s.tenant)); n != 1 {
		t.Errorf("synced audit rows = %d, want 1", n)
	}
}

// Only a policy-sync key is accepted. An admin key, an ordinary key, a
// user session (bearer or cookie) and an unknown key are refused, and none
// writes a policy.
func TestSync_OnlyPolicySyncKeys(t *testing.T) {
	s := newSyncSetup(t)
	_, adminKey := s.issueKey(t, s.tenant, apikeystore.IssueInput{IsAdmin: true})
	_, ordinaryKey := s.issueKey(t, s.tenant, apikeystore.IssueInput{OwnerRoles: []string{"grc_engineer"}})
	page := syncPage("3001", "Refused Policy", "1.0", "Body.", nil)

	for name, c := range map[string]struct {
		bearer string
		want   int
	}{
		"admin API key":    {adminKey, http.StatusForbidden},
		"ordinary API key": {ordinaryKey, http.StatusForbidden},
		"admin session":    {s.adminBear, http.StatusForbidden},
		"unknown API key":  {bearer.PrefixProd + "0123456789abcdef0123456789abcdef", http.StatusUnauthorized},
		"no credential":    {"", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			postSync(t, s.setupResult, c.bearer, c.want, page)
		})
	}

	// A session cookie does not stand in for the key.
	body, _ := json.Marshal(map[string]any{
		"source": map[string]string{"site": "acme.atlassian.net", "space": "SEC"},
		"pages":  []map[string]any{page},
	})
	req, _ := http.NewRequest(http.MethodPost, s.server.URL+"/v1/policies:sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: jwtmw.DefaultCookieName, Value: s.adminBear})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cookie request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("session cookie: status %d, want 403", resp.StatusCode)
	}

	if n := countPolicies(t, s.setupResult, s.tenant); n != 0 {
		t.Errorf("policies after refused syncs = %d, want 0", n)
	}
}

// Each tenant's sync sees only its own synced policies: the same source
// and page id in another tenant is a new page there, and neither tenant
// can read the other's policy.
func TestSync_TenantIsolation(t *testing.T) {
	s := newSyncSetup(t)
	tenantB := freshTenant(t, s.admin)
	_, keyA := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, keyB := s.issueKey(t, tenantB, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	page := syncPage("4001", "Shared Page Policy", "1.0", "Body.", nil)

	a := postSync(t, s.setupResult, keyA, http.StatusOK, page)
	b := postSync(t, s.setupResult, keyB, http.StatusOK, page)
	if a.Created != 1 || b.Created != 1 {
		t.Fatalf("tenant A %+v, tenant B %+v: want one created in each", a, b)
	}
	if a.Results[0].PolicyID == b.Results[0].PolicyID {
		t.Fatalf("both tenants got policy %s", a.Results[0].PolicyID)
	}
	if n := countPolicies(t, s.setupResult, s.tenant); n != 1 {
		t.Errorf("tenant A policies = %d, want 1", n)
	}
	if n := countPolicies(t, s.setupResult, tenantB); n != 1 {
		t.Errorf("tenant B policies = %d, want 1", n)
	}
	// Tenant A's admin session cannot read tenant B's synced policy.
	s.do(t, http.MethodGet, "/v1/policies/"+b.Results[0].PolicyID, nil, s.adminBear, http.StatusNotFound)
	// Tenant B re-pushing sees its own row as unchanged, never A's.
	again := postSync(t, s.setupResult, keyB, http.StatusOK, page)
	if again.Unchanged != 1 || again.Results[0].PolicyID != b.Results[0].PolicyID {
		t.Errorf("tenant B re-push = %+v; want unchanged %s", again, b.Results[0].PolicyID)
	}
	if n := len(syncedAuditRows(t, s.setupResult, tenantB)); n != 1 {
		t.Errorf("tenant B synced audit rows = %d, want 1", n)
	}
}

// A synced policy appears where any published policy does: the list, the
// calendar (by its next review), needs-attention and acknowledgement
// targeting.
func TestSync_AppearsLikeAnyPublishedPolicy(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})
	_, engineer := s.issueRoledUserBearer(t, "engineer-2850@sync.test", []string{"grc_engineer"})
	review := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")
	resp := postSync(t, s.setupResult, key, http.StatusOK,
		syncPage("5001", "Engineering Policy", "1.0", "Body.", map[string]any{
			"next_review_at":                review,
			"acknowledgment_required_roles": []string{"grc_engineer"},
		}))
	if resp.Created != 1 {
		t.Fatalf("sync = %+v", resp)
	}
	id := resp.Results[0].PolicyID

	var list struct {
		Policies []struct {
			ID string `json:"id"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies", nil, s.adminBear, http.StatusOK), &list); err != nil {
		t.Fatal(err)
	}
	if !containsID(len(list.Policies), func(i int) string { return list.Policies[i].ID }, id) {
		t.Errorf("GET /v1/policies does not list %s", id)
	}

	var cal struct {
		Events []struct {
			Type            string `json:"type"`
			RelatedEntityID string `json:"related_entity_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/calendar?types=policy", nil, s.adminBear, http.StatusOK), &cal); err != nil {
		t.Fatal(err)
	}
	if !containsID(len(cal.Events), func(i int) string { return cal.Events[i].RelatedEntityID }, id) {
		t.Errorf("GET /v1/calendar does not show the review of %s", id)
	}

	var mine struct {
		Pending []struct {
			PolicyID string `json:"policy_id"`
		} `json:"pending"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/me/acknowledgments", nil, engineer, http.StatusOK), &mine); err != nil {
		t.Fatal(err)
	}
	if !containsID(len(mine.Pending), func(i int) string { return mine.Pending[i].PolicyID }, id) {
		t.Errorf("GET /v1/me/acknowledgments does not ask the engineer to acknowledge %s", id)
	}

	var na struct {
		Items []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/needs-attention?for=me&limit=100&type=policy_ack", nil, engineer, http.StatusOK), &na); err != nil {
		t.Fatal(err)
	}
	if !containsID(len(na.Items), func(i int) string { return na.Items[i].ID }, id) {
		t.Errorf("GET /v1/needs-attention does not list the acknowledgement of %s: %+v", id, na.Items)
	}
}

func containsID(n int, at func(int) string, id string) bool {
	for i := 0; i < n; i++ {
		if at(i) == id {
			return true
		}
	}
	return false
}
