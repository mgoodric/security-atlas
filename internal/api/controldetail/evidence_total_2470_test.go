package controldetail

// #2470: the per-control evidence list's `total` is the count of the
// request's own control and window, read with the same evidencePage as the
// rows, and a period-scoped caller resolves the control by control_id only.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// pageRecorder2470 records the evidencePage each per-control read got.
type pageRecorder2470 struct {
	stubEvidenceReader
	listPage, countPage *evidencePage
	tenantCounted       *bool
}

func (s pageRecorder2470) EvidenceForControl(_ context.Context, _ uuid.UUID, p evidencePage) ([]dbx.ListEvidenceForControlPagedRow, error) {
	*s.listPage = p
	return nil, nil
}

func (s pageRecorder2470) CountEvidenceForControl(_ context.Context, _ uuid.UUID, p evidencePage) (int64, error) {
	*s.countPage = p
	return 4, nil
}

func (s pageRecorder2470) CountEvidenceForTenant(_ context.Context) (int64, error) {
	*s.tenantCounted = true
	return 999, nil
}

func TestEvidence2470_PerControlTotalAndResolution(t *testing.T) {
	jwt := func(roles ...string) credstore.Credential {
		return credstore.Credential{ID: "jwt:2470", TenantID: contractTenantID, OwnerRoles: roles, TenantRoles: roles}
	}
	cases := []struct {
		name   string
		cred   credstore.Credential
		idOnly bool
	}{
		{"admin flag", credstore.Credential{ID: "key_2470", TenantID: contractTenantID, IsAdmin: true}, false},
		{"approver flag", credstore.Credential{ID: "key_2470", TenantID: contractTenantID, IsApprover: true}, false},
		{"live tenant admin", jwt("admin"), false},
		{"live grc_engineer", jwt("grc_engineer"), false},
		{"live control_owner", jwt("control_owner"), false},
		{"api key control_owner label", credstore.Credential{ID: "key_2470", TenantID: contractTenantID, OwnerRoles: []string{"control_owner"}}, false},
		{"auditor", jwt("auditor"), true},
		{"auditor with a stale grc_engineer claim", credstore.Credential{ID: "jwt:2470", TenantID: contractTenantID, OwnerRoles: []string{"grc_engineer", "auditor"}, TenantRoles: []string{"auditor"}}, true},
		{"unknown role", jwt("viewer"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var listPage, countPage evidencePage
			var tenantCounted bool
			h := newHandlerWithEvidenceReader(pageRecorder2470{listPage: &listPage, countPage: &countPage, tenantCounted: &tenantCounted})
			target := "/v1/evidence?control_id=" + uuid.NewString() +
				"&since=2026-01-01T00:00:00Z&until=2026-03-31T23:59:59Z"
			req := httptest.NewRequest(http.MethodGet, target, nil)
			ctx, err := tenancy.WithTenant(authctx.WithCredential(req.Context(), tc.cred), contractTenantID)
			if err != nil {
				t.Fatalf("with tenant: %v", err)
			}
			rec := httptest.NewRecorder()
			h.Evidence(rec, req.WithContext(ctx))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Total int64 `json:"total"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Total != 4 || tenantCounted {
				t.Fatalf("total = %d (tenant count read: %v); want the control-window count 4", body.Total, tenantCounted)
			}
			if listPage.idOnly != tc.idOnly {
				t.Errorf("list idOnly = %v; want %v", listPage.idOnly, tc.idOnly)
			}
			if countPage.since != listPage.since || countPage.until != listPage.until || countPage.idOnly != listPage.idOnly {
				t.Errorf("count page %+v differs from list page %+v", countPage, listPage)
			}
		})
	}
}
