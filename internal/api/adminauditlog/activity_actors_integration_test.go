//go:build integration

// #2414 — GET /v1/activity/actors lists the machine and system actors in
// the caller's own tenant's activity, under the same visibility rules as
// GET /v1/activity/unified.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

// seedActorRow inserts one decision_audit_log row (or, for kind
// "feature_flag", one feature_flag_audit_log row) by actor.
func seedActorRow(t *testing.T, tenantID uuid.UUID, kind, actor, action string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}
	switch kind {
	case "decision":
		_, err = tx.Exec(ctx,
			`INSERT INTO decision_audit_log
			   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
			 VALUES (gen_random_uuid(), $1, $2, $3, 'evidence', 'r-2414', 'allow')`,
			tenantID, actor, action)
	case "feature_flag":
		_, err = tx.Exec(ctx,
			`INSERT INTO feature_flag_audit_log
			   (id, tenant_id, flag_key, from_enabled, to_enabled, actor)
			 VALUES (gen_random_uuid(), $1, 'risk.enabled', true, false, $2)`,
			tenantID, actor)
	default:
		t.Fatalf("seedActorRow: unsupported kind %q", kind)
	}
	if err != nil {
		t.Fatalf("seed %s by %s: %v", kind, actor, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

func queryActors(t *testing.T, tenantID uuid.UUID, isAdmin, includeReads bool) []string {
	t.Helper()
	return getActors(t, adminauditlog.New(appPool), tenantID, isAdmin, includeReads).Actors
}

func getActors(t *testing.T, h *adminauditlog.Handler, tenantID uuid.UUID, isAdmin, includeReads bool) adminauditlog.ActivityActorsResponse {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_actors",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   uuid.NewString(),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/activity/actors", h.ActivityActors)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/actors?from=%s&to=%s", from, to)
	if includeReads {
		url += "&include_reads=true"
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.ActivityActorsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestActivityActors_TenantScopedMachineActors(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	cleanupUnifiedTables(t, tenant)
	cleanupUnifiedTables(t, other)

	seedActorRow(t, tenant, "decision", "system:scheduler", "write")
	seedActorRow(t, tenant, "decision", "oauth_client:ci", "write")
	seedActorRow(t, tenant, "decision", "system", "approve")
	seedActorRow(t, tenant, "decision", "system:reader", "read")      // read telemetry
	seedActorRow(t, tenant, "feature_flag", "system:flags", "")       // admin-only kind
	seedActorRow(t, tenant, "decision", uuid.NewString(), "write")    // a person: never listed
	seedActorRow(t, tenant, "decision", "key_2414", "write")          // an API key: never listed
	seedActorRow(t, other, "decision", "oauth_client:other", "write") // another tenant

	cases := []struct {
		name         string
		isAdmin      bool
		includeReads bool
		want         []string
	}{
		{"viewer", false, false, []string{"oauth_client:ci", "system", "system:scheduler"}},
		{"viewer with reads", false, true, []string{"oauth_client:ci", "system", "system:reader", "system:scheduler"}},
		{"admin", true, false, []string{"oauth_client:ci", "system", "system:flags", "system:scheduler"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := queryActors(t, tenant, tc.isAdmin, tc.includeReads)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("actors = %v; want %v", got, tc.want)
			}
		})
	}

	if got := queryActors(t, other, true, true); !slices.Equal(got, []string{"oauth_client:other"}) {
		t.Fatalf("other tenant actors = %v; want only its own", got)
	}
}

// #2627: the actors list applies the #2173 module filter. With Exceptions
// switched off, an actor seen only in exception rows is not offered, while
// one seen in core rows still is. With it on, both are.
func TestActivityActors_ModuleFilter(t *testing.T) {
	off, on := uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{off, on} {
		cleanupUnifiedTables(t, tenant)
		seedActorRow(t, tenant, "decision", "system:scheduler", "write")
		seedSystemExceptionRow(t, tenant, "system:exceptions")
	}
	switchExceptions(t, off, false)
	switchExceptions(t, on, true)
	views := moduleaccess.NewViews(moduleaccess.NewStore(appPool), featureflag.NewStore(appPool))
	h := adminauditlog.New(appPool).WithModules(views)

	if got := getActors(t, h, off, true, false).Actors; !slices.Equal(got, []string{"system:scheduler"}) {
		t.Fatalf("exceptions off: actors = %v; want [system:scheduler]", got)
	}
	if got := getActors(t, h, on, true, false).Actors; !slices.Equal(got, []string{"system:exceptions", "system:scheduler"}) {
		t.Fatalf("exceptions on: actors = %v; want [system:exceptions system:scheduler]", got)
	}
}

// #2627: past the 1000-actor cap the response says it is truncated rather
// than dropping actors silently; at or under the cap it does not.
func TestActivityActors_TruncatedPastCap(t *testing.T) {
	under, over := uuid.New(), uuid.New()
	cleanupUnifiedTables(t, under)
	cleanupUnifiedTables(t, over)
	seedMachineActors(t, under, 1000)
	seedMachineActors(t, over, 1001)

	if resp := getActors(t, adminauditlog.New(appPool), under, true, false); resp.Truncated || len(resp.Actors) != 1000 {
		t.Fatalf("1000 actors: truncated = %v, %d actors; want false, 1000", resp.Truncated, len(resp.Actors))
	}
	if resp := getActors(t, adminauditlog.New(appPool), over, true, false); !resp.Truncated || len(resp.Actors) != 1000 {
		t.Fatalf("1001 actors: truncated = %v, %d actors; want true, 1000", resp.Truncated, len(resp.Actors))
	}
}

// #2718: the module filter runs before the cap. A window with 1000 actors
// the caller may see and more seen only in a switched-off module's rows
// lists the 1000 and is not truncated.
func TestActivityActors_HiddenRowsDoNotUseTheCap(t *testing.T) {
	tenant := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedMachineActors(t, tenant, 1000)
	for _, actor := range []string{"system:hidden-a", "system:hidden-b", "system:hidden-c"} {
		seedSystemExceptionRow(t, tenant, actor)
	}
	// A decision on a switched-off module's route, by an actor that sorts
	// before every visible one.
	inTenantTx(t, tenant, `INSERT INTO decision_audit_log
		   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result, request_path)
		 VALUES (gen_random_uuid(), $1, 'system:a-hidden', 'write', 'exceptions', 'r-2718', 'allow', '/v1/exceptions/r-2718')`,
		tenant)
	switchExceptions(t, tenant, false)
	views := moduleaccess.NewViews(moduleaccess.NewStore(appPool), featureflag.NewStore(appPool))

	resp := getActors(t, adminauditlog.New(appPool).WithModules(views), tenant, true, false)
	if resp.Truncated || len(resp.Actors) != 1000 {
		t.Fatalf("truncated = %v, %d actors; want false, 1000", resp.Truncated, len(resp.Actors))
	}
	for _, a := range resp.Actors {
		if strings.Contains(a, "hidden") {
			t.Fatalf("actor %q is seen only in Exceptions rows, which are switched off", a)
		}
	}
}

// decisionCase is one authorization decision placed by request path and
// target type.
type decisionCase struct{ path, targetType string }

// registryDecisionCases builds decisions on every route the module
// registry lists: the route itself, a path below it, a `:verb` on it, and
// the route's last segment as a bare target type, plus paths and types no
// module owns. decision_audit_log requires a target type, so a case
// placed by its path alone carries one no module owns.
func registryDecisionCases() []decisionCase {
	seen := map[decisionCase]bool{}
	var out []decisionCase
	add := func(c decisionCase) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	routes := []string{}
	for _, m := range featureflag.Modules {
		routes = append(routes, m.Routes...)
		for _, f := range m.Features {
			routes = append(routes, f.Routes...)
		}
	}
	for _, r := range routes {
		concrete := strings.ReplaceAll(r, "*", "x2718")
		segs := strings.Split(r, "/")
		last := segs[len(segs)-1]
		add(decisionCase{concrete, "unowned-2718"})
		add(decisionCase{concrete + "/r-2718", "unowned-2718"})
		add(decisionCase{concrete + ":bulk", "unowned-2718"})
		add(decisionCase{"", last})
		add(decisionCase{"/v1/unowned-2718", last})
	}
	add(decisionCase{"/v1/unowned-2718", "unowned-2718"})
	add(decisionCase{"", "unowned-2718"})
	return out
}

// #2718: the query's module filter places every decision as entrySeen
// does, for every route in the registry, with every module and feature
// on, with them all off, and with a mix.
func TestActivityActors_ModuleFilterMatchesGo(t *testing.T) {
	cases := registryDecisionCases()
	views := moduleaccess.NewViews(moduleaccess.NewStore(appPool), featureflag.NewStore(appPool))
	h := adminauditlog.New(appPool).WithModules(views)

	scenarios := []struct {
		name string
		off  func(i int) bool // i-th switch in registry order
	}{
		{"all on", func(int) bool { return false }},
		{"all off", func(int) bool { return true }},
		{"odd off", func(i int) bool { return i%2 == 1 }},
		{"even off", func(i int) bool { return i%2 == 0 }},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			tenant := uuid.New()
			cleanupUnifiedTables(t, tenant)
			ctx, err := tenancy.WithTenant(context.Background(), tenant.String())
			if err != nil {
				t.Fatal(err)
			}
			flags := featureflag.NewStore(appPool)
			i := 0
			for _, m := range featureflag.Modules {
				keys := []string{}
				if !m.Core {
					keys = append(keys, m.FlagKey)
				}
				for _, f := range m.Features {
					keys = append(keys, f.FlagKey)
				}
				for _, k := range keys {
					if _, err := flags.Set(ctx, k, !sc.off(i), "test-2718", "test"); err != nil {
						t.Fatalf("set %s: %v", k, err)
					}
					i++
				}
			}
			for n, c := range cases {
				inTenantTx(t, tenant, `INSERT INTO decision_audit_log
					   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result, request_path)
					 VALUES (gen_random_uuid(), $1, $2, 'write', $3, 'r-2718', 'allow', $4)`,
					tenant, fmt.Sprintf("system:c%04d", n), c.targetType, c.path)
			}

			set, err := views.Switches(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
			if err != nil {
				t.Fatalf("switches: %v", err)
			}
			want := []string{}
			hidden := 0
			for n, c := range cases {
				if adminauditlog.DecisionSeen(set, c.path, c.targetType) {
					want = append(want, fmt.Sprintf("system:c%04d", n))
				} else {
					hidden++
				}
			}
			if sc.name != "all on" && hidden == 0 {
				t.Fatal("no case is hidden: the scenario does not exercise the filter")
			}
			got := getActors(t, h, tenant, true, false).Actors
			if !slices.Equal(got, want) {
				for n, c := range cases {
					a := fmt.Sprintf("system:c%04d", n)
					if slices.Contains(got, a) != slices.Contains(want, a) {
						t.Errorf("path %q target %q: listed = %v, entrySeen = %v",
							c.path, c.targetType, slices.Contains(got, a), slices.Contains(want, a))
					}
				}
				t.Fatalf("SQL module filter disagrees with entrySeen (%d listed, %d want)", len(got), len(want))
			}
		})
	}
}

// seedSystemExceptionRow inserts one exception_audit_log row by actor.
func seedSystemExceptionRow(t *testing.T, tenantID uuid.UUID, actor string) {
	t.Helper()
	inTenantTx(t, tenantID, `INSERT INTO exception_audit_log
		   (id, tenant_id, exception_id, action, actor, to_state)
		 VALUES (gen_random_uuid(), $1, gen_random_uuid(), 'requested', $2, 'requested')`,
		tenantID, actor)
}

// seedMachineActors inserts n decision rows, each by its own
// system:<process> actor.
func seedMachineActors(t *testing.T, tenantID uuid.UUID, n int) {
	t.Helper()
	inTenantTx(t, tenantID, `INSERT INTO decision_audit_log
		   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
		 SELECT gen_random_uuid(), $1, 'system:p' || lpad(i::text, 5, '0'), 'write', 'evidence', 'r-2627', 'allow'
		 FROM generate_series(1, $2::int) AS i`,
		tenantID, n)
}

func inTenantTx(t *testing.T, tenantID uuid.UUID, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}
