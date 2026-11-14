//go:build integration

// #1747 integration tests — POST /v1/admin/policies/import, the next
// version of an imported policy through the normal workflow, and the
// import fields on the list, detail and CSV export surfaces. Real
// Postgres under RLS, driven through the full HTTP stack (JWT, tenancy,
// authz middleware).

package policies_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
)

func policyImportFile(title, version, extra string) map[string]string {
	return map[string]string{
		"name": strings.ToLower(strings.ReplaceAll(title, " ", "-")) + ".md",
		"content": "---\n" +
			"title: " + title + "\n" +
			"version: \"" + version + "\"\n" +
			"approved_by_name: Jane Doe\n" +
			"approved_at: 2024-02-01\n" +
			"effective_date: 2024-02-15\n" +
			"next_review_at: 2025-02-01\n" +
			"source_url: https://wiki.example.com/" + version + "\n" +
			extra +
			"---\n# " + title + "\n\nPolicy text.\n",
	}
}

type importResp struct {
	Committed bool `json:"committed"`
	Imported  int  `json:"imported"`
	Skipped   int  `json:"skipped"`
	Failed    int  `json:"failed"`
	Results   []struct {
		File   string `json:"file"`
		Status string `json:"status"`
		Reason string `json:"reason"`
		Policy *struct {
			ID                     string   `json:"id"`
			Status                 string   `json:"status"`
			SourceAttribution      string   `json:"source_attribution"`
			Imported               bool     `json:"imported"`
			HistoricalApproverName string   `json:"historical_approver_name"`
			HistoricalApprovedAt   string   `json:"historical_approved_at"`
			EffectiveDate          string   `json:"effective_date"`
			NextReviewAt           string   `json:"next_review_at"`
			SourceURL              string   `json:"source_url"`
			ImportedBy             string   `json:"imported_by"`
			Warnings               []string `json:"warnings"`
		} `json:"policy"`
	} `json:"results"`
}

func postImport(t *testing.T, s setupResult, bearer string, partial bool, want int, files ...map[string]string) importResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"files": files, "partial": partial})
	raw := s.do(t, http.MethodPost, "/v1/admin/policies/import", body, bearer, want)
	var out importResp
	if want == http.StatusOK || want == http.StatusUnprocessableEntity {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode import response: %v (%s)", err, raw)
		}
	}
	return out
}

