//go:build integration

// #2837 integration tests — a synced policy names the system it comes
// from. A page pushed from a SharePoint site reads sync_source
// "SharePoint", and an in-tool edit of it is refused "managed in
// SharePoint"; a Confluence page still reads "Confluence". Real Postgres
// under RLS through the full HTTP stack, with sync_integration_test.go's
// helpers.

package policies_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
)

func TestSyncSource_NamesSharePoint(t *testing.T) {
	s := newSyncSetup(t)
	_, key := s.issueKey(t, s.tenant, apikeystore.IssueInput{Purpose: credstore.PurposePolicySync})

	page := syncPage("12", "Access Control Policy", "1.0", "Body.", nil)
	page["url"] = "https://contoso.sharepoint.com/sites/Policies/Policies/Access%20Control%20Policy.docx"
	body, _ := json.Marshal(map[string]any{
		"source": map[string]string{"site": "https://contoso.sharepoint.com/sites/Policies", "space": "Policies"},
		"pages":  []map[string]any{page},
	})
	var rep syncResp
	if err := json.Unmarshal(s.do(t, http.MethodPost, "/v1/policies:sync", body, key, http.StatusOK), &rep); err != nil {
		t.Fatalf("decode sync: %v", err)
	}
	if rep.Created != 1 {
		t.Fatalf("sync = %+v; want one created", rep)
	}
	spID := rep.Results[0].PolicyID
	cfID := postSync(t, s.setupResult, key, http.StatusOK, syncPage("13", "Confluence Policy", "1.0", "Body.", nil)).Results[0].PolicyID

	for id, want := range map[string]string{spID: "SharePoint", cfID: "Confluence"} {
		var env struct {
			Policy struct {
				SyncSource string `json:"sync_source"`
			} `json:"policy"`
		}
		if err := json.Unmarshal(s.do(t, http.MethodGet, "/v1/policies/"+id, nil, s.adminBear, http.StatusOK), &env); err != nil {
			t.Fatalf("decode policy: %v", err)
		}
		if env.Policy.SyncSource != want {
			t.Errorf("policy %s sync_source = %q, want %q", id, env.Policy.SyncSource, want)
		}
	}

	// The store's refusal (review date) and the handler's own (draft
	// edit) both name SharePoint.
	for _, req := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodPatch, "/v1/policies/" + spID + "/review-date", []byte(`{"next_review_at":"2027-01-01"}`)},
		{http.MethodPatch, "/v1/policies/" + spID, draftJSON(t, s.ctrlID, nil)},
		{http.MethodPost, "/v1/policies", draftJSON(t, s.ctrlID, map[string]any{"predecessor_id": spID})},
	} {
		raw := s.do(t, req.method, req.path, req.body, s.adminBear, http.StatusConflict)
		if !strings.Contains(string(raw), "managed in SharePoint") {
			t.Errorf("%s %s = %s; want a managed-in-SharePoint refusal", req.method, req.path, raw)
		}
	}
}
