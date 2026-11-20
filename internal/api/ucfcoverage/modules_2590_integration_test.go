//go:build integration

package ucfcoverage_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #2590: control coverage names no exception, whether the Exceptions
// module is switched off for the tenant (tenantA) or on (tenantB). The
// response is built from the control, its anchor and requirements, and
// evaluation results; an exception on the control never appears in it.
func TestControlCoverage2590_NamesNoException(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	flags := featureflag.NewStore(dbtest.NewAppPool(t))
	migrate := dbtest.NewMigratePool(t)

	for tenant, on := range map[string]bool{tenantA: false, tenantB: true} {
		ctx, err := tenancy.WithTenant(context.Background(), tenant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := flags.Set(ctx, "exceptions.enabled", on, "test-2590", "test"); err != nil {
			t.Fatalf("switch exceptions.enabled=%v: %v", on, err)
		}
		t.Cleanup(func() {
			_, _ = flags.Set(ctx, "exceptions.enabled", true, "test-2590", "cleanup")
		})

		cid := seedControl(t, tenant, "IAC-37", "test-2590-"+tenant[:4], "MFA Enforcement")
		exID := uuid.New()
		if _, err := migrate.Exec(context.Background(), `
			INSERT INTO exceptions (
				id, tenant_id, control_id, scope_cell_predicate,
				justification, requested_by, expires_at, status
			)
			VALUES ($1, $2, $3, '{}'::jsonb, '#2590 test exception', 'requester',
			        now() + INTERVAL '15 days', 'active')
		`, exID, tenant, cid); err != nil {
			t.Fatalf("seed exception: %v", err)
		}

		ts, bearer := setupHTTPServer(t, tenant)
		resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("exceptions on=%v: status %d; body=%s", on, resp.StatusCode, body)
		}
		if bytes.Contains(body, []byte(exID.String())) || bytes.Contains(body, []byte("#2590 test exception")) {
			t.Errorf("exceptions on=%v: coverage names the exception: %s", on, body)
		}
	}
}