func tenantPolicyCount(t *testing.T, s setupResult) int {
	t.Helper()
	var n int
	if err := s.admin.QueryRow(context.Background(), `SELECT count(*) FROM policies WHERE tenant_id = $1`, s.tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestImportHTTP_AdminImportsTwoFiles(t *testing.T) {
	s := setup(t)
	resp := postImport(t, s, s.adminBear, false, http.StatusOK,
		policyImportFile("Access Control Policy", "2.0", "linked_controls: ["+s.ctrlID.String()+"]\n"),
		policyImportFile("Vendor Policy", "1.1", ""),
	)
	if !resp.Committed || resp.Imported != 2 || resp.Failed != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	for _, r := range resp.Results {
		p := r.Policy
		if p == nil {
			t.Fatalf("%s: no policy in result (%s %s)", r.File, r.Status, r.Reason)
		}
		if p.Status != "published" || p.SourceAttribution != "imported" || !p.Imported {
			t.Errorf("%s: status=%s source=%s imported=%v", r.File, p.Status, p.SourceAttribution, p.Imported)
		}
		nextReview, err := time.Parse(time.RFC3339, p.NextReviewAt)
		if err != nil || !nextReview.Equal(time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("%s: next_review_at = %q", r.File, p.NextReviewAt)
		}
		if p.HistoricalApproverName != "Jane Doe" || p.HistoricalApprovedAt != "2024-02-01" ||
			p.EffectiveDate != "2024-02-15" || !strings.HasPrefix(p.SourceURL, "https://wiki.example.com/") ||
			p.ImportedBy != s.adminUser.String() {
			t.Errorf("%s: import fields = %+v", r.File, p)
		}
	}

	// The list flags the imported policy that has no linked controls.
	raw := s.do(t, http.MethodGet, "/v1/policies?include=ack_rate", nil, s.adminBear, http.StatusOK)
	var list struct {
		Policies []struct {
			Title    string   `json:"title"`
			Imported bool     `json:"imported"`
			Warnings []string `json:"warnings"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	flagged := map[string]bool{}
	for _, p := range list.Policies {
		if !p.Imported {
			t.Errorf("%s: imported=false in list", p.Title)
		}
		for _, w := range p.Warnings {
			if w == "orphan_policy" {
				flagged[p.Title] = true
			}
		}
	}
	if !flagged["Vendor Policy"] || flagged["Access Control Policy"] {
		t.Errorf("orphan flags = %v; want only Vendor Policy", flagged)
	}

	// CSV export carries the import fields.
	raw = s.do(t, http.MethodGet, "/v1/admin/policies/export?format=csv", nil, s.adminBear, http.StatusOK)
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for _, h := range []string{"source_attribution", "imported", "historical_approver_name", "historical_approved_at", "source_url", "imported_by", "imported_at"} {
		if _, ok := col[h]; !ok {
			t.Fatalf("CSV header missing %q: %v", h, rows[0])
		}
	}
	if len(rows) != 3 {
		t.Fatalf("CSV rows = %d, want header + 2", len(rows))
	}
	for _, row := range rows[1:] {
		if row[col["imported"]] != "true" || row[col["historical_approver_name"]] != "Jane Doe" ||
			row[col["historical_approved_at"]] != "2024-02-01" || row[col["imported_at"]] == "" ||
			!strings.HasPrefix(row[col["source_url"]], "https://wiki.example.com/") {
			t.Errorf("CSV row = %v", row)
		}
	}
}

func TestImportHTTP_NonAdminForbidden(t *testing.T) {
	s := setup(t)
	tenant := uuid.MustParse(s.tenant)
	// testjwt.ApproverFor is admin-equivalent at the JWT layer, so the
	// non-admin cases are a GRC engineer (may author policies) and a
	// viewer.
	for name, claims := range map[string]jwt.AtlasClaims{
		"grc_engineer": testjwt.OwnerFor(tenant, []string{"grc_engineer"}),
		"viewer":       testjwt.ViewerFor(tenant),
	} {
		bearer := s.apiServer.IssueTestJWT(t, claims)
		postImport(t, s, bearer, false, http.StatusForbidden, policyImportFile("Nope "+name, "1", ""))
	}
	if n := tenantPolicyCount(t, s); n != 0 {
		t.Fatalf("non-admin import wrote %d policies", n)
	}
}

func TestImportHTTP_MissingApprovedAt(t *testing.T) {
	s := setup(t)
	bad := policyImportFile("Broken", "1", "")
	bad["content"] = strings.Replace(bad["content"], "approved_at: 2024-02-01\n", "", 1)
	good := policyImportFile("Good", "1", "")

	resp := postImport(t, s, s.adminBear, false, http.StatusUnprocessableEntity, good, bad)
	if resp.Committed || resp.Failed != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	for _, r := range resp.Results {
		if r.File == bad["name"] && (r.Status != "failed" || !strings.Contains(r.Reason, "approved_at")) {
			t.Errorf("bad file = %+v", r)
		}
	}
	if n := tenantPolicyCount(t, s); n != 0 {
		t.Fatalf("default import wrote %d policies despite a failure", n)
	}

	resp = postImport(t, s, s.adminBear, true, http.StatusOK, good, bad)
	if !resp.Committed || resp.Imported != 1 || resp.Failed != 1 {
		t.Fatalf("partial resp = %+v", resp)
	}
	if n := tenantPolicyCount(t, s); n != 1 {
		t.Fatalf("partial import wrote %d policies, want 1", n)
	}

	// Re-importing the same file skips it and reports why.
	resp = postImport(t, s, s.adminBear, false, http.StatusOK, good)
	if resp.Skipped != 1 || resp.Results[0].Status != "skipped" || resp.Results[0].Reason == "" {
		t.Fatalf("re-import resp = %+v", resp)
	}
}

func TestImportHTTP_NextVersionThroughWorkflow(t *testing.T) {
	s := setup(t)
	resp := postImport(t, s, s.adminBear, false, http.StatusOK, policyImportFile("Chain Policy", "1.0", ""))
	importedID := resp.Results[0].Policy.ID

	body, _ := json.Marshal(map[string]any{
		"title":              "Chain Policy",
		"version":            "2.0",
		"body_md":            "# Chain Policy v2",
		"owner_role":         "tenant_admin",
		"approver_role":      "security_lead",
		"linked_control_ids": []string{s.ctrlID.String()},
		"predecessor_id":     importedID,
	})
	raw := s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusCreated)
	var created struct {
		Policy struct {
			ID            string `json:"id"`
			Status        string `json:"status"`
			PredecessorID string `json:"predecessor_id"`
			Imported      bool   `json:"imported"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.Policy.Status != "draft" || created.Policy.PredecessorID != importedID || created.Policy.Imported {
		t.Fatalf("draft = %+v", created.Policy)
	}
	id := created.Policy.ID
	pub, _ := json.Marshal(map[string]any{"new_version": "2.0"})
	s.do(t, http.MethodPost, "/v1/policies/"+id+"/publish", pub, s.adminBear, http.StatusConflict)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/submit", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPatch, "/v1/policies/"+id+"/approve", nil, s.adminBear, http.StatusOK)
	s.do(t, http.MethodPost, "/v1/policies/"+id+"/publish", pub, s.adminBear, http.StatusCreated)

	raw = s.do(t, http.MethodGet, "/v1/policies/"+importedID, nil, s.adminBear, http.StatusOK)
	var prior struct {
		Policy struct {
			Status   string `json:"status"`
			Imported bool   `json:"imported"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	if prior.Policy.Status != "superseded" || !prior.Policy.Imported {
		t.Fatalf("imported predecessor = %+v", prior.Policy)
	}

	// A second successor of the same predecessor is a conflict.
	s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusConflict)
	// source_attribution=imported is not accepted on create.
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	delete(m, "predecessor_id")
	m["source_attribution"] = "imported"
	body, _ = json.Marshal(m)
	s.do(t, http.MethodPost, "/v1/policies", body, s.adminBear, http.StatusBadRequest)
}
