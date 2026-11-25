//go:build integration

package admindemo_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/demoseed"
)

// #3094: POST /v1/admin/demo/teardown while another session writes to
// audit_periods gives up on the lock, answers 503 with Retry-After and the
// teardown_busy code instead of a generic 500, and leaves the demo tenant in
// place for the retry.
func TestTeardown_BusyAuditPeriodsGets503WithRetryAfter(t *testing.T) {
	resetDemoAuditRows(t)
	sessionTenant := seedTenant(t, "3094 busy teardown")
	actor := uuid.New()
	seedActorUser(t, sessionTenant, actor)
	cleanupDemoTenant(t, "demo")
	ctx := context.Background()

	seeder, err := demoseed.NewSeeder(adminPool, demoseed.DefaultScale)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}
	if _, err := seeder.Apply(ctx, demoseed.ApplyInput{Slug: "demo"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	holder, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `LOCK TABLE audit_periods IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatalf("holder lock: %v", err)
	}

	h := newRouter(t, sessionTenant, actor, true, true)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/demo/teardown")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%v", res.StatusCode, body)
	}
	if got := res.Header.Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want 5", got)
	}
	if body["code"] != "teardown_busy" {
		t.Errorf("code = %v, want teardown_busy", body["code"])
	}
	if !demoTenantExists(t, "demo") {
		t.Error("demo tenant gone after a busy teardown; it must roll back")
	}
}
