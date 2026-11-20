//go:build integration

package policies_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestImportHTTP2586_AudienceEnum pins the audience enum on the admin
// import: a file naming a free-text audience fails with every unrecognised
// value in its reason, and nothing is written without partial. A file whose
// audience is a role or everyone imports, and under partial it commits
// while the bad file is reported.
func TestImportHTTP2586_AudienceEnum(t *testing.T) {
	s := setup(t)
	legacy := policyImportFile("Legacy Audience", "1",
		"acknowledgment_required_roles: [employee, viewer, contractor, employee]\n")
	good := policyImportFile("Everyone Audience", "1",
		"acknowledgment_required_roles: [everyone, auditor]\n")

	resp := postImport(t, s, s.adminBear, false, http.StatusUnprocessableEntity, good, legacy)
	if resp.Committed || resp.Failed != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	var reason string
	for _, r := range resp.Results {
		if r.File == legacy["name"] {
			if r.Status != "failed" {
				t.Errorf("legacy file status = %q; want failed", r.Status)
			}
			reason = r.Reason
		}
	}
	for _, v := range []string{`"employee"`, `"contractor"`} {
		if !strings.Contains(reason, v) {
			t.Errorf("reason %q does not name %s", reason, v)
		}
	}
	if strings.Contains(reason, `"viewer"`) || strings.Count(reason, `"employee"`) != 1 {
		t.Errorf("reason %q should name each unrecognised value once and no valid one", reason)
	}
	if n := tenantPolicyCount(t, s); n != 0 {
		t.Fatalf("import wrote %d policies despite a refused audience", n)
	}

	resp = postImport(t, s, s.adminBear, true, http.StatusOK, good, legacy)
	if !resp.Committed || resp.Imported != 1 || resp.Failed != 1 {
		t.Fatalf("partial resp = %+v", resp)
	}
	var roles []string
	if err := s.admin.QueryRow(context.Background(),
		`SELECT acknowledgment_required_roles FROM policies WHERE tenant_id = $1`, s.tenant).Scan(&roles); err != nil {
		t.Fatalf("read imported audience: %v", err)
	}
	if strings.Join(roles, ",") != "everyone,auditor" {
		t.Errorf("imported audience = %v; want [everyone auditor]", roles)
	}
}
