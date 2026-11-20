//go:build integration

package adminauditlog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// storeDown fails every switch read as the flag store does when its
// database read fails.
type storeDown struct{}

func (storeDown) Get(context.Context, string) (featureflag.Flag, error) {
	return featureflag.Flag{}, featureflag.ErrStoreUnavailable
}

// newModulesRouter serves the Activity feed and the admin unified log to a
// tenant admin, with the module views over the given switch store.
func newModulesRouter(t *testing.T, tenantID uuid.UUID, flags featureflag.Looker) http.Handler {
	t.Helper()
	views := moduleaccess.NewViews(moduleaccess.NewStore(appPool), flags)
	h := adminauditlog.New(appPool).WithModules(views)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_modules_2590",
				TenantID: tenantID.String(),
				IsAdmin:  true,
				UserID:   uuid.NewString(),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/activity/unified", h.ActivityList)
	r.Get("/v1/admin/audit-log/unified", h.UnifiedList)
	return r
}

func getUnified(t *testing.T, h http.Handler, path string, q url.Values) (int, adminauditlog.UnifiedListResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+q.Encode(), nil))
	var out adminauditlog.UnifiedListResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rec.Code, out
}

// switchExceptions stores the Exceptions module switch for tenantID.
func switchExceptions(t *testing.T, tenantID uuid.UUID, on bool) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := featureflag.NewStore(appPool).Set(ctx, "exceptions.enabled", on, "test-2590", "test"); err != nil {
		t.Fatalf("set exceptions.enabled=%v: %v", on, err)
	}
}

// #2590: two tenants, each with three exception rows and three evidence
// rows. Exceptions is switched off for one and on for the other.
//
//   - Activity leaves the switched-off module's rows out, and fills each
//     page with rows the caller can see: every page but the last is
//     full, and the last carries no cursor.
//   - The admin unified log keeps every row and marks the switched-off
//     module's rows module_switched_off.
//   - With the switch store down, both fail closed (503
//     module_state_unavailable, #2575) rather than falling back to the
//     seed default, which is on.
func TestModules2590_SwitchedOffModuleInActivityAndAdminLog(t *testing.T) {
	off, on := uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{off, on} {
		cleanupAuditTables(t, tenant)
		for range 3 {
			seedAuditRow(t, tenant, "exception_audit_log")
			seedAuditRow(t, tenant, "evidence_audit_log")
		}
	}
	switchExceptions(t, off, false)
	switchExceptions(t, on, true)
	flags := featureflag.NewStore(appPool)
	now := time.Now().UTC()
	kinds := url.Values{
		"kind":  {"exception,evidence"},
		"limit": {"2"},
		"from":  {now.Add(-time.Hour).Format(time.RFC3339)},
		"to":    {now.Add(time.Hour).Format(time.RFC3339)},
	}
	query := func(over map[string]string) url.Values {
		q := url.Values{}
		for k, v := range kinds {
			q[k] = append([]string(nil), v...)
		}
		for k, v := range over {
			q.Set(k, v)
		}
		return q
	}

	// Activity, paged two at a time.
	count := func(tenant uuid.UUID) map[string]int {
		h := newModulesRouter(t, tenant, flags)
		seen := map[string]int{}
		q := query(nil)
		for page := 0; ; page++ {
			code, resp := getUnified(t, h, "/v1/activity/unified", q)
			if code != http.StatusOK {
				t.Fatalf("activity page %d: status %d", page, code)
			}
			for _, e := range resp.Entries {
				seen[e.Kind]++
			}
			if resp.NextCursor == "" {
				return seen
			}
			if len(resp.Entries) != 2 {
				t.Errorf("tenant %s page %d: %d rows with a next cursor; want a full page of 2", tenant, page, len(resp.Entries))
			}
			q.Set("cursor", resp.NextCursor)
			if page > 10 {
				t.Fatal("activity paging did not end")
			}
		}
	}
	if got := count(off); got["exception"] != 0 || got["evidence"] != 3 {
		t.Errorf("switched off: activity kinds = %v; want 0 exception, 3 evidence", got)
	}
	if got := count(on); got["exception"] != 3 || got["evidence"] != 3 {
		t.Errorf("switched on: activity kinds = %v; want 3 exception, 3 evidence", got)
	}

	// The admin unified log keeps every row and marks the switched-off ones.
	marks := func(tenant uuid.UUID) (exceptions, marked, markedEvidence int) {
		code, resp := getUnified(t, newModulesRouter(t, tenant, flags), "/v1/admin/audit-log/unified",
			query(map[string]string{"limit": "50"}))
		if code != http.StatusOK {
			t.Fatalf("admin unified: status %d", code)
		}
		for _, e := range resp.Entries {
			if e.Kind == "exception" {
				exceptions++
			}
			if e.ModuleSwitchedOff {
				marked++
				if e.Kind != "exception" {
					markedEvidence++
				}
			}
		}
		return exceptions, marked, markedEvidence
	}
	if x, m, me := marks(off); x != 3 || m != 3 || me != 0 {
		t.Errorf("switched off: admin log %d exception rows, %d marked, %d core marked; want 3, 3, 0", x, m, me)
	}
	if x, m, _ := marks(on); x != 3 || m != 0 {
		t.Errorf("switched on: admin log %d exception rows, %d marked; want 3, 0", x, m)
	}

	// Store down: both fail closed.
	for _, path := range []string{"/v1/activity/unified", "/v1/admin/audit-log/unified"} {
		if code, _ := getUnified(t, newModulesRouter(t, on, storeDown{}), path, kinds); code != http.StatusServiceUnavailable {
			t.Errorf("store down: %s status %d; want 503", path, code)
		}
	}
}
